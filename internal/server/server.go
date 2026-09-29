// Package server serves the embedded dashboard and the report JSON API.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"importstats/internal/analyzer"
	"importstats/internal/watch"
)

//go:embed all:web
var assets embed.FS

// Reanalyzer re-runs the analysis from scratch. It may be nil, in which case
// the dashboard's re-analyse action is disabled.
type Reanalyzer func() (*analyzer.Report, error)

// state holds the currently served report and serialises re-analysis so two
// concurrent requests cannot run overlapping walks.
type state struct {
	mu      sync.RWMutex
	payload []byte
	again   Reanalyzer
	live    *Broadcaster
}

func (s *state) current() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.payload
}

// refresh re-runs the analysis and swaps in the new payload.
func (s *state) refresh() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.again == nil {
		return nil, fmt.Errorf("re-analysis is not available")
	}
	rep, err := s.again()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	s.payload = payload
	return payload, nil
}

// Serve starts the dashboard on the given port (0 picks a free one) and blocks.
// again may be nil to disable the re-analyse endpoint.
func Serve(rep *analyzer.Report, again Reanalyzer, port int, open bool) error {
	return serve(rep, again, nil, port, open)
}

// ServeWatching serves the dashboard and publishes a notification after
// debounced source changes trigger a fresh analysis.
func ServeWatching(rep *analyzer.Report, again Reanalyzer, roots []string, port int, open bool) error {
	return serve(rep, again, roots, port, open)
}

func serve(rep *analyzer.Report, again Reanalyzer, roots []string, port int, open bool) error {
	payload, err := json.Marshal(rep)
	if err != nil {
		return err
	}

	st := &state{payload: payload, again: again}
	var watcher *watch.Watcher
	if len(roots) > 0 && again != nil {
		watcher, err = watch.New(watch.Options{Roots: roots})
		if err != nil {
			return err
		}
		st.live = NewBroadcaster(4)
		go func() {
			for event := range watcher.Events() {
				if _, err := st.refresh(); err == nil {
					st.live.Publish(LiveUpdate{Changed: event.Changed, Added: event.Added, Removed: event.Removed, At: event.At})
				}
			}
		}()
		go watcher.Start(context.Background())
	}
	mux, err := handler(st)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("cannot listen on port %d: %w", port, err)
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	fmt.Printf("Dashboard ready at %s  (Ctrl+C to stop)\n", url)

	if open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(url)
		}()
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

func handler(st *state) (http.Handler, error) {
	sub, err := fs.Sub(assets, "web")
	if err != nil {
		return nil, err
	}

	writeJSON := func(w http.ResponseWriter, payload []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(payload)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/report", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, st.current())
	})

	mux.HandleFunc("/api/reanalyze", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		payload, err := st.refresh()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, payload)
	})
	if st.live != nil {
		mux.Handle("/api/live", LiveHandler(st.live.Subscriber()))
	}

	return mux, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
