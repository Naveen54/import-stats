package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"importstats/internal/graph"
	"importstats/internal/project"
	"importstats/internal/resolver"
)

func analyzeTree(t *testing.T, files map[string]string) *Report {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	files["package.json"] = `{"name":"demo"}`
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Detect(root, "")
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Walk(graph.Options{Entry: p.Entry, Src: p.SrcRoot, Res: resolver.New(p.Root, p.SrcRoot, nil)})
	if err != nil {
		t.Fatal(err)
	}
	return Analyze(g, p)
}

func unusedNames(rep *Report) map[string]bool {
	out := map[string]bool{}
	for _, e := range rep.UnusedExports {
		out[e.File+":"+e.Name] = true
	}
	return out
}

func TestUnusedExportsDetectsDeadBindings(t *testing.T) {
	rep := analyzeTree(t, map[string]string{
		"src/index.js": `import { used } from './util';
console.log(used);
`,
		"src/util.js": `export const used = 1;
export const dead = 2;
export function alsoDead() {}
`,
	})

	got := unusedNames(rep)
	if !got["src/util.js:dead"] {
		t.Errorf("expected dead to be reported; got %v", rep.UnusedExports)
	}
	if !got["src/util.js:alsoDead"] {
		t.Errorf("expected alsoDead to be reported; got %v", rep.UnusedExports)
	}
	if got["src/util.js:used"] {
		t.Error("used is imported and must not be reported")
	}
	if rep.Summary.UnusedExports != len(rep.UnusedExports) {
		t.Errorf("summary count = %d, want %d", rep.Summary.UnusedExports, len(rep.UnusedExports))
	}
}

func TestUnusedExportsHonoursAliasesAndDefaults(t *testing.T) {
	rep := analyzeTree(t, map[string]string{
		"src/index.js": `import Thing, { a as renamed } from './util';
console.log(Thing, renamed);
`,
		"src/util.js": `export default function Thing() {}
export const a = 1;
export const b = 2;
`,
	})

	got := unusedNames(rep)
	if got["src/util.js:default"] {
		t.Error("default is imported and must not be reported")
	}
	if got["src/util.js:a"] {
		t.Error("a is imported under an alias and must not be reported")
	}
	if !got["src/util.js:b"] {
		t.Errorf("b should be reported; got %v", rep.UnusedExports)
	}
}

// A namespace import may touch any property, so the file must opt out wholly.
func TestUnusedExportsSuppressedByNamespaceImport(t *testing.T) {
	rep := analyzeTree(t, map[string]string{
		"src/index.js": `import * as util from './util';
console.log(util);
`,
		"src/util.js": `export const a = 1;
export const b = 2;
`,
	})
	if len(rep.UnusedExports) != 0 {
		t.Errorf("namespace import must suppress everything, got %v", rep.UnusedExports)
	}
}

// Re-exports forward names to consumers we cannot follow, so barrels opt out.
func TestUnusedExportsSuppressedByBarrelReExport(t *testing.T) {
	rep := analyzeTree(t, map[string]string{
		"src/index.js": `import { a } from './barrel';
console.log(a);
`,
		"src/barrel.js": `export { a, b } from './util';
`,
		"src/util.js": `export const a = 1;
export const b = 2;
`,
	})
	for _, e := range rep.UnusedExports {
		if e.File == "src/util.js" {
			t.Errorf("re-exported file must be exempt, got %+v", e)
		}
	}
}

// The entry is the root of the graph; nothing imports it by definition.
func TestUnusedExportsSkipsEntry(t *testing.T) {
	rep := analyzeTree(t, map[string]string{
		"src/index.js": `export const neverImported = 1;
`,
	})
	if len(rep.UnusedExports) != 0 {
		t.Errorf("entry exports must be skipped, got %v", rep.UnusedExports)
	}
}

func TestUnusedExportsDeterministic(t *testing.T) {
	files := map[string]string{
		"src/index.js": `import { used } from './util';
console.log(used);
`,
		"src/util.js": `export const used = 1;
export const z = 2;
export const a = 3;
export const m = 4;
`,
	}
	first := analyzeTree(t, files)
	second := analyzeTree(t, files)

	if len(first.UnusedExports) != 3 {
		t.Fatalf("expected 3 unused, got %v", first.UnusedExports)
	}
	for i := range first.UnusedExports {
		if first.UnusedExports[i] != second.UnusedExports[i] {
			t.Fatalf("nondeterministic order: %v vs %v", first.UnusedExports, second.UnusedExports)
		}
	}
	// Sorted by line within a file.
	for i := 1; i < len(first.UnusedExports); i++ {
		if first.UnusedExports[i-1].Line > first.UnusedExports[i].Line {
			t.Errorf("not sorted by line: %v", first.UnusedExports)
		}
	}
}
