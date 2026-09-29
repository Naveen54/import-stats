package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func reportFixture() *analyzer.Report {
	return &analyzer.Report{
		Summary: analyzer.Summary{
			Entry:          "src/main.tsx",
			ProjectName:    "fixture-app",
			ProjectRoot:    "/project",
			SrcRoot:        "/project/src",
			FilesAnalyzed:  7,
			SrcFiles:       9,
			OrphanFiles:    1,
			TotalImports:   12,
			PackageImports: 8,
			LocalImports:   2,
			StyleImports:   1,
			AssetImports:   1,
			UniquePackages: 2,
			Cycles:         1,
			UnusedDeps:     1,
			UndeclaredDeps: 1,
			Warnings:       1,
			GeneratedAt:    "2026-09-21T08:00:00Z",
			DurationMillis: 42,
		},
		Packages: []analyzer.PackageStat{
			{
				Name:    "@scope/pkg, \"quoted\"",
				Imports: 4,
				Files:   2,
				Version: "1.2.3",
				DepType: "dependency",
				Subpaths: []string{
					"lib/a",
				},
				Symbols: []analyzer.SymbolUse{
					{Name: "Alpha", Count: 6},
					{Name: "Beta", Count: 5},
					{Name: "Gamma", Count: 4},
					{Name: "Delta", Count: 3},
					{Name: "Epsilon", Count: 2},
					{Name: "Zeta", Count: 1},
				},
				Kinds: map[string]int{"named": 3, "default": 1},
				Sites: []analyzer.ImportSite{
					{File: "src/b.ts", Line: 4, Kind: "named", Spec: "@scope/pkg, \"quoted\"", Symbols: []string{"Alpha"}},
					{File: "src/a.ts", Line: 2, Kind: "named", Spec: "@scope/pkg, \"quoted\"", Symbols: []string{"Beta"}},
					{File: "src/a.ts", Line: 8, Kind: "default", Spec: "@scope/pkg, \"quoted\""},
				},
			},
			{
				Name:    "react",
				Imports: 2,
				Files:   1,
				Version: "18.2.0",
				DepType: "dependency",
				Symbols: []analyzer.SymbolUse{
					{Name: "useState", Count: 2},
				},
				Kinds: map[string]int{"named": 2},
				Sites: []analyzer.ImportSite{
					{File: "src/app.tsx", Line: 1, Kind: "named", Spec: "react", Symbols: []string{"useState"}},
				},
			},
		},
		InternalFiles: []analyzer.FileStat{
			{File: "src/util.ts", FanIn: 1, FanOut: 0, Imports: 1},
		},
		Orphans:        []string{"src/unused.ts"},
		Cycles:         [][]string{{"src/a.ts", "src/b.ts"}},
		UnusedDeps:     []string{"left-pad"},
		UndeclaredDeps: []string{"missing-pkg"},
		Warnings: []analyzer.Warning{
			{File: "src/a.ts", Line: 10, Spec: "missing-pkg", Kind: "named", Type: "missing", Hint: "install it"},
		},
		Assets: []analyzer.PackageStat{
			{Name: ".css", Imports: 1, Files: 1, DepType: "style", Kinds: map[string]int{"side-effect": 1}},
		},
	}
}

func TestWriteJSONRoundTripsIndented(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := WriteJSON(reportFixture(), path); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("WriteJSON output is not valid JSON: %s", data)
	}
	if !bytes.Contains(data, []byte("\n  \"summary\"")) {
		t.Fatalf("WriteJSON output is not indented:\n%s", data)
	}

	var got analyzer.Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.Summary.TotalImports != 12 || got.Summary.UniquePackages != 2 || got.Summary.Warnings != 1 {
		t.Fatalf("summary did not round-trip: %+v", got.Summary)
	}
	if got.Packages[0].Name != "@scope/pkg, \"quoted\"" || got.Packages[0].Imports != 4 {
		t.Fatalf("package stats did not round-trip: %+v", got.Packages[0])
	}
	if len(got.Packages[0].Sites) != 3 || got.Packages[0].Sites[1].File != "src/a.ts" {
		t.Fatalf("import sites did not round-trip: %+v", got.Packages[0].Sites)
	}
	if !reflect.DeepEqual(got.Cycles, [][]string{{"src/a.ts", "src/b.ts"}}) {
		t.Fatalf("cycles did not round-trip: %+v", got.Cycles)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Spec != "missing-pkg" {
		t.Fatalf("warnings did not round-trip: %+v", got.Warnings)
	}
}

func TestWriteCSVPackagesAndQuoting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.csv")
	if err := WriteCSV(reportFixture(), path); err != nil {
		t.Fatalf("WriteCSV() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("csv parse error = %v\n%s", err, data)
	}
	wantHeader := []string{"package", "imports", "files", "depType", "version", "topSymbols", "importingFiles"}
	if !reflect.DeepEqual(rows[0], wantHeader) {
		t.Fatalf("header = %v, want %v", rows[0], wantHeader)
	}
	if got, want := len(rows), 3; got != want {
		t.Fatalf("row count = %d, want %d rows including header", got, want)
	}

	first := rows[1]
	if first[0] != "'@scope/pkg, \"quoted\"" || first[1] != "4" || first[2] != "2" || first[3] != "dependency" || first[4] != "1.2.3" {
		t.Fatalf("first package row = %v", first)
	}
	if got, want := first[5], "Alpha(6) Beta(5) Gamma(4) Delta(3) Epsilon(2)"; got != want {
		t.Fatalf("topSymbols = %q, want %q", got, want)
	}
	if strings.Contains(first[5], "Zeta") {
		t.Fatalf("topSymbols was not capped at five entries: %q", first[5])
	}
	if got, want := first[6], "src/a.ts src/b.ts"; got != want {
		t.Fatalf("importingFiles = %q, want deduplicated sorted %q", got, want)
	}
}

func TestWriteCSVNeutralizesSpreadsheetFormulas(t *testing.T) {
	rep := &analyzer.Report{
		Packages: []analyzer.PackageStat{
			{
				Name:    "=1+1",
				Imports: 1,
				Files:   1,
				Version: "-2.0.0",
				DepType: "dependency",
				Symbols: []analyzer.SymbolUse{{Name: "+symbol", Count: 1}},
				Sites: []analyzer.ImportSite{
					{File: "@evil/path.ts", Line: 1, Kind: "import", Spec: "=1+1"},
				},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "formulas.csv")
	if err := WriteCSV(rep, path); err != nil {
		t.Fatalf("WriteCSV() error = %v", err)
	}

	rows, err := csv.NewReader(bytes.NewReader(mustReadFile(t, path))).ReadAll()
	if err != nil {
		t.Fatalf("csv parse error = %v", err)
	}
	row := rows[1]
	for col, want := range map[int]string{
		0: "'=1+1",
		4: "'-2.0.0",
		5: "'+symbol(1)",
		6: "'@evil/path.ts",
	} {
		if row[col] != want {
			t.Fatalf("column %d = %q, want %q in row %v", col, row[col], want, row)
		}
	}
}

func TestWritersHandleEmptyReport(t *testing.T) {
	dir := t.TempDir()
	empty := &analyzer.Report{}

	jsonPath := filepath.Join(dir, "empty.json")
	if err := WriteJSON(empty, jsonPath); err != nil {
		t.Fatalf("WriteJSON(empty) error = %v", err)
	}
	var got analyzer.Report
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("ReadFile(json) error = %v", err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(empty json) error = %v", err)
	}
	if len(got.Packages) != 0 {
		t.Fatalf("empty JSON packages = %d, want 0", len(got.Packages))
	}

	csvPath := filepath.Join(dir, "empty.csv")
	if err := WriteCSV(empty, csvPath); err != nil {
		t.Fatalf("WriteCSV(empty) error = %v", err)
	}
	rows, err := csv.NewReader(bytes.NewReader(mustReadFile(t, csvPath))).ReadAll()
	if err != nil {
		t.Fatalf("csv parse error = %v", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("empty CSV rows = %d, want header only", got)
	}
}

func TestWritersReturnErrorsForUnwritablePath(t *testing.T) {
	base := t.TempDir()
	missingJSON := filepath.Join(base, "missing", "report.json")
	missingCSV := filepath.Join(base, "missing", "report.csv")

	if err := WriteJSON(reportFixture(), missingJSON); err == nil {
		t.Fatal("WriteJSON() error = nil, want error for path in non-existent directory")
	}
	if err := WriteCSV(reportFixture(), missingCSV); err == nil {
		t.Fatal("WriteCSV() error = nil, want error for path in non-existent directory")
	}
}

func TestWritersReturnFinalFlushAndCloseErrors(t *testing.T) {
	flushErr := errors.New("flush failed")
	if err := writeCSV(reportFixture(), failWriter{err: flushErr}); !errors.Is(err, flushErr) {
		t.Fatalf("writeCSV() error = %v, want flush error", err)
	}

	closeErr := errors.New("close failed")
	if err := writeJSON(reportFixture(), &closeFailWriter{err: closeErr}); !errors.Is(err, closeErr) {
		t.Fatalf("writeJSON() error = %v, want close error", err)
	}
}

func TestPrintSummary(t *testing.T) {
	rep := reportFixture()

	var buf bytes.Buffer
	PrintSummary(&buf, rep, 0)
	out := buf.String()
	for _, want := range []string{
		"Entry            src/main.tsx",
		"Project          fixture-app",
		"Files analysed   7 (of 9 under src)",
		"Imports          12 total",
		"Packages         2 unique",
		"Orphans          1   Cycles 1   Warnings 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("PrintSummary(top=0) missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Top packages by imports") {
		t.Fatalf("PrintSummary(top=0) printed package list:\n%s", out)
	}

	buf.Reset()
	PrintSummary(&buf, rep, 99)
	out = buf.String()
	if !strings.Contains(out, "Top packages by imports") ||
		!strings.Contains(out, "@scope/pkg, \"quoted\"") ||
		!strings.Contains(out, "react") {
		t.Fatalf("PrintSummary(top>package count) did not print all packages:\n%s", out)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return data
}

type failWriter struct {
	err error
}

func (w failWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (w failWriter) Close() error {
	return nil
}

type closeFailWriter struct {
	bytes.Buffer
	err error
}

func (w closeFailWriter) Close() error {
	return w.err
}
