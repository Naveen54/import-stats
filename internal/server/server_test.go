package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"importstats/internal/analyzer"
)

func serverReportFixture() *analyzer.Report {
	return &analyzer.Report{
		Summary: analyzer.Summary{
			Entry:          "src/main.tsx",
			ProjectName:    "fixture-app",
			ProjectRoot:    "/project",
			SrcRoot:        "/project/src",
			FilesAnalyzed:  3,
			SrcFiles:       4,
			TotalImports:   5,
			PackageImports: 2,
			LocalImports:   3,
			UniquePackages: 1,
			GeneratedAt:    "2026-09-21T08:00:00Z",
			DurationMillis: 12,
		},
		Packages: []analyzer.PackageStat{
			{
				Name:    "react",
				Imports: 2,
				Files:   1,
				Version: "18.2.0",
				DepType: "dependency",
				Kinds:   map[string]int{"named": 2},
				Sites: []analyzer.ImportSite{
					{File: "src/main.tsx", Line: 1, Kind: "named", Spec: "react", Symbols: []string{"useState"}},
				},
			},
		},
	}
}

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	return testServerWith(t, nil)
}

func testServerWith(t *testing.T, again Reanalyzer) *httptest.Server {
	t.Helper()
	payload, err := json.Marshal(serverReportFixture())
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	h, err := handler(&state{payload: payload, again: again})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	return httptest.NewServer(h)
}

func TestDashboardIndex(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()

	res, body := get(t, ts.URL+"/")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200; body=%s", res.StatusCode, body)
	}
	if !strings.Contains(body, "<title>importstats") || !strings.Contains(body, "Package imports") {
		t.Fatalf("GET / did not return dashboard markup:\n%s", body)
	}
}

func TestEmbeddedAssets(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()

	for _, path := range []string{"/app.css", "/app.js"} {
		res, body := get(t, ts.URL+path)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, res.StatusCode, body)
		}
		if body == "" {
			t.Fatalf("GET %s returned an empty body", path)
		}
	}
}

func TestAPIReport(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()

	res, body := get(t, ts.URL+"/api/report")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/report status = %d, want 200; body=%s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got analyzer.Report
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("Unmarshal(/api/report) error = %v; body=%s", err, body)
	}
	if got.Summary.ProjectName != "fixture-app" || got.Summary.TotalImports != 5 {
		t.Fatalf("summary = %+v", got.Summary)
	}
	if len(got.Packages) != 1 || got.Packages[0].Name != "react" || got.Packages[0].Sites[0].File != "src/main.tsx" {
		t.Fatalf("packages = %+v", got.Packages)
	}
}

func TestMissingAssetReturns404(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()

	res, _ := get(t, ts.URL+"/does-not-exist.js")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing asset status = %d, want 404", res.StatusCode)
	}
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s) error = %v", url, err)
	}
	return res, string(body)
}

func post(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	res, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s error = %v", url, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s) error = %v", url, err)
	}
	return res, string(body)
}

func TestReanalyzeReturnsFreshReport(t *testing.T) {
	var calls int32
	ts := testServerWith(t, func() (*analyzer.Report, error) {
		atomic.AddInt32(&calls, 1)
		rep := serverReportFixture()
		rep.Summary.FilesAnalyzed = 99
		rep.Summary.GeneratedAt = "2030-01-02T03:04:05Z"
		return rep, nil
	})
	defer ts.Close()

	res, body := post(t, ts.URL+"/api/reanalyze")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/reanalyze status = %d, want 200; body=%s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got analyzer.Report
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Summary.FilesAnalyzed != 99 {
		t.Errorf("filesAnalyzed = %d, want 99", got.Summary.FilesAnalyzed)
	}
	if got.Summary.GeneratedAt != "2030-01-02T03:04:05Z" {
		t.Errorf("generatedAt = %q", got.Summary.GeneratedAt)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("reanalyzer called %d times, want 1", n)
	}

	// The cached report served at /api/report must reflect the new run.
	res2, body2 := get(t, ts.URL+"/api/report")
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/report status = %d", res2.StatusCode)
	}
	var cached analyzer.Report
	if err := json.Unmarshal([]byte(body2), &cached); err != nil {
		t.Fatalf("unmarshal cached: %v", err)
	}
	if cached.Summary.FilesAnalyzed != 99 {
		t.Errorf("cached filesAnalyzed = %d, want 99 after re-analysis", cached.Summary.FilesAnalyzed)
	}
}

func TestReanalyzeRejectsGet(t *testing.T) {
	ts := testServerWith(t, func() (*analyzer.Report, error) {
		t.Error("reanalyzer must not run for GET")
		return serverReportFixture(), nil
	})
	defer ts.Close()

	res, _ := get(t, ts.URL+"/api/reanalyze")
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/reanalyze status = %d, want 405", res.StatusCode)
	}
	if allow := res.Header.Get("Allow"); allow != http.MethodPost {
		t.Errorf("Allow = %q, want POST", allow)
	}
}

func TestReanalyzeReportsFailureAndKeepsPreviousReport(t *testing.T) {
	ts := testServerWith(t, func() (*analyzer.Report, error) {
		return nil, errors.New("entry vanished")
	})
	defer ts.Close()

	res, body := post(t, ts.URL+"/api/reanalyze")
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(payload["error"], "entry vanished") {
		t.Errorf("error = %q, want it to mention the cause", payload["error"])
	}

	// A failed run must leave the previously served report intact.
	res2, body2 := get(t, ts.URL+"/api/report")
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/report status = %d", res2.StatusCode)
	}
	var cached analyzer.Report
	if err := json.Unmarshal([]byte(body2), &cached); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cached.Summary.FilesAnalyzed != serverReportFixture().Summary.FilesAnalyzed {
		t.Errorf("cached report changed after a failed re-analysis")
	}
}

func TestReanalyzeDisabledWhenNil(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()

	res, body := post(t, ts.URL+"/api/reanalyze")
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", res.StatusCode, body)
	}
}

// TestReanalyzeConcurrent proves overlapping requests never run the analysis
// concurrently, which would race on the shared resolver caches.
func TestReanalyzeConcurrent(t *testing.T) {
	var running, maxRunning, total int32
	ts := testServerWith(t, func() (*analyzer.Report, error) {
		n := atomic.AddInt32(&running, 1)
		for {
			old := atomic.LoadInt32(&maxRunning)
			if n <= old || atomic.CompareAndSwapInt32(&maxRunning, old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&total, 1)
		atomic.AddInt32(&running, -1)
		return serverReportFixture(), nil
	})
	defer ts.Close()

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := post(t, ts.URL+"/api/reanalyze")
			if res.StatusCode != http.StatusOK {
				t.Errorf("status = %d", res.StatusCode)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxRunning); got != 1 {
		t.Errorf("max concurrent analyses = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&total); got != 6 {
		t.Errorf("completed analyses = %d, want 6", got)
	}
}
