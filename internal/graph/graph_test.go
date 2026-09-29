package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"importstats/internal/resolver"
	"importstats/internal/scanner"
)

func writeFile(t *testing.T, root, name, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

func walkFixture(t *testing.T, root string, ignore []string, workers int) *Graph {
	t.Helper()
	entry := filepath.Join(root, "src", "index.js")
	src := filepath.Join(root, "src")
	g, err := Walk(Options{Entry: entry, Src: src, Res: resolver.New(root, src, nil), Ignore: ignore, Workers: workers})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func rels(root string, paths []string) []string {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			out[i] = filepath.ToSlash(p)
			continue
		}
		out[i] = filepath.ToSlash(rel)
	}
	return out
}

func hasFile(root string, files []string, name string) bool {
	want := filepath.Join(root, filepath.FromSlash(name))
	if real, err := filepath.EvalSymlinks(want); err == nil {
		want = real
	}
	for _, f := range files {
		if f == want {
			return true
		}
	}
	return false
}

func findEdge(t *testing.T, g *Graph, from, spec string) Edge {
	t.Helper()
	for _, e := range g.Edges {
		if e.From == from && e.Specifier == spec {
			return e
		}
	}
	t.Fatalf("edge from %s spec %s not found in %+v", from, spec, g.Edges)
	return Edge{}
}

func TestWalkReachesTransitivelyImportedFilesAndRecordsEdges(t *testing.T) {
	root := t.TempDir()
	entry := writeFile(t, root, "src/index.js", "import App from './App';\n")
	app := writeFile(t, root, "src/App.js", "\nconst util = require('./util');\n")
	util := writeFile(t, root, "src/util.js", "export const util = 1;\n")

	g := walkFixture(t, root, nil, 2)
	wantFiles := []string{"src/App.js", "src/index.js", "src/util.js"}
	if got := rels(root, g.Files); !reflect.DeepEqual(got, wantFiles) {
		t.Fatalf("files = %v, want %v", got, wantFiles)
	}

	e := findEdge(t, g, entry, "./App")
	if e.Kind != scanner.KindImport || e.Line != 1 || e.Res.Path != app {
		t.Errorf("entry edge = %+v", e)
	}
	e = findEdge(t, g, app, "./util")
	if e.Kind != scanner.KindRequire || e.Line != 2 || e.Res.Path != util {
		t.Errorf("app edge = %+v", e)
	}
}

func TestWalkTerminatesOnCyclesAndVisitsEachFileOnce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/index.js", "import './a';\n")
	writeFile(t, root, "src/a.js", "import './b';\n")
	writeFile(t, root, "src/b.js", "import './a';\n")

	g := walkFixture(t, root, nil, 8)
	counts := map[string]int{}
	for _, f := range g.Files {
		counts[filepath.ToSlash(f)]++
	}
	if got, want := len(g.Files), 3; got != want {
		t.Fatalf("len(files) = %d, want %d (%v)", got, want, rels(root, g.Files))
	}
	for f, n := range counts {
		if n != 1 {
			t.Fatalf("file %s visited %d times", f, n)
		}
	}
}

func TestWalkTerminatesOnSymlinkLoop(t *testing.T) {
	root := t.TempDir()
	entry := writeFile(t, root, "src/index.js", "import './loop/index.js';\n")
	loop := filepath.Join(root, "src", "loop")
	if err := os.Symlink(filepath.Join(root, "src"), loop); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	done := make(chan struct {
		g   *Graph
		err error
	}, 1)
	go func() {
		g, err := Walk(Options{
			Entry:   entry,
			Src:     filepath.Join(root, "src"),
			Res:     resolver.New(root, filepath.Join(root, "src"), nil),
			Workers: 4,
		})
		done <- struct {
			g   *Graph
			err error
		}{g: g, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if gotFiles, want := rels(root, got.g.Files), []string{"src/index.js"}; !reflect.DeepEqual(gotFiles, want) {
			t.Fatalf("files = %v, want %v", gotFiles, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Walk did not terminate on symlink loop")
	}
}

func TestWalkSymlinkAliasScansPhysicalFileOnce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/index.js", "import './target';\nimport './alias';\n")
	target := writeFile(t, root, "src/target.js", "import React from 'react';\n")
	if err := os.Symlink(target, filepath.Join(root, "src", "alias.js")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	g := walkFixture(t, root, nil, 4)
	if got, want := rels(root, g.Files), []string{"src/index.js", "src/target.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	var reactEdges int
	for _, e := range g.Edges {
		if e.Res.Type == resolver.TypePackage && e.Res.Package == "react" {
			reactEdges++
		}
	}
	if reactEdges != 1 {
		t.Fatalf("react edges = %d, want 1; edges=%+v", reactEdges, g.Edges)
	}
}

func TestWalkDiamondSharedDependencyVisitedOnce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/index.js", "import './left';\nimport './right';\n")
	writeFile(t, root, "src/left.js", "import './shared';\n")
	writeFile(t, root, "src/right.js", "import './shared';\n")
	writeFile(t, root, "src/shared.js", "export const shared = 1;\n")

	g := walkFixture(t, root, nil, 8)
	if got, want := len(g.Files), 4; got != want {
		t.Fatalf("len(files) = %d, want %d (%v)", got, want, rels(root, g.Files))
	}
	if !hasFile(root, g.Files, "src/shared.js") {
		t.Fatalf("shared dependency not visited: %v", rels(root, g.Files))
	}
}

func TestWalkDetectsOrphans(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/index.js", "import './used';\n")
	writeFile(t, root, "src/used.js", "export const used = 1;\n")
	writeFile(t, root, "src/orphan.js", "export const orphan = 1;\n")

	g := walkFixture(t, root, nil, 1)
	want := []string{"src/orphan.js"}
	if got := rels(root, g.Orphans); !reflect.DeepEqual(got, want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
}

func TestWalkDoesNotTraverseNodeModules(t *testing.T) {
	root := t.TempDir()
	entry := writeFile(t, root, "src/index.js", "import './node_modules/pkg';\n")
	mod := writeFile(t, root, "src/node_modules/pkg.js", "import '../should-not-visit';\n")
	writeFile(t, root, "src/should-not-visit.js", "export const x = 1;\n")

	g := walkFixture(t, root, nil, 4)
	if hasFile(root, g.Files, "src/node_modules/pkg.js") {
		t.Fatalf("node_modules file was traversed: %v", rels(root, g.Files))
	}
	if hasFile(root, g.Files, "src/should-not-visit.js") {
		t.Fatalf("import inside node_modules was traversed: %v", rels(root, g.Files))
	}
	e := findEdge(t, g, entry, "./node_modules/pkg")
	if e.Res.Path != mod {
		t.Fatalf("node_modules edge path = %q, want %q", e.Res.Path, mod)
	}
}

func TestWalkIgnorePatternsSubstringAndBasenameGlob(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/index.js", "import './keep';\nimport './ignored-dir/a';\nimport './widget.test';\n")
	writeFile(t, root, "src/keep.js", "export const keep = 1;\n")
	writeFile(t, root, "src/ignored-dir/a.js", "export const a = 1;\n")
	writeFile(t, root, "src/widget.test.js", "export const test = 1;\n")

	g := walkFixture(t, root, []string{"ignored-dir", "*.test.js"}, 4)
	if got, want := rels(root, g.Files), []string{"src/index.js", "src/keep.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if got, want := rels(root, g.SrcFiles), []string{"src/index.js", "src/keep.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("srcFiles = %v, want %v", got, want)
	}
	if got := rels(root, g.Orphans); len(got) != 0 {
		t.Fatalf("orphans = %v, want none", got)
	}
}

func TestWalkIgnoreDropsLocalEdge(t *testing.T) {
	root := t.TempDir()
	entry := writeFile(t, root, "src/index.js", "import './generated/large';\nimport './keep';\n")
	writeFile(t, root, "src/generated/large.js", "export const large = 1;\n")
	writeFile(t, root, "src/keep.js", "export const keep = 1;\n")

	g := walkFixture(t, root, []string{"generated"}, 2)
	if got, want := rels(root, g.Files), []string{"src/index.js", "src/keep.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for _, e := range g.Edges {
		if e.From == entry && e.Specifier == "./generated/large" {
			t.Fatalf("ignored local edge was retained: %+v", e)
		}
	}
	if got, want := rels(root, g.Ignored), []string{"src/generated/large.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ignored = %v, want %v", got, want)
	}
}

func TestWalkRecordsButDoesNotTraverseNonSourceTargets(t *testing.T) {
	root := t.TempDir()
	entry := writeFile(t, root, "src/index.js", "import './style.scss';\nimport logo from './logo.svg';\nimport data from './data.json';\n")
	style := writeFile(t, root, "src/style.scss", "body{}")
	logo := writeFile(t, root, "src/logo.svg", "<svg/>")
	data := writeFile(t, root, "src/data.json", `{"ok":true}`)

	g := walkFixture(t, root, nil, 2)
	if got, want := rels(root, g.Files), []string{"src/index.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	cases := map[string]struct {
		path string
		typ  resolver.Type
	}{
		"./style.scss": {style, resolver.TypeStyle},
		"./logo.svg":   {logo, resolver.TypeAsset},
		"./data.json":  {data, resolver.TypeJSONFile},
	}
	for spec, want := range cases {
		e := findEdge(t, g, entry, spec)
		if e.Res.Path != want.path || e.Res.Type != want.typ {
			t.Errorf("edge %s = %+v, want path %s type %s", spec, e.Res, want.path, want.typ)
		}
	}
}

func TestWalkUnreadableTransitiveFileIsSkippedNotAnalyzed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/index.js", "import './unreadable';\n")
	unreadable := writeFile(t, root, "src/unreadable.js", "import React from 'react';\n")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(unreadable, 0o644)
	if b, err := os.ReadFile(unreadable); err == nil {
		_ = b
		t.Skip("file permissions are not enforced")
	}

	g := walkFixture(t, root, nil, 1)
	if got, want := rels(root, g.Files), []string{"src/index.js"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if got, want := g.Skipped, []string{unreadable}; !reflect.DeepEqual(got, want) {
		t.Fatalf("skipped = %v, want %v", got, want)
	}
}

func TestWalkUnreadableOrDeletedEntryReturnsError(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "src", "deleted.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src")
	g, err := Walk(Options{Entry: entry, Src: src, Res: resolver.New(root, src, nil), Workers: 1})
	if err == nil {
		t.Fatal("Walk returned nil error for unreadable entry")
	}
	if got, want := g.Skipped, []string{entry}; !reflect.DeepEqual(got, want) {
		t.Fatalf("skipped = %v, want %v", got, want)
	}
}

func TestWalkDeterministicAcrossRunsAndWorkers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/index.js", "import './a';\nimport './b';\nimport './c';\n")
	writeFile(t, root, "src/a.js", "import './shared';\nimport './asset.svg';\n")
	writeFile(t, root, "src/b.js", "import './shared';\nconst c = require('./c');\n")
	writeFile(t, root, "src/c.js", "export * from './shared';\n")
	writeFile(t, root, "src/shared.js", "export const shared = 1;\n")
	writeFile(t, root, "src/asset.svg", "<svg/>")
	writeFile(t, root, "src/orphan.js", "export const orphan = 1;\n")

	var baseline []byte
	for _, workers := range []int{1, 8, 1, 8, 4} {
		g := walkFixture(t, root, nil, workers)
		b, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = b
			continue
		}
		if string(b) != string(baseline) {
			t.Fatalf("walk with workers=%d differed\n got: %s\nwant: %s", workers, b, baseline)
		}
	}
}
