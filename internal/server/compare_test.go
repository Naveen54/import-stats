package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	cmp "importstats/internal/compare"
)

func comparisonFixture() *cmp.Report {
	return &cmp.Report{
		GeneratedAt: "2026-09-21T09:00:00Z",
		Projects: []cmp.Project{
			{Name: "admin", Root: "/apps/admin", Entry: "src/index.js", Files: 4, Imports: 8, Packages: 2},
			{Name: "web", Root: "/apps/web", Entry: "src/index.js", Files: 3, Imports: 5, Packages: 2},
			{Name: "broken", Root: "/apps/broken", Error: "entry not found"},
		},
		Rows: []cmp.Row{
			{
				Package: "react",
				Cells: map[string]cmp.Cell{
					"admin": {Present: true, Version: "^18.2.0", Imports: 3, Files: 2},
					"web":   {Present: true, Version: "^16.14.0", Imports: 2, Files: 1},
				},
				Used: 2, Imports: 5, Drift: true, Versions: []string{"^16.14.0", "^18.2.0"},
			},
			{
				Package: "only-admin",
				Cells: map[string]cmp.Cell{
					"admin": {Present: true, Version: "1.0.0", Imports: 1, Files: 1},
				},
				Used: 1, Imports: 1, Versions: []string{"1.0.0"},
			},
		},
		DriftCount:  1,
		SharedCount: 1,
		UniqueCount: 1,
	}
}

func testComparisonServer(t *testing.T, rep *cmp.Report) *httptest.Server {
	t.Helper()
	payload, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	h, err := comparisonHandler(payload)
	if err != nil {
		t.Fatalf("comparisonHandler() error = %v", err)
	}
	return httptest.NewServer(h)
}

func getComparison(t *testing.T, url string) (*http.Response, string) {
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

func TestComparisonAPIReport(t *testing.T) {
	ts := testComparisonServer(t, comparisonFixture())
	defer ts.Close()

	res, body := getComparison(t, ts.URL+"/api/comparison")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/comparison status = %d, want 200; body=%s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got cmp.Report
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("Unmarshal(/api/comparison) error = %v; body=%s", err, body)
	}
	if got.GeneratedAt != "2026-09-21T09:00:00Z" || got.DriftCount != 1 || len(got.Rows) != 2 {
		t.Fatalf("comparison = %+v", got)
	}
	if got.Rows[0].Package != "react" || got.Rows[0].Cells["web"].Version != "^16.14.0" {
		t.Fatalf("rows = %+v", got.Rows)
	}
}

func TestComparisonIndexServesHTML(t *testing.T) {
	ts := testComparisonServer(t, comparisonFixture())
	defer ts.Close()

	res, body := getComparison(t, ts.URL+"/")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200; body=%s", res.StatusCode, body)
	}
	for _, want := range []string{"<title>importstats", "Project comparison", "Package matrix", "compare.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET / missing %q:\n%s", want, body)
		}
	}
}

func TestComparisonNilReportDoesNotPanic(t *testing.T) {
	ts := testComparisonServer(t, nil)
	defer ts.Close()

	res, body := getComparison(t, ts.URL+"/api/comparison")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/comparison status = %d, want 200; body=%s", res.StatusCode, body)
	}
	if strings.TrimSpace(body) != "null" {
		t.Fatalf("nil report JSON = %q, want null", body)
	}
}

func TestComparisonUnknownRouteReturns404(t *testing.T) {
	ts := testComparisonServer(t, comparisonFixture())
	defer ts.Close()

	res, body := getComparison(t, ts.URL+"/missing.js")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing asset status = %d, want 404; body=%s", res.StatusCode, body)
	}
}

func TestComparisonJSONDeterministic(t *testing.T) {
	ts := testComparisonServer(t, comparisonFixture())
	defer ts.Close()

	_, first := getComparison(t, ts.URL+"/api/comparison")
	_, second := getComparison(t, ts.URL+"/api/comparison")
	if first != second {
		t.Fatalf("JSON differed across requests:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestManualServeComparison(t *testing.T) {
	path := os.Getenv("IMPORTSTATS_SERVE_COMPARISON_JSON")
	if path == "" {
		t.Skip("set IMPORTSTATS_SERVE_COMPARISON_JSON to serve a comparison report")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var rep cmp.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	port := 7810
	if raw := os.Getenv("IMPORTSTATS_SERVE_COMPARISON_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid IMPORTSTATS_SERVE_COMPARISON_PORT: %v", err)
		}
		port = parsed
	}
	if err := ServeComparison(&rep, port, false); err != nil {
		t.Fatalf("ServeComparison() error = %v", err)
	}
}
