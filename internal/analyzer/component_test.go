package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"importstats/internal/graph"
	"importstats/internal/project"
	"importstats/internal/resolver"
)

// componentFixture builds a small project exercising every IsComponent case:
// JSX-only, PascalCase-named-export-only, a plain util, an index barrel, and
// the documented miss case (React.createElement with no JSX, no PascalCase
// named export).
func componentFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = realRoot
	}
	files := map[string]string{
		"package.json": `{"name":"demo"}`,
		"src/index.js": `import App from './App';
import Home from './pages/Home';
import { formatDate } from './utils/date';
import { createHomeElement } from './pages/CreateElementHome';
import * as Barrel from './components';
`,
		// JSX-returning default export: caught via HasJSX.
		"src/App.js": `import React from 'react';
export default function App() {
  return <div className="app">Hi</div>;
}
`,
		// Default export of a JSX-returning function assigned via a bare
		// identifier default export; still caught via HasJSX because the
		// JSX appears somewhere in the file.
		"src/pages/Home.js": `import React from 'react';
function Home() {
  return <section><h1>Home</h1></section>;
}
export default Home;
`,
		// No JSX at all: a plain camelCase util. Must NOT be flagged.
		"src/utils/date.js": `export function formatDate(d) {
  return String(d);
}
`,
		// Barrel/index file re-exporting named components. Not itself a
		// component (no JSX, no PascalCase own export other than the
		// re-exports, which do not count).
		"src/components/index.js": `export { default as Icon } from './Icon';
`,
		"src/components/Icon.js": `import React from 'react';
export default function Icon() {
  return <svg />;
}
`,
		// Documented miss case: a component built purely with
		// React.createElement, no JSX, and no PascalCase named export
		// (only the "default" sentinel name is available to us).
		"src/pages/CreateElementHome.js": `import React from 'react';
export function createHomeElement() {
  return React.createElement('div', null, 'Home');
}
`,
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func analyzeComponentFixture(t *testing.T) *Report {
	t.Helper()
	root := componentFixture(t)
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

func fileByName(rep *Report, name string) *FileStat {
	for i := range rep.InternalFiles {
		if rep.InternalFiles[i].File == name {
			return &rep.InternalFiles[i]
		}
	}
	return nil
}

func TestIsComponentDetection(t *testing.T) {
	rep := analyzeComponentFixture(t)

	cases := []struct {
		file string
		want bool
	}{
		{"src/App.js", true},                      // JSX in a default-exported function
		{"src/pages/Home.js", true},               // JSX in a named function, default-exported by identifier
		{"src/components/Icon.js", true},          // JSX, reached only via the barrel
		{"src/utils/date.js", false},              // plain camelCase util, no JSX
		{"src/components/index.js", false},        // barrel: re-exports only, no JSX of its own
		{"src/pages/CreateElementHome.js", false}, // documented miss: React.createElement, no JSX, no PascalCase export
	}
	for _, tc := range cases {
		fs := fileByName(rep, tc.file)
		if fs == nil {
			t.Fatalf("%s missing from internal files", tc.file)
		}
		if fs.IsComponent != tc.want {
			t.Errorf("%s: IsComponent = %v, want %v", tc.file, fs.IsComponent, tc.want)
		}
	}
}

func TestIsPascalCaseHeuristic(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Header", true},
		{"MyComponent", true},
		{"App2", true},
		{"formatDate", false},
		{"default", false},
		{"API", false}, // all-caps constant, not a component name
		{"_Private", false},
		{"", false},
		{"A", false}, // single letter: no lowercase to establish a word
	}
	for _, tc := range cases {
		if got := isPascalCase(tc.name); got != tc.want {
			t.Errorf("isPascalCase(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
