package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func TestMarkdownFullReportRendersEverySection(t *testing.T) {
	out := Markdown(mdFullReport(), 5)

	for _, want := range []string{
		"# Import stats: fixture-app — src/main.tsx",
		"Analysed 7 files · 12 imports · 2 packages · generated 2026-09-21T08:00:00Z",
		"## Summary",
		"| Total imports | 12 |",
		"<summary>Top packages (2)</summary>",
		"| 1 | react | 5 | 3 | 18.2.0 | dependency | 1.5 KB |",
		"## Health",
		"### Undeclared dependencies (1)",
		"- missing-pkg",
		"### Unused dependencies (1)",
		"- left-pad",
		"<summary>Cycles (1)</summary>",
		"- src/a.ts → src/b.ts",
		"- Orphan files: 1",
		"## Insights",
		"- **warn** react: Prefer direct imports",
		"<summary>Unused exports (1)</summary>",
		"| src/unused.ts | 9 | stale | function |",
		"<summary>Rule violations (1)</summary>",
		"| layers | src/ui.ts | src/data.ts | 12 |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown() missing %q in:\n%s", want, out)
		}
	}
}

func TestMarkdownEmptyReportIsSensible(t *testing.T) {
	out := Markdown(&analyzer.Report{}, 10)

	for _, want := range []string{
		"# Import stats: unknown project — unknown entry",
		"Analysed 0 files · 0 imports · 0 packages · generated unknown time",
		"## Summary",
		"| Total imports | 0 |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown(empty) missing %q in:\n%s", want, out)
		}
	}
	for _, notWant := range []string{
		"Top packages",
		"## Health",
		"## Insights",
		"<summary>Unused exports",
		"<summary>Rule violations",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("Markdown(empty) unexpectedly contains %q in:\n%s", notWant, out)
		}
	}
}

func TestMarkdownNilReportDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Markdown(nil) panicked: %v", r)
		}
	}()
	out := Markdown(nil, 10)
	if !strings.Contains(out, "unknown project") {
		t.Fatalf("Markdown(nil) =\n%s", out)
	}
}

func TestMarkdownEscapesHostileTableCells(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{ProjectName: "fixture", Entry: "src/main.tsx", UniquePackages: 1},
		Packages: []analyzer.PackageStat{
			{
				Name:      "pkg|under_score*`[x]",
				Imports:   1,
				Files:     1,
				Version:   "1|2_3*`",
				DepType:   "dependency",
				SizeBytes: 42,
			},
		},
		UnusedExports: []analyzer.ExportStat{
			{File: "src/a|b_c*`.ts", Line: 2, Name: "n|m", Kind: "const"},
		},
	}
	out := Markdown(rep, 10)

	for _, want := range []string{
		"pkg\\|under\\_score\\*\\`\\[x\\]",
		"1\\|2\\_3\\*\\`",
		"src/a\\|b\\_c\\*\\`.ts",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown() missing escaped cell %q in:\n%s", want, out)
		}
	}

	for _, rowNeedle := range []string{"pkg\\|under", "src/a\\|b"} {
		row := mdLineContaining(out, rowNeedle)
		if got, want := mdUnescapedPipes(row), 8; rowNeedle == "src/a\\|b" {
			if got != 5 {
				t.Fatalf("unused export row has %d unescaped pipes, want 5: %q", got, row)
			}
		} else if got != want {
			t.Fatalf("package row has %d unescaped pipes, want %d: %q", got, want, row)
		}
	}
}

func TestMarkdownTopTruncation(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{ProjectName: "fixture", Entry: "src/main.tsx", UniquePackages: 3},
		Packages: []analyzer.PackageStat{
			{Name: "a", Imports: 3},
			{Name: "b", Imports: 2},
			{Name: "c", Imports: 1},
		},
		UnusedExports: []analyzer.ExportStat{
			{File: "one.ts", Name: "one"},
			{File: "two.ts", Name: "two"},
			{File: "three.ts", Name: "three"},
		},
	}
	out := Markdown(rep, 1)

	for _, want := range []string{
		"| 1 | a | 3 | 0 |  |  | 0 B |",
		"| one.ts | 0 | one |  |",
		"…and 2 more.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown(top=1) missing %q in:\n%s", want, out)
		}
	}
	for _, notWant := range []string{"| 2 | b |", "| two.ts |"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("Markdown(top=1) unexpectedly contains %q in:\n%s", notWant, out)
		}
	}
	if got := strings.Count(out, "…and 2 more."); got != 2 {
		t.Fatalf("truncation line count = %d, want 2 in:\n%s", got, out)
	}
}

func TestMarkdownOmitsEmptySections(t *testing.T) {
	rep := &analyzer.Report{
		Summary:  analyzer.Summary{ProjectName: "fixture", Entry: "src/main.tsx", UniquePackages: 1},
		Packages: []analyzer.PackageStat{{Name: "react", Imports: 1}},
	}
	out := Markdown(rep, 10)

	for _, notWant := range []string{
		"## Health",
		"## Insights",
		"<summary>Unused exports",
		"<summary>Rule violations",
		"Undeclared dependencies (0)",
		"Unused dependencies (0)",
		"Cycles (0)",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("Markdown() unexpectedly contains %q in:\n%s", notWant, out)
		}
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1536 * 1024, "1.5 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1536 * 1024 * 1024, "1.5 GB"},
	}
	for _, tt := range tests {
		if got := humanSize(tt.bytes); got != tt.want {
			t.Fatalf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestMarkdownDeterministic(t *testing.T) {
	rep := mdFullReport()
	first := Markdown(rep, 5)
	second := Markdown(rep, 5)
	if first != second {
		t.Fatalf("Markdown() was not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestWriteMarkdownRoundTripAndError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	rep := mdFullReport()
	want := Markdown(rep, 5)
	if err := WriteMarkdown(rep, path, 5); err != nil {
		t.Fatalf("WriteMarkdown() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != want {
		t.Fatalf("WriteMarkdown() wrote:\n%s\nwant:\n%s", data, want)
	}

	missing := filepath.Join(t.TempDir(), "missing", "report.md")
	if err := WriteMarkdown(rep, missing, 5); err == nil {
		t.Fatal("WriteMarkdown() error = nil, want error for path in non-existent directory")
	}
}

func mdFullReport() *analyzer.Report {
	return &analyzer.Report{
		Summary: analyzer.Summary{
			Entry:          "src/main.tsx",
			ProjectName:    "fixture-app",
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
			Insights:       1,
			UnusedExports:  1,
			RuleViolations: 1,
			GeneratedAt:    "2026-09-21T08:00:00Z",
		},
		Packages: []analyzer.PackageStat{
			{Name: "react", Imports: 5, Files: 3, Version: "18.2.0", DepType: "dependency", SizeBytes: 1536},
			{Name: "lodash", Imports: 3, Files: 2, Version: "4.17.21", DepType: "devDependency", SizeBytes: 1024 * 1024},
		},
		Orphans:        []string{"src/orphan.ts"},
		Cycles:         [][]string{{"src/a.ts", "src/b.ts"}},
		UnusedDeps:     []string{"left-pad"},
		UndeclaredDeps: []string{"missing-pkg"},
		Insights: []analyzer.Insight{
			{ID: "deep-import", Severity: "warn", Subject: "react", Message: "Prefer direct imports"},
		},
		UnusedExports: []analyzer.ExportStat{
			{File: "src/unused.ts", Line: 9, Name: "stale", Kind: "function"},
		},
		RuleViolations: []analyzer.RuleViolation{
			{Rule: "layers", From: "src/ui.ts", To: "src/data.ts", Line: 12},
		},
	}
}

func mdLineContaining(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func mdUnescapedPipes(s string) int {
	count := 0
	for i, r := range s {
		if r != '|' || mdEscapedAt(s, i) {
			continue
		}
		count++
	}
	return count
}

func mdEscapedAt(s string, idx int) bool {
	slashes := 0
	for i := idx - 1; i >= 0 && s[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 == 1
}
