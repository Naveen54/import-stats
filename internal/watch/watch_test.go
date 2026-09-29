package watch

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestWatcherDetectsCreatedModifiedDeleted(t *testing.T) {
	root := t.TempDir()
	file := writeWatchFile(t, root, "src/app.js", "one")
	w, cancel := startTestWatcher(t, root, []string{".js"})
	defer cancel()

	writeWatchFile(t, root, "src/added.js", "new")
	ev := waitWatchEvent(t, w.Events())
	wantAdded := filepath.Join(root, "src", "added.js")
	if !reflect.DeepEqual(ev.Added, []string{wantAdded}) {
		t.Fatalf("added = %v, want %v", ev.Added, []string{wantAdded})
	}

	writeWatchFile(t, root, "src/app.js", "changed-size")
	ev = waitWatchEvent(t, w.Events())
	if !reflect.DeepEqual(ev.Changed, []string{file}) {
		t.Fatalf("changed = %v, want %v", ev.Changed, []string{file})
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	ev = waitWatchEvent(t, w.Events())
	if !reflect.DeepEqual(ev.Removed, []string{file}) {
		t.Fatalf("removed = %v, want %v", ev.Removed, []string{file})
	}
}

func TestWatcherIgnoresIrrelevantExtensions(t *testing.T) {
	root := t.TempDir()
	w, cancel := startTestWatcher(t, root, []string{".js"})
	defer cancel()

	writeWatchFile(t, root, "src/style.css", "body{}")
	assertNoWatchEvent(t, w.Events(), 80*time.Millisecond)
}

func TestWatcherIgnoresNodeModules(t *testing.T) {
	root := t.TempDir()
	w, cancel := startTestWatcher(t, root, []string{".js"})
	defer cancel()

	writeWatchFile(t, root, "node_modules/pkg/index.js", "export {}")
	writeWatchFile(t, root, "src/ok.js", "export {}")
	ev := waitWatchEvent(t, w.Events())
	want := filepath.Join(root, "src", "ok.js")
	if !reflect.DeepEqual(ev.Added, []string{want}) {
		t.Fatalf("added = %v, want only %v", ev.Added, want)
	}
}

func TestWatcherDebouncesBurst(t *testing.T) {
	root := t.TempDir()
	w, cancel := startTestWatcher(t, root, []string{".js"})
	defer cancel()

	paths := []string{"src/a.js", "src/b.js", "src/c.js"}
	for _, p := range paths {
		writeWatchFile(t, root, p, p)
	}
	ev := waitWatchEvent(t, w.Events())
	if got := len(ev.Added); got != 3 {
		t.Fatalf("added count = %d, want 3; event=%+v", got, ev)
	}
	assertNoWatchEvent(t, w.Events(), 80*time.Millisecond)
}

func TestWatcherCancellationClosesChannel(t *testing.T) {
	root := t.TempDir()
	w, cancel := startTestWatcher(t, root, []string{".js"})
	before := runtime.NumGoroutine()
	cancel()
	select {
	case _, ok := <-w.Events():
		if ok {
			t.Fatal("events channel still open")
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("possible goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestWatcherSymlinkLoopDoesNotHang(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, root, "src/app.js", "one")
	loop := filepath.Join(root, "src", "loop")
	if err := os.Symlink(filepath.Join(root, "src"), loop); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	w, cancel := startTestWatcher(t, root, []string{".js"})
	defer cancel()

	writeWatchFile(t, root, "src/next.js", "two")
	select {
	case <-w.Events():
	case <-time.After(time.Second):
		t.Fatal("watcher hung on symlink loop")
	}
}

func TestWatcherNonExistentRootDoesNotPanic(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	w, err := New(Options{Roots: []string{root}, Exts: []string{".js"}, Interval: 10 * time.Millisecond, Debounce: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Start(ctx)
	}()
	assertNoWatchEvent(t, w.Events(), 50*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop for missing root")
	}
}

func startTestWatcher(t *testing.T, root string, exts []string) (*Watcher, context.CancelFunc) {
	t.Helper()
	w, err := New(Options{Roots: []string{root}, Exts: exts, Interval: 10 * time.Millisecond, Debounce: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)
	// Let the initial snapshot complete so tests assert changes after startup.
	time.Sleep(25 * time.Millisecond)
	return w, cancel
}

func writeWatchFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func waitWatchEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("events channel closed")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch event")
	}
	return Event{}
}

func assertNoWatchEvent(t *testing.T, ch <-chan Event, d time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		t.Fatalf("unexpected event ok=%v event=%+v", ok, ev)
	case <-time.After(d):
	}
}
