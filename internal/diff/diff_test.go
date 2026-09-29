package diff

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func TestComputePackages(t *testing.T) {
	baseline := &analyzer.Report{
		Summary: analyzer.Summary{GeneratedAt: "2026-09-20T08:00:00Z"},
		Packages: []analyzer.PackageStat{
			{Name: "gone", Imports: 2, Version: "1.0.0", SizeBytes: 50},
			{Name: "same", Imports: 1, Version: "1.0.0", SizeBytes: 10},
			{Name: "size-only", Imports: 4, Version: "1.0.0", SizeBytes: 100},
			{Name: "count", Imports: 3, Version: "1.0.0", SizeBytes: 30},
			{Name: "version-only", Imports: 5, Version: "1.0.0", SizeBytes: 90},
		},
	}
	current := &analyzer.Report{
		Summary: analyzer.Summary{GeneratedAt: "2026-09-21T08:00:00Z"},
		Packages: []analyzer.PackageStat{
			{Name: "added", Imports: 7, Version: "2.0.0", SizeBytes: 70},
			{Name: "same", Imports: 1, Version: "1.0.0", SizeBytes: 10},
			{Name: "size-only", Imports: 4, Version: "1.0.0", SizeBytes: 125},
			{Name: "count", Imports: 6, Version: "1.0.0", SizeBytes: 30},
			{Name: "version-only", Imports: 5, Version: "1.1.0", SizeBytes: 90},
		},
	}

	got := Compute(baseline, current)
	wantAdded := []PackageChange{{Name: "added", After: 7, DeltaImports: 7, AfterVersion: "2.0.0", DeltaSizeBytes: 70}}
	wantRemoved := []PackageChange{{Name: "gone", Before: 2, DeltaImports: -2, BeforeVersion: "1.0.0", DeltaSizeBytes: -50}}
	wantChanged := []PackageChange{
		{Name: "count", Before: 3, After: 6, DeltaImports: 3, BeforeVersion: "1.0.0", AfterVersion: "1.0.0"},
		{Name: "size-only", Before: 4, After: 4, BeforeVersion: "1.0.0", AfterVersion: "1.0.0", DeltaSizeBytes: 25},
		{Name: "version-only", Before: 5, After: 5, BeforeVersion: "1.0.0", AfterVersion: "1.1.0"},
	}
	if !reflect.DeepEqual(got.AddedPackages, wantAdded) {
		t.Fatalf("AddedPackages = %+v, want %+v", got.AddedPackages, wantAdded)
	}
	if !reflect.DeepEqual(got.RemovedPackages, wantRemoved) {
		t.Fatalf("RemovedPackages = %+v, want %+v", got.RemovedPackages, wantRemoved)
	}
	if !reflect.DeepEqual(got.ChangedPackages, wantChanged) {
		t.Fatalf("ChangedPackages = %+v, want %+v", got.ChangedPackages, wantChanged)
	}
}

func TestComputeFindingsAndSummaryDeltas(t *testing.T) {
	baseline := &analyzer.Report{
		Summary: analyzer.Summary{
			FilesAnalyzed: 10, TotalImports: 20, PackageImports: 12, LocalImports: 8,
			UniquePackages: 4, OrphanFiles: 2, Cycles: 1, UndeclaredDeps: 1,
			UnusedDeps: 3, Warnings: 1, UnusedExports: 5, RuleViolations: 1,
		},
		Cycles:         [][]string{{"b.ts", "a.ts"}, {"old.ts", "z.ts"}},
		UndeclaredDeps: []string{"old-missing"},
		UnusedDeps:     []string{"old-unused"},
		Orphans:        []string{"gone.ts", "same.ts"},
		RuleViolations: []analyzer.RuleViolation{{Rule: "layers", From: "src/a.ts", To: "src/b.ts", Line: 4}},
	}
	current := &analyzer.Report{
		Summary: analyzer.Summary{
			FilesAnalyzed: 12, TotalImports: 18, PackageImports: 13, LocalImports: 5,
			UniquePackages: 6, OrphanFiles: 2, Cycles: 2, UndeclaredDeps: 2,
			UnusedDeps: 4, Warnings: 0, UnusedExports: 7, RuleViolations: 3,
		},
		Cycles:         [][]string{{"a.ts", "b.ts"}, {"c.ts", "d.ts"}},
		UndeclaredDeps: []string{"new-missing", "old-missing"},
		UnusedDeps:     []string{"old-unused", "new-unused"},
		Orphans:        []string{"new.ts", "same.ts"},
		RuleViolations: []analyzer.RuleViolation{
			{Rule: "layers", From: "src/a.ts", To: "src/b.ts", Line: 4},
			{Rule: "layers", From: "src/a.ts", To: "src/c.ts", Line: 9},
			{Rule: "imports", From: "src/z.ts", To: "src/y.ts", Line: 1},
		},
	}

	got := Compute(baseline, current)
	if !reflect.DeepEqual(got.NewCycles, [][]string{{"c.ts", "d.ts"}}) {
		t.Fatalf("NewCycles = %#v", got.NewCycles)
	}
	if !reflect.DeepEqual(got.ResolvedCycles, [][]string{{"old.ts", "z.ts"}}) {
		t.Fatalf("ResolvedCycles = %#v", got.ResolvedCycles)
	}
	if !reflect.DeepEqual(got.NewUndeclared, []string{"new-missing"}) {
		t.Fatalf("NewUndeclared = %#v", got.NewUndeclared)
	}
	if !reflect.DeepEqual(got.NewUnusedDeps, []string{"new-unused"}) {
		t.Fatalf("NewUnusedDeps = %#v", got.NewUnusedDeps)
	}
	if !reflect.DeepEqual(got.NewOrphans, []string{"new.ts"}) || !reflect.DeepEqual(got.ResolvedOrphans, []string{"gone.ts"}) {
		t.Fatalf("orphans new=%#v resolved=%#v", got.NewOrphans, got.ResolvedOrphans)
	}
	wantViolations := []analyzer.RuleViolation{
		{Rule: "layers", From: "src/a.ts", To: "src/c.ts", Line: 9},
		{Rule: "imports", From: "src/z.ts", To: "src/y.ts", Line: 1},
	}
	if !reflect.DeepEqual(got.NewViolations, wantViolations) {
		t.Fatalf("NewViolations = %#v, want %#v", got.NewViolations, wantViolations)
	}
	wantDeltas := map[string]int{
		"filesAnalyzed":          2,
		"totalImports":           -2,
		"packageImports":         1,
		"localImports":           -3,
		"uniquePackages":         2,
		"cycles":                 1,
		"undeclaredDependencies": 1,
		"unusedDependencies":     1,
		"warnings":               -1,
		"unusedExports":          2,
		"ruleViolations":         2,
	}
	if !reflect.DeepEqual(got.SummaryDeltas, wantDeltas) {
		t.Fatalf("SummaryDeltas = %#v, want %#v", got.SummaryDeltas, wantDeltas)
	}
	if _, ok := got.SummaryDeltas["orphanFiles"]; ok {
		t.Fatalf("zero orphanFiles delta should be omitted: %#v", got.SummaryDeltas)
	}
}

func TestIdenticalReportsAreEmptyAndWriteTextSaysSo(t *testing.T) {
	rep := sampleReport()
	got := Compute(rep, cloneReport(rep))
	if !diffEmpty(got) {
		t.Fatalf("diff is not empty: %#v", got)
	}
	var buf bytes.Buffer
	WriteText(&buf, got)
	if !strings.Contains(buf.String(), "No changes.") {
		t.Fatalf("WriteText did not report no changes:\n%s", buf.String())
	}
}

func TestNilBaselineTreatsEverythingAsAdded(t *testing.T) {
	current := sampleReport()
	got := Compute(nil, current)
	if got.BaselineGeneratedAt != "" || got.CurrentGeneratedAt != current.Summary.GeneratedAt {
		t.Fatalf("timestamps = %q/%q", got.BaselineGeneratedAt, got.CurrentGeneratedAt)
	}
	if !reflect.DeepEqual(got.AddedPackages, []PackageChange{{Name: "react", After: 3, DeltaImports: 3, AfterVersion: "18.2.0", DeltaSizeBytes: 1000}}) {
		t.Fatalf("AddedPackages = %#v", got.AddedPackages)
	}
	if !reflect.DeepEqual(got.NewCycles, [][]string{{"src/a.ts", "src/b.ts"}}) {
		t.Fatalf("NewCycles = %#v", got.NewCycles)
	}
	if !reflect.DeepEqual(got.NewOrphans, []string{"src/orphan.ts"}) {
		t.Fatalf("NewOrphans = %#v", got.NewOrphans)
	}
	if got.SummaryDeltas["totalImports"] != current.Summary.TotalImports || got.SummaryDeltas["uniquePackages"] != current.Summary.UniquePackages {
		t.Fatalf("SummaryDeltas = %#v", got.SummaryDeltas)
	}
}

func TestCheckGates(t *testing.T) {
	firing := &Diff{
		NewCycles:     [][]string{{"a", "b"}},
		NewUndeclared: []string{"missing"},
		NewOrphans:    []string{"orphan"},
		NewViolations: []analyzer.RuleViolation{{From: "a", To: "b", Line: 1}},
		SummaryDeltas: map[string]int{"totalImports": 1, "uniquePackages": 1},
	}
	all := []string{"new-cycles", "new-undeclared", "new-orphans", "rules", "imports-up", "packages-up"}
	failed, err := Check(firing, all)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !reflect.DeepEqual(failed, all) {
		t.Fatalf("failed = %#v, want %#v", failed, all)
	}

	notFiring := &Diff{SummaryDeltas: map[string]int{"totalImports": -1, "uniquePackages": 0}}
	failed, err = Check(notFiring, all)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("failed = %#v, want none", failed)
	}

	_, err = Check(firing, []string{"new-cycles", "typo"})
	if err == nil || !strings.Contains(err.Error(), "typo") || !strings.Contains(err.Error(), "new-cycles") || !strings.Contains(err.Error(), "packages-up") {
		t.Fatalf("unknown gate error = %v", err)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	path := filepath.Join(".", "load_roundtrip_test_report.json")
	t.Cleanup(func() { _ = os.Remove(path) })
	rep := sampleReport()
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, rep) {
		t.Fatalf("Load() = %#v, want %#v", got, rep)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load("missing_report.json"); err == nil {
		t.Fatal("Load(missing) error = nil")
	}

	path := filepath.Join(".", "malformed_report_test.json")
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := os.WriteFile(path, []byte(`{"summary":`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load(malformed) error = nil")
	}
}

func TestComputeDeterministic(t *testing.T) {
	baseline := sampleReport()
	current := cloneReport(baseline)
	current.Packages = append(current.Packages, analyzer.PackageStat{Name: "@scope/new", Imports: 1})
	current.Orphans = append(current.Orphans, "src/another.ts")
	current.Cycles = append(current.Cycles, []string{"src/d.ts", "src/c.ts"})
	current.RuleViolations = append(current.RuleViolations, analyzer.RuleViolation{Rule: "z", From: "src/z.ts", To: "src/a.ts", Line: 3})
	current.Summary.TotalImports++
	current.Summary.UniquePackages++

	first := Compute(baseline, current)
	second := Compute(baseline, current)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Compute not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestWriteTextIncludesSections(t *testing.T) {
	d := &Diff{
		BaselineGeneratedAt: "2026-09-20T08:00:00Z",
		CurrentGeneratedAt:  "2026-09-21T08:00:00Z",
		AddedPackages:       []PackageChange{{Name: "react", After: 2, DeltaImports: 2, AfterVersion: "18.2.0", DeltaSizeBytes: 20}},
		NewCycles:           [][]string{{"a.ts", "b.ts"}},
		NewUndeclared:       []string{"missing"},
		NewViolations:       []analyzer.RuleViolation{{Rule: "layers", From: "a.ts", To: "b.ts", Line: 2}},
		SummaryDeltas:       map[string]int{"totalImports": 2},
	}
	var buf bytes.Buffer
	WriteText(&buf, d)
	out := buf.String()
	for _, want := range []string{"Baseline diff", "Added packages:", "+ react", "New cycles:", "New undeclared dependencies:", "New rule violations:", "Summary deltas:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("WriteText output missing %q:\n%s", want, out)
		}
	}
}

func sampleReport() *analyzer.Report {
	return &analyzer.Report{
		Summary: analyzer.Summary{
			GeneratedAt:    "2026-09-21T08:00:00Z",
			FilesAnalyzed:  2,
			TotalImports:   5,
			PackageImports: 3,
			LocalImports:   2,
			UniquePackages: 1,
			OrphanFiles:    1,
			Cycles:         1,
			UndeclaredDeps: 1,
			UnusedDeps:     1,
			Warnings:       1,
			UnusedExports:  1,
			RuleViolations: 1,
			DurationMillis: 10,
		},
		Packages:       []analyzer.PackageStat{{Name: "react", Imports: 3, Version: "18.2.0", SizeBytes: 1000}},
		Cycles:         [][]string{{"src/b.ts", "src/a.ts"}},
		UndeclaredDeps: []string{"missing-pkg"},
		UnusedDeps:     []string{"unused-pkg"},
		Orphans:        []string{"src/orphan.ts"},
		RuleViolations: []analyzer.RuleViolation{{Rule: "layers", From: "src/a.ts", To: "src/b.ts", Line: 7}},
	}
}

func cloneReport(rep *analyzer.Report) *analyzer.Report {
	data, err := json.Marshal(rep)
	if err != nil {
		panic(err)
	}
	var out analyzer.Report
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return &out
}
