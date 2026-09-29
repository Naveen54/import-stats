package viz

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func TestSmallGraphEdgesHaveAnalyzerDirection(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{Entry: "src/index.js"},
		InternalFiles: []analyzer.FileStat{
			{File: "src/index.js"},
			{File: "src/App.js", FanIn: 1, Sites: []analyzer.ImportSite{{File: "src/index.js"}}},
			{File: "src/components/Button.js", FanIn: 1, Sites: []analyzer.ImportSite{{File: "src/App.js"}}},
		},
	}

	dot := DOT(rep, Options{})
	for _, want := range []string{
		`"src/index.js" -> "src/App.js";`,
		`"src/App.js" -> "src/components/Button.js";`,
		`"src/index.js" [label="src/index.js", shape="box"`,
	} {
		if !strings.Contains(dot, want) {
			t.Fatalf("DOT missing %q:\n%s", want, dot)
		}
	}

	mermaid := Mermaid(rep, Options{})
	if !strings.Contains(mermaid, `n0["src/App.js"]`) ||
		!strings.Contains(mermaid, `n1["src/components/Button.js"]`) ||
		!strings.Contains(mermaid, `n2["src/index.js"]`) {
		t.Fatalf("Mermaid missing expected labelled nodes:\n%s", mermaid)
	}
	for _, want := range []string{"n2 --> n0", "n0 --> n1", "class n2 entry"} {
		if !strings.Contains(mermaid, want) {
			t.Fatalf("Mermaid missing %q:\n%s", want, mermaid)
		}
	}
}

func TestEscapingAndMermaidIDs(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{Entry: `src/app entry.js`},
		InternalFiles: []analyzer.FileStat{
			{File: `src/app entry.js`},
			{File: `src/a"b\c [x]/雪.js`, FanIn: 1, Sites: []analyzer.ImportSite{{File: `src/app entry.js`}}},
			{File: `src/@scope/pkg name.js`, FanIn: 1, Sites: []analyzer.ImportSite{{File: `src/a"b\c [x]/雪.js`}}},
		},
	}

	dot := DOT(rep, Options{})
	if !strings.Contains(dot, `"src/a\"b\\c [x]/雪.js"`) {
		t.Fatalf("DOT did not quote and escape hostile path:\n%s", dot)
	}
	if !strings.Contains(dot, `"src/a\"b\\c [x]/雪.js" -> "src/@scope/pkg name.js";`) {
		t.Fatalf("DOT edge was not correctly escaped:\n%s", dot)
	}

	mermaid := Mermaid(rep, Options{})
	if !strings.Contains(mermaid, `["src/a\"b\\c [x]/雪.js"]`) {
		t.Fatalf("Mermaid did not quote and escape hostile label:\n%s", mermaid)
	}
	idRe := regexp.MustCompile(`^\s*(n[0-9]+)(?:\[| --| -->|$)`)
	for _, line := range strings.Split(mermaid, "\n") {
		match := idRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if ok, _ := regexp.MatchString(`^[A-Za-z0-9_]+$`, match[1]); !ok {
			t.Fatalf("invalid Mermaid ID %q in:\n%s", match[1], mermaid)
		}
	}
}

func TestCollapseMergesCountsAndDropsSelfEdges(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{Entry: "src/app/index.js"},
		InternalFiles: []analyzer.FileStat{
			{File: "src/app/index.js"},
			{File: "src/app/components/Header.js", FanIn: 1, Sites: []analyzer.ImportSite{{File: "src/app/index.js"}}},
			{File: "src/lib/a.js", FanIn: 2, Sites: []analyzer.ImportSite{{File: "src/app/index.js"}, {File: "src/app/components/Header.js"}}},
		},
	}

	dot := DOT(rep, Options{Collapse: true})
	if strings.Contains(dot, `"src/app" -> "src/app"`) {
		t.Fatalf("collapse emitted noisy self-edge:\n%s", dot)
	}
	if !strings.Contains(dot, `"src/app" -> "src/lib" [label="2"];`) {
		t.Fatalf("collapse did not merge and label edge count:\n%s", dot)
	}

	mermaid := Mermaid(rep, Options{Collapse: true})
	if !strings.Contains(mermaid, ` -- "2" --> `) {
		t.Fatalf("Mermaid collapse edge missing count label:\n%s", mermaid)
	}
}

func TestMaxNodesDropsDanglingEdges(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{Entry: "src/entry.js"},
		InternalFiles: []analyzer.FileStat{
			{File: "src/entry.js", FanIn: 0},
			{File: "src/high.js", FanIn: 5, Sites: []analyzer.ImportSite{{File: "src/entry.js"}}},
			{File: "src/mid.js", FanIn: 3, Sites: []analyzer.ImportSite{{File: "src/high.js"}}},
			{File: "src/low.js", FanIn: 1, Sites: []analyzer.ImportSite{{File: "src/mid.js"}}},
		},
	}

	dot := DOT(rep, Options{MaxNodes: 2})
	nodes := emittedDOTNodes(dot)
	edges := emittedDOTEdges(dot)
	if len(nodes) != 2 || !nodes["src/high.js"] || !nodes["src/mid.js"] {
		t.Fatalf("MaxNodes kept wrong nodes: %#v\n%s", nodes, dot)
	}
	for _, e := range edges {
		if !nodes[e[0]] || !nodes[e[1]] {
			t.Fatalf("dangling edge %q -> %q in:\n%s", e[0], e[1], dot)
		}
	}
}

func TestIncludePackages(t *testing.T) {
	rep := &analyzer.Report{
		Summary:       analyzer.Summary{Entry: "src/index.js"},
		InternalFiles: []analyzer.FileStat{{File: "src/index.js"}},
		Packages: []analyzer.PackageStat{
			{Name: "react", Files: 1, Sites: []analyzer.ImportSite{{File: "src/index.js"}}},
		},
	}

	without := DOT(rep, Options{})
	if strings.Contains(without, "pkg:react") || strings.Contains(without, `"src/index.js" -> "pkg:react"`) {
		t.Fatalf("package appeared when IncludePackages=false:\n%s", without)
	}

	with := DOT(rep, Options{IncludePackages: true})
	for _, want := range []string{
		`"pkg:react" [label="react", shape="component"`,
		`"src/index.js" -> "pkg:react";`,
	} {
		if !strings.Contains(with, want) {
			t.Fatalf("package output missing %q:\n%s", want, with)
		}
	}

	mermaid := Mermaid(rep, Options{IncludePackages: true})
	if !strings.Contains(mermaid, `["react"]`) || !strings.Contains(mermaid, " package\n") {
		t.Fatalf("Mermaid package styling missing:\n%s", mermaid)
	}
}

func TestEmptyReportIsWellFormed(t *testing.T) {
	if got, want := DOT(&analyzer.Report{}, Options{}), "digraph importgraph {\n  rankdir=LR;\n}\n"; got != want {
		t.Fatalf("empty DOT mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
	if got, want := Mermaid(&analyzer.Report{}, Options{}), "flowchart LR\n"; got != want {
		t.Fatalf("empty Mermaid mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestDeterminism(t *testing.T) {
	rep := &analyzer.Report{
		Summary: analyzer.Summary{Entry: "src/index.js"},
		InternalFiles: []analyzer.FileStat{
			{File: "src/b.js", FanIn: 2, Sites: []analyzer.ImportSite{{File: "src/index.js"}, {File: "src/a.js"}}},
			{File: "src/index.js"},
			{File: "src/a.js", FanIn: 1, Sites: []analyzer.ImportSite{{File: "src/index.js"}}},
		},
		Packages: []analyzer.PackageStat{
			{Name: "zeta", Files: 1, Sites: []analyzer.ImportSite{{File: "src/b.js"}}},
			{Name: "alpha", Files: 1, Sites: []analyzer.ImportSite{{File: "src/a.js"}}},
		},
	}

	if a, b := DOT(rep, Options{IncludePackages: true, Collapse: true}), DOT(rep, Options{IncludePackages: true, Collapse: true}); a != b {
		t.Fatalf("DOT not deterministic\nfirst:\n%s\nsecond:\n%s", a, b)
	}
	if a, b := Mermaid(rep, Options{IncludePackages: true}), Mermaid(rep, Options{IncludePackages: true}); a != b {
		t.Fatalf("Mermaid not deterministic\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}

func TestWriteDOTAndMermaid(t *testing.T) {
	rep := &analyzer.Report{
		Summary:       analyzer.Summary{Entry: "src/index.js"},
		InternalFiles: []analyzer.FileStat{{File: "src/index.js"}},
	}
	dir := t.TempDir()
	dotPath := filepath.Join(dir, "graph.dot")
	mmdPath := filepath.Join(dir, "graph.mmd")

	if err := WriteDOT(rep, Options{}, dotPath); err != nil {
		t.Fatalf("WriteDOT: %v", err)
	}
	if err := WriteMermaid(rep, Options{}, mmdPath); err != nil {
		t.Fatalf("WriteMermaid: %v", err)
	}
	if got, want := mustRead(t, dotPath), DOT(rep, Options{}); got != want {
		t.Fatalf("DOT file mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
	if got, want := mustRead(t, mmdPath), Mermaid(rep, Options{}); got != want {
		t.Fatalf("Mermaid file mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
	if err := WriteDOT(rep, Options{}, filepath.Join(dir, "missing", "graph.dot")); err == nil {
		t.Fatal("WriteDOT to missing directory succeeded")
	}
	if err := WriteMermaid(rep, Options{}, filepath.Join(dir, "missing", "graph.mmd")); err == nil {
		t.Fatal("WriteMermaid to missing directory succeeded")
	}
}

func TestDOTRendersWithGraphvizWhenInstalled(t *testing.T) {
	dotPath, err := exec.LookPath("dot")
	if err != nil {
		t.Skip("Graphviz dot is not installed")
	}
	rep := &analyzer.Report{
		Summary: analyzer.Summary{Entry: `src/index "quoted".js`},
		InternalFiles: []analyzer.FileStat{
			{File: `src/index "quoted".js`},
			{File: `src/components/Button \雪.js`, FanIn: 1, Sites: []analyzer.ImportSite{{File: `src/index "quoted".js`}}},
		},
	}
	cmd := exec.Command(dotPath, "-Tsvg")
	cmd.Stdin = strings.NewReader(DOT(rep, Options{}))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dot -Tsvg failed: %v\n%s", err, out)
	}
}

func emittedDOTNodes(dot string) map[string]bool {
	nodes := map[string]bool{}
	re := regexp.MustCompile(`(?m)^\s+"((?:\\.|[^"\\])*)"\s+\[`)
	for _, match := range re.FindAllStringSubmatch(dot, -1) {
		nodes[unescapeDOT(match[1])] = true
	}
	return nodes
}

func emittedDOTEdges(dot string) [][2]string {
	var edges [][2]string
	re := regexp.MustCompile(`(?m)^\s+"((?:\\.|[^"\\])*)"\s+->\s+"((?:\\.|[^"\\])*)"`)
	for _, match := range re.FindAllStringSubmatch(dot, -1) {
		edges = append(edges, [2]string{unescapeDOT(match[1]), unescapeDOT(match[2])})
	}
	return edges
}

func unescapeDOT(s string) string {
	replacer := strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\r`, "\r", `\t`, "\t")
	return replacer.Replace(s)
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}
