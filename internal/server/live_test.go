package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"importstats/internal/watch"
)

func TestLiveHandlerHeaders(t *testing.T) {
	updates := make(chan LiveUpdate)
	ts := httptest.NewServer(LiveHandler(func() (<-chan LiveUpdate, func()) { return updates, func() {} }))
	defer ts.Close()

	res, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	checks := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for name, want := range checks {
		if got := res.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestLiveHandlerFramesEvent(t *testing.T) {
	updates := make(chan LiveUpdate, 1)
	ts := httptest.NewServer(LiveHandler(func() (<-chan LiveUpdate, func()) { return updates, func() {} }))
	defer ts.Close()

	res, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	updates <- LiveUpdate{Changed: []string{"/abs/app.js"}, At: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}

	frame := readUntilContains(t, res.Body, `"changed":["/abs/app.js"]`)
	if !strings.Contains(frame, "event: update\n") || !strings.Contains(frame, `"changed":["/abs/app.js"]`) {
		t.Fatalf("bad SSE frame:\n%s", frame)
	}
}

func TestLiveHandlerHeartbeats(t *testing.T) {
	old := liveHeartbeatInterval
	liveHeartbeatInterval = 10 * time.Millisecond
	defer func() { liveHeartbeatInterval = old }()

	updates := make(chan LiveUpdate)
	ts := httptest.NewServer(LiveHandler(func() (<-chan LiveUpdate, func()) { return updates, func() {} }))
	defer ts.Close()

	res, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	got := readUntilContains(t, res.Body, ": ping\n\n")
	if !strings.Contains(got, ": ping\n\n") {
		t.Fatalf("heartbeat missing from %q", got)
	}
}

func TestLiveHandlerDisconnectUnsubscribesAndDoesNotLeak(t *testing.T) {
	var active int32
	updates := make(chan LiveUpdate)
	ts := httptest.NewServer(LiveHandler(func() (<-chan LiveUpdate, func()) {
		atomic.AddInt32(&active, 1)
		return updates, func() { atomic.AddInt32(&active, -1) }
	}))
	defer ts.Close()

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&active); got != 1 {
		t.Fatalf("active subscribers = %d, want 1", got)
	}
	cancel()
	_ = res.Body.Close()

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&active) == 0 })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+4 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("possible goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestLiveHandlerTwoClientsReceiveEvents(t *testing.T) {
	b := NewBroadcaster(2)
	ts := httptest.NewServer(LiveHandler(b.Subscriber()))
	defer ts.Close()

	res1 := openSSE(t, ts.URL)
	defer res1.Body.Close()
	res2 := openSSE(t, ts.URL)
	defer res2.Body.Close()

	b.Publish(LiveUpdate{Added: []string{"/one.js"}, At: time.Now()})
	for i, res := range []*http.Response{res1, res2} {
		got := readUntilContains(t, res.Body, `"added":["/one.js"]`)
		if !strings.Contains(got, `"added":["/one.js"]`) {
			t.Fatalf("client %d frame = %q", i+1, got)
		}
	}
}

func TestBroadcasterSlowClientDoesNotBlockOthers(t *testing.T) {
	b := NewBroadcaster(1)
	slow, slowUnsub := b.Subscriber()()
	defer slowUnsub()
	fast, fastUnsub := b.Subscriber()()
	defer fastUnsub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			b.Publish(LiveUpdate{Changed: []string{fmt.Sprintf("/%d.js", i)}, At: time.Now()})
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}
	select {
	case <-fast:
	case <-time.After(time.Second):
		t.Fatal("fast subscriber did not receive an update")
	}
	select {
	case <-slow:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber buffer did not retain a coalesced update")
	}
}

func TestLiveHandlerWithRealWatcherEndToEnd(t *testing.T) {
	root := t.TempDir()
	writeFileForLive(t, root, "src/app.js", "one")
	w, err := watchNewForLive(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	time.Sleep(25 * time.Millisecond)

	b := NewBroadcaster(2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range w.Events() {
			b.Publish(LiveUpdate{Changed: ev.Changed, Added: ev.Added, Removed: ev.Removed, At: ev.At})
		}
	}()

	ts := httptest.NewServer(LiveHandler(b.Subscriber()))
	defer ts.Close()
	res := openSSE(t, ts.URL)
	defer res.Body.Close()

	writeFileForLive(t, root, "src/added.js", "two")
	got := readUntilContains(t, res.Body, "added.js")
	if !strings.Contains(got, "added.js") {
		t.Fatalf("watcher-to-SSE frame did not mention touched file: %q", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch bridge did not stop")
	}
}

func openSSE(t *testing.T, url string) *http.Response {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readUntilContains(t *testing.T, r io.Reader, needle string) string {
	t.Helper()
	reader := bufio.NewReader(r)
	out := strings.Builder{}
	for {
		partCh := make(chan string, 1)
		errCh := make(chan error, 1)
		go func() {
			part, err := reader.ReadString('\n')
			if err != nil {
				errCh <- err
				return
			}
			partCh <- part
		}()
		select {
		case part := <-partCh:
			out.WriteString(part)
			if strings.Contains(out.String(), needle) {
				return out.String()
			}
		case err := <-errCh:
			t.Fatalf("read SSE: %v", err)
		case <-time.After(time.Second):
			t.Fatal("timed out reading SSE")
		}
	}
}

func watchNewForLive(root string) (*watch.Watcher, error) {
	return watch.New(watch.Options{Roots: []string{root}, Exts: []string{".js"}, Interval: 10 * time.Millisecond, Debounce: 20 * time.Millisecond})
}

func writeFileForLive(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
