package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func TestWriteAnnotationsPopulatedReportRendersEveryFindingCategory(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAnnotations(&buf, ciReportFixture()); err != nil {
		t.Fatalf("WriteAnnotations() error = %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"::error file=src/components/Button.tsx,line=12::src/components/Button.tsx must not import src/pages/Home.tsx (rule: src/components !-> src/pages)",
		"::warning file=src/cycle-b.ts::Circular dependency: src/cycle-b.ts → src/cycle-a.ts → src/cycle-b.ts",
		"::warning file=src/a.ts,line=3::Imported package \"missing-pkg\" is not declared in package.json",
		"::warning::Declared dependency \"left-pad\" is not imported by reachable files",
		"::notice file=src/orphan.ts::Orphan file is not reachable from the entry point: src/orphan.ts",
		"::notice file=src/deep.ts::src/deep.ts: Prefer a public module boundary",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("annotations missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/Users/") {
		t.Fatalf("annotations leaked an absolute path:\n%s", out)
	}
}

func TestWriteAnnotationsEmptyAndNilReports(t *testing.T) {
	for _, tc := range []struct {
		name string
		rep  *analyzer.Report
	}{
		{name: "empty", rep: &analyzer.Report{}},
		{name: "nil", rep: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteAnnotations(&buf, tc.rep); err != nil {
				t.Fatalf("WriteAnnotations() error = %v", err)
			}
			if buf.Len() != 0 {
				t.Fatalf("WriteAnnotations() wrote %q, want empty output", buf.String())
			}
		})
	}
}

func TestWriteAnnotationsEscapesWorkflowCommands(t *testing.T) {
	rep := &analyzer.Report{
		RuleViolations: []analyzer.RuleViolation{
			{
				Rule: "src/a,b !-> src:pages%\nnext",
				From: "src/components/Button,primary:blue.tsx",
				To:   "src/pages/Home.tsx",
				Line: 4,
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteAnnotations(&buf, rep); err != nil {
		t.Fatalf("WriteAnnotations() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"file=src/components/Button%2Cprimary%3Ablue.tsx",
		"rule: src/a,b !-> src:pages%25%0Anext",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("escaped annotations missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "file=src/components/Button,primary:blue.tsx") ||
		strings.Contains(out, "src:pages%\nnext") {
		t.Fatalf("annotation contains unescaped workflow-command data:\n%s", out)
	}
}

func TestWriteAnnotationsDeterministic(t *testing.T) {
	var first bytes.Buffer
	if err := WriteAnnotations(&first, ciReportFixture()); err != nil {
		t.Fatalf("WriteAnnotations() first error = %v", err)
	}
	var second bytes.Buffer
	if err := WriteAnnotations(&second, ciReportFixture()); err != nil {
		t.Fatalf("WriteAnnotations() second error = %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("WriteAnnotations() was not deterministic:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

func TestWriteAnnotationsSurfacesWriteErrors(t *testing.T) {
	writeErr := errors.New("write failed")
	if err := WriteAnnotations(failWriter{err: writeErr}, ciReportFixture()); !errors.Is(err, writeErr) {
		t.Fatalf("WriteAnnotations() error = %v, want write error", err)
	}
}
