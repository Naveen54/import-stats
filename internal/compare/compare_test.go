package compare

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func rep(name string, files, imports int, pkgs ...analyzer.PackageStat) *analyzer.Report {
	return &analyzer.Report{
		Summary: analyzer.Summary{
			Entry: "src/index.js", ProjectName: name,
			FilesAnalyzed: files, TotalImports: imports, UniquePackages: len(pkgs),
		},
		Packages: pkgs,
	}
}

func p(name, version string, imports, files int) analyzer.PackageStat {
	return analyzer.PackageStat{Name: name, Version: version, Imports: imports, Files: files, DepType: "dependency"}
}

func row(r *Report, pkg string) *Row {
	for i := range r.Rows {
		if r.Rows[i].Package == pkg {
			return &r.Rows[i]
		}
	}
	return nil
}

func TestBuildDetectsVersionDrift(t *testing.T) {
	got := Build([]Input{
		{Name: "a", Report: rep("a", 10, 20, p("react", "^16.0.0", 5, 5), p("lodash", "^4.17.0", 2, 2))},
		{Name: "b", Report: rep("b", 8, 15, p("react", "^18.2.0", 3, 3), p("lodash", "^4.17.0", 1, 1))},
	})

	react := row(got, "react")
	if react == nil {
		t.Fatal("react row missing")
	}
	if !react.Drift {
		t.Error("react versions differ and must be flagged as drift")
	}
	if !reflect.DeepEqual(react.Versions, []string{"^16.0.0", "^18.2.0"}) {
		t.Errorf("react versions = %v", react.Versions)
	}

	lodash := row(got, "lodash")
	if lodash.Drift {
		t.Error("lodash versions agree and must not be drift")
	}
	if got.DriftCount != 1 {
		t.Errorf("driftCount = %d, want 1", got.DriftCount)
	}
	// Drift rows must sort first so the actionable ones lead.
	if got.Rows[0].Package != "react" {
		t.Errorf("first row = %s, want react (drift sorts first)", got.Rows[0].Package)
	}
}

func TestBuildCountsSharedAndUnique(t *testing.T) {
	got := Build([]Input{
		{Name: "a", Report: rep("a", 1, 1, p("react", "^18.0.0", 1, 1), p("only-a", "^1.0.0", 1, 1))},
		{Name: "b", Report: rep("b", 1, 1, p("react", "^18.0.0", 1, 1), p("only-b", "^1.0.0", 1, 1))},
		{Name: "c", Report: rep("c", 1, 1, p("react", "^18.0.0", 1, 1))},
	})

	if got.SharedCount != 1 {
		t.Errorf("sharedCount = %d, want 1 (react)", got.SharedCount)
	}
	if got.UniqueCount != 2 {
		t.Errorf("uniqueCount = %d, want 2 (only-a, only-b)", got.UniqueCount)
	}
	if r := row(got, "react"); r.Used != 3 {
		t.Errorf("react used = %d, want 3", r.Used)
	}
	if r := row(got, "only-a"); !r.Cells["a"].Present || r.Cells["b"].Present {
		t.Error("only-a must be present in a and absent from b")
	}
}

// A broken project must not hide the rest of the comparison.
func TestBuildKeepsGoingWhenAProjectFails(t *testing.T) {
	got := Build([]Input{
		{Name: "good", Report: rep("good", 5, 5, p("react", "^18.0.0", 1, 1))},
		{Name: "broken", Root: "/nope", Err: errors.New("entry not found")},
	})

	if len(got.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(got.Projects))
	}
	var broken *Project
	for i := range got.Projects {
		if got.Projects[i].Name == "broken" {
			broken = &got.Projects[i]
		}
	}
	if broken == nil || !strings.Contains(broken.Error, "entry not found") {
		t.Fatalf("broken project error not surfaced: %+v", broken)
	}
	// The failed project must not count towards "shared by all".
	if got.SharedCount != 1 {
		t.Errorf("sharedCount = %d, want 1; a failed project must not count", got.SharedCount)
	}
	if r := row(got, "react"); r.Used != 1 {
		t.Errorf("react used = %d, want 1", r.Used)
	}
}

func TestBuildIgnoresBuiltinsAndUndeclaredVersions(t *testing.T) {
	builtin := analyzer.PackageStat{Name: "fs", DepType: "builtin", Imports: 3}
	undeclared := analyzer.PackageStat{Name: "mystery", DepType: "undeclared", Imports: 1}

	got := Build([]Input{
		{Name: "a", Report: rep("a", 1, 1, builtin, undeclared)},
		{Name: "b", Report: rep("b", 1, 1, builtin, undeclared)},
	})

	if row(got, "fs") != nil {
		t.Error("builtins must be excluded from the comparison")
	}
	m := row(got, "mystery")
	if m == nil {
		t.Fatal("undeclared package should still appear")
	}
	if m.Drift {
		t.Error("a package with no declared version cannot be drifting")
	}
}

func TestBuildDeterministic(t *testing.T) {
	inputs := []Input{
		{Name: "b", Report: rep("b", 1, 1, p("react", "^18.0.0", 1, 1), p("z", "^1.0.0", 4, 1), p("a", "^1.0.0", 4, 1))},
		{Name: "a", Report: rep("a", 1, 1, p("react", "^16.0.0", 1, 1), p("m", "^1.0.0", 4, 1))},
	}
	first := Build(inputs)
	second := Build(inputs)

	if len(first.Rows) != len(second.Rows) {
		t.Fatal("row counts differ")
	}
	for i := range first.Rows {
		if first.Rows[i].Package != second.Rows[i].Package {
			t.Fatalf("nondeterministic order at %d: %s vs %s", i, first.Rows[i].Package, second.Rows[i].Package)
		}
	}
	if first.Projects[0].Name != "a" {
		t.Errorf("projects must be sorted, got %s first", first.Projects[0].Name)
	}
}

func TestBuildEmpty(t *testing.T) {
	got := Build(nil)
	if got == nil || len(got.Rows) != 0 || len(got.Projects) != 0 {
		t.Fatalf("empty build should be inert, got %+v", got)
	}
	if got.SharedCount != 0 {
		t.Errorf("sharedCount = %d, want 0", got.SharedCount)
	}
}

func TestWriteText(t *testing.T) {
	r := Build([]Input{
		{Name: "a", Report: rep("a", 10, 20, p("react", "^16.0.0", 5, 5))},
		{Name: "b", Report: rep("b", 8, 15, p("react", "^18.2.0", 3, 3))},
	})

	var buf bytes.Buffer
	WriteText(&buf, r, 15)
	out := buf.String()

	for _, want := range []string{"Comparing 2 projects", "Version drift", "react", "^16.0.0", "^18.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestWriteTextEmpty(t *testing.T) {
	var buf bytes.Buffer
	WriteText(&buf, Build(nil), 15)
	if !strings.Contains(buf.String(), "Nothing to compare") {
		t.Errorf("unexpected output: %q", buf.String())
	}
	buf.Reset()
	WriteText(&buf, nil, 15) // must not panic
}

func TestWriteTextTruncates(t *testing.T) {
	var a, b []analyzer.PackageStat
	for i := 0; i < 10; i++ {
		name := string(rune('a'+i)) + "-pkg"
		a = append(a, p(name, "^1.0.0", 1, 1))
		b = append(b, p(name, "^2.0.0", 1, 1))
	}
	r := Build([]Input{
		{Name: "a", Report: rep("a", 1, 1, a...)},
		{Name: "b", Report: rep("b", 1, 1, b...)},
	})

	var buf bytes.Buffer
	WriteText(&buf, r, 3)
	if !strings.Contains(buf.String(), "and 7 more") {
		t.Errorf("expected truncation notice, got:\n%s", buf.String())
	}
}
