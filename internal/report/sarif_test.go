package report

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func TestSARIFPopulatedReportRendersEveryFindingCategory(t *testing.T) {
	out, err := SARIF(ciReportFixture())
	if err != nil {
		t.Fatalf("SARIF() error = %v", err)
	}

	log := unmarshalSARIF(t, out)
	run := log["runs"].([]any)[0].(map[string]any)
	results := run["results"].([]any)
	if got, want := len(results), 6; got != want {
		t.Fatalf("results len = %d, want %d:\n%s", got, want, out)
	}

	for _, id := range []string{
		sarifRuleViolationID,
		sarifCycleID,
		sarifUndeclaredDepID,
		sarifUnusedDepID,
		sarifOrphanID,
		sarifInsightID,
	} {
		if !sarifHasRule(run, id) {
			t.Fatalf("SARIF rules missing %q in:\n%s", id, out)
		}
		if !sarifHasResult(results, id) {
			t.Fatalf("SARIF results missing %q in:\n%s", id, out)
		}
	}

	if strings.Contains(out, "/Users/") || strings.Contains(out, "/work/app/") {
		t.Fatalf("SARIF leaked an absolute path:\n%s", out)
	}
	if !strings.Contains(out, `"uri": "src/components/Button.tsx"`) {
		t.Fatalf("SARIF did not relativize artifact URIs:\n%s", out)
	}
	if !strings.Contains(out, `"startLine": 12`) {
		t.Fatalf("SARIF did not include known startLine:\n%s", out)
	}
}

func TestSARIFEmptyAndNilReportsHaveEmptyResultsArray(t *testing.T) {
	for _, tc := range []struct {
		name string
		rep  *analyzer.Report
	}{
		{name: "empty", rep: &analyzer.Report{}},
		{name: "nil", rep: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := SARIF(tc.rep)
			if err != nil {
				t.Fatalf("SARIF() error = %v", err)
			}
			if strings.Contains(out, `"results": null`) {
				t.Fatalf("SARIF results are null:\n%s", out)
			}
			if !strings.Contains(out, `"results": []`) {
				t.Fatalf("SARIF results are not an empty array:\n%s", out)
			}
			log := unmarshalSARIF(t, out)
			if got := log["version"]; got != sarifVersion {
				t.Fatalf("version = %v, want %q", got, sarifVersion)
			}
			run := log["runs"].([]any)[0].(map[string]any)
			if got := len(run["results"].([]any)); got != 0 {
				t.Fatalf("results len = %d, want 0", got)
			}
		})
	}
}

func TestSARIFRoundTripHasRequiredKeys(t *testing.T) {
	out, err := SARIF(ciReportFixture())
	if err != nil {
		t.Fatalf("SARIF() error = %v", err)
	}
	log := unmarshalSARIF(t, out)
	for _, key := range []string{"$schema", "version", "runs"} {
		if _, ok := log[key]; !ok {
			t.Fatalf("SARIF missing top-level key %q in %#v", key, log)
		}
	}
	run := log["runs"].([]any)[0].(map[string]any)
	tool := run["tool"].(map[string]any)
	driver := tool["driver"].(map[string]any)
	for _, key := range []string{"name", "informationUri", "rules"} {
		if _, ok := driver[key]; !ok {
			t.Fatalf("SARIF driver missing key %q in %#v", key, driver)
		}
	}
	if _, ok := run["results"]; !ok {
		t.Fatalf("SARIF run missing results in %#v", run)
	}
}

func TestSARIFOmitUnknownStartLine(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{ProjectRoot: "/tmp/test-project"},
		RuleViolations: []analyzer.RuleViolation{
			{Rule: "src/a !-> src/b", From: "/tmp/test-project/src/a.ts", To: "/tmp/test-project/src/b.ts"},
		},
	}
	out, err := SARIF(rep)
	if err != nil {
		t.Fatalf("SARIF() error = %v", err)
	}
	if strings.Contains(out, `"startLine": 0`) || strings.Contains(out, `"region"`) {
		t.Fatalf("SARIF emitted an unknown/zero region:\n%s", out)
	}
}

func TestSARIFDeterministic(t *testing.T) {
	first, err := SARIF(ciReportFixture())
	if err != nil {
		t.Fatalf("SARIF() first error = %v", err)
	}
	second, err := SARIF(ciReportFixture())
	if err != nil {
		t.Fatalf("SARIF() second error = %v", err)
	}
	if first != second {
		t.Fatalf("SARIF() was not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestWriteSARIFWritesFileAndSurfacesErrors(t *testing.T) {
	dir := filepath.Join(".", ".sarif-test-output")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("RemoveAll() error = %v", err)
		}
	})

	path := filepath.Join(dir, "report.sarif")
	want, err := SARIF(ciReportFixture())
	if err != nil {
		t.Fatalf("SARIF() error = %v", err)
	}
	if err := WriteSARIF(ciReportFixture(), path); err != nil {
		t.Fatalf("WriteSARIF() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != want {
		t.Fatalf("WriteSARIF() wrote:\n%s\nwant:\n%s", data, want)
	}

	if err := WriteSARIF(ciReportFixture(), filepath.Join(dir, "missing", "report.sarif")); err == nil {
		t.Fatal("WriteSARIF() error = nil, want create error")
	}

	writeErr := errors.New("write failed")
	if err := writeSARIF(ciReportFixture(), failWriter{err: writeErr}); !errors.Is(err, writeErr) {
		t.Fatalf("writeSARIF() error = %v, want write error", err)
	}

	closeErr := errors.New("close failed")
	if err := writeSARIF(ciReportFixture(), &closeFailWriter{err: closeErr}); !errors.Is(err, closeErr) {
		t.Fatalf("writeSARIF() error = %v, want close error", err)
	}
}

func unmarshalSARIF(t *testing.T, out string) map[string]any {
	t.Helper()
	var log map[string]any
	if err := json.Unmarshal([]byte(out), &log); err != nil {
		t.Fatalf("SARIF did not unmarshal: %v\n%s", err, out)
	}
	return log
}

func sarifHasRule(run map[string]any, id string) bool {
	rules := run["tool"].(map[string]any)["driver"].(map[string]any)["rules"].([]any)
	for _, raw := range rules {
		if raw.(map[string]any)["id"] == id {
			return true
		}
	}
	return false
}

func sarifHasResult(results []any, id string) bool {
	for _, raw := range results {
		if raw.(map[string]any)["ruleId"] == id {
			return true
		}
	}
	return false
}

func ciReportFixture() *analyzer.Report {
	return &analyzer.Report{
		Summary: analyzer.Summary{
			ProjectRoot: "/tmp/test-project",
			SrcRoot:     "/tmp/test-project/src",
		},
		Packages: []analyzer.PackageStat{
			{
				Name:    "missing-pkg",
				DepType: "undeclared",
				Sites: []analyzer.ImportSite{
					{File: "/tmp/test-project/src/z.ts", Line: 7, Kind: "named", Spec: "missing-pkg"},
					{File: "/tmp/test-project/src/a.ts", Line: 3, Kind: "default", Spec: "missing-pkg"},
				},
			},
		},
		Orphans:        []string{"/tmp/test-project/src/orphan.ts"},
		Cycles:         [][]string{{"/tmp/test-project/src/cycle-b.ts", "/tmp/test-project/src/cycle-a.ts"}},
		UnusedDeps:     []string{"left-pad"},
		UndeclaredDeps: []string{"missing-pkg"},
		Insights: []analyzer.Insight{
			{ID: "deep-import", Severity: "warn", Subject: "src/deep.ts", Message: "Prefer a public module boundary"},
		},
		RuleViolations: []analyzer.RuleViolation{
			{Rule: "src/components !-> src/pages", From: "/tmp/test-project/src/components/Button.tsx", To: "/tmp/test-project/src/pages/Home.tsx", Line: 12},
		},
	}
}
