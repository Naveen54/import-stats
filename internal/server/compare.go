package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"time"

	"importstats/internal/compare"
)

//go:embed web/compare.html web/compare.js web/compare.css
var comparisonAssets embed.FS

// ServeComparison serves the comparison dashboard and blocks.
func ServeComparison(rep *compare.Report, port int, open bool) error {
	payload, err := json.Marshal(rep)
	if err != nil {
		return err
	}

	mux, err := comparisonHandler(payload)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("cannot listen on port %d: %w", port, err)
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	fmt.Printf("Comparison dashboard ready at %s  (Ctrl+C to stop)\n", url)

	if open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(url)
		}()
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

func comparisonHandler(payload []byte) (http.Handler, error) {
	sub, err := fs.Sub(comparisonAssets, "web")
	if err != nil {
		return nil, err
	}

	assets := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			assets.ServeHTTP(w, r)
			return
		}
		http.ServeFileFS(w, r, sub, "compare.html")
	})
	mux.HandleFunc("/api/comparison", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(payload)
	})
	return mux, nil
}
