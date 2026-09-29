package analyzer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"importstats/internal/graph"
	"importstats/internal/project"
	"importstats/internal/resolver"
)

// fixture builds a small React-like project on disk.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = realRoot
	}
	files := map[string]string{
		"package.json": `{"name":"demo","dependencies":{"react":"^17.0.0","lodash":"^4.17.21","never-used":"^1.0.0"},"devDependencies":{"jest":"^29.0.0"}}`,
		"src/index.js": `import React from 'react';
import { render } from 'react-dom';
import App from './app/App';
import './styles/main.scss';
`,
		"src/app/App.js": `import React from 'react';
import { debounce, isEmpty } from 'lodash';
import Header from '../components/Header';
const Lazy = React.lazy(() => import('../pages/Home'));
export default function App() { return null; }
`,
		"src/components/Header.js": `import React from 'react';
import cn from 'classnames';
import { helper } from '../utils/helper';
export default Header;
`,
		"src/utils/helper.js": `const _ = require('lodash');
import Header from '../components/Header';
export const helper = 1;
`,
		"src/pages/Home/index.js": `import React from 'react';
import logo from '../../assets/logo.svg';
export default Home;
`,
		"src/orphan.js":        `import React from 'react';`,
		"src/styles/main.scss": `body{}`,
		"src/assets/logo.svg":  `<svg/>`,
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

func analyzeFixture(t *testing.T) *Report {
	t.Helper()
	root := fixture(t)
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

func pkg(rep *Report, name string) *PackageStat {
	for i := range rep.Packages {
		if rep.Packages[i].Name == name {
			return &rep.Packages[i]
		}
	}
	return nil
}

func TestEndToEnd(t *testing.T) {
	rep := analyzeFixture(t)

	if rep.Summary.Entry != "src/index.js" {
		t.Fatalf("entry = %s", rep.Summary.Entry)
	}
	if rep.Summary.FilesAnalyzed != 5 {
		t.Errorf("filesAnalyzed = %d, want 5", rep.Summary.FilesAnalyzed)
	}

	react := pkg(rep, "react")
	// orphan.js also imports react but is unreachable, so it must not count.
	if react == nil || react.Imports != 4 || react.Files != 4 {
		t.Errorf("react = %+v, want 4 imports in 4 files", react)
	}
	if react != nil && react.DepType != "dependency" {
		t.Errorf("react depType = %s", react.DepType)
	}

	lodash := pkg(rep, "lodash")
	if lodash == nil || lodash.Imports != 2 || lodash.Files != 2 {
		t.Fatalf("lodash = %+v, want 2 imports in 2 files", lodash)
	}
	if lodash.Kinds["import"] != 1 || lodash.Kinds["require"] != 1 {
		t.Errorf("lodash kinds = %v", lodash.Kinds)
	}
	syms := map[string]int{}
	for _, s := range lodash.Symbols {
		syms[s.Name] = s.Count
	}
	if syms["debounce"] != 1 || syms["isEmpty"] != 1 || syms["default:_"] != 1 {
		t.Errorf("lodash symbols = %v", syms)
	}

	if cn := pkg(rep, "classnames"); cn == nil || cn.DepType != "undeclared" {
		t.Errorf("classnames should be undeclared, got %+v", cn)
	}
	if rd := pkg(rep, "react-dom"); rd == nil || rd.DepType != "undeclared" {
		t.Errorf("react-dom should be undeclared, got %+v", rd)
	}

	if len(rep.UnusedDeps) != 1 || rep.UnusedDeps[0] != "never-used" {
		t.Errorf("unused deps = %v", rep.UnusedDeps)
	}

	if rep.Summary.StyleImports != 1 || rep.Summary.AssetImports != 1 {
		t.Errorf("style=%d asset=%d", rep.Summary.StyleImports, rep.Summary.AssetImports)
	}

	if len(rep.Orphans) != 1 || rep.Orphans[0] != "src/orphan.js" {
		t.Errorf("orphans = %v", rep.Orphans)
	}

	if len(rep.Cycles) != 1 {
		t.Fatalf("cycles = %v", rep.Cycles)
	}
	want := map[string]bool{"src/components/Header.js": true, "src/utils/helper.js": true}
	for _, f := range rep.Cycles[0] {
		if !want[f] {
			t.Errorf("unexpected file %s in cycle %v", f, rep.Cycles[0])
		}
	}
}

func TestDynamicImportFollowed(t *testing.T) {
	rep := analyzeFixture(t)
	var home *FileStat
	for i := range rep.InternalFiles {
		if rep.InternalFiles[i].File == "src/pages/Home/index.js" {
			home = &rep.InternalFiles[i]
		}
	}
	if home == nil || home.Imports != 1 {
		t.Fatalf("lazy-imported page not tracked: %+v", home)
	}
	if home.Sites[0].Kind != "dynamic-import" {
		t.Errorf("kind = %s, want dynamic-import", home.Sites[0].Kind)
	}
}

func TestFanInFanOut(t *testing.T) {
	rep := analyzeFixture(t)
	for _, f := range rep.InternalFiles {
		if f.File == "src/components/Header.js" {
			if f.FanIn != 2 || f.FanOut != 1 {
				t.Errorf("Header fanIn=%d fanOut=%d, want 2/1", f.FanIn, f.FanOut)
			}
		}
	}
}

func TestFanOutCountsDistinctLocalTargets(t *testing.T) {
	root := t.TempDir()
	p := &project.Info{
		Entry:   filepath.Join(root, "src", "index.ts"),
		Root:    root,
		SrcRoot: filepath.Join(root, "src"),
	}
	from := filepath.Join(root, "src", "index.ts")
	target := filepath.Join(root, "src", "types.ts")
	g := &graph.Graph{
		Files: []string{from, target},
		Edges: []graph.Edge{
			{From: from, Specifier: "./types", Kind: "import", Line: 1, Res: resolver.Result{Type: resolver.TypeLocal, Path: target}, Symbols: []string{"A"}},
			{From: from, Specifier: "./types", Kind: "import", Line: 2, Res: resolver.Result{Type: resolver.TypeLocal, Path: target}, Symbols: []string{"value"}},
		},
	}

	rep := Analyze(g, p)
	var index *FileStat
	for i := range rep.InternalFiles {
		if rep.InternalFiles[i].File == "src/index.ts" {
			index = &rep.InternalFiles[i]
		}
	}
	if index == nil {
		t.Fatalf("src/index.ts not found in internal files: %+v", rep.InternalFiles)
	}
	if index.FanOut != 1 {
		t.Fatalf("FanOut = %d, want 1 distinct local target", index.FanOut)
	}
	var types *FileStat
	for i := range rep.InternalFiles {
		if rep.InternalFiles[i].File == "src/types.ts" {
			types = &rep.InternalFiles[i]
		}
	}
	if types == nil || types.Imports != 2 {
		t.Fatalf("types imports = %+v, want 2 import statements", types)
	}
}

func TestSkippedFilesProduceUnreadableWarnings(t *testing.T) {
	root := t.TempDir()
	skipped := filepath.Join(root, "src", "unreadable.ts")
	p := &project.Info{Entry: filepath.Join(root, "src", "index.ts"), Root: root, SrcRoot: filepath.Join(root, "src")}
	g := &graph.Graph{
		Files:   []string{filepath.Join(root, "src", "index.ts"), skipped},
		Skipped: []string{skipped, skipped},
	}

	rep := Analyze(g, p)
	if len(rep.Warnings) != 1 || rep.Summary.Warnings != 1 {
		t.Fatalf("warnings = %+v summary=%d, want exactly one warning", rep.Warnings, rep.Summary.Warnings)
	}
	w := rep.Warnings[0]
	if w.File != "src/unreadable.ts" || w.Type != "unreadable" || w.Hint == "" {
		t.Fatalf("warning = %+v, want unreadable warning for skipped file", w)
	}
}

func TestAnalyzeJSONDeterministic(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "aliaspkg"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &project.Info{
		Entry:   filepath.Join(root, "src", "index.ts"),
		Root:    root,
		SrcRoot: filepath.Join(root, "src"),
		Deps:    map[string]string{"unused-b": "2.0.0", "unused-a": "1.0.0"},
	}
	from := filepath.Join(root, "src", "index.ts")
	other := filepath.Join(root, "src", "other.ts")
	g := &graph.Graph{
		Files: []string{from, other},
		Edges: []graph.Edge{
			{From: from, Specifier: "z-pkg", Kind: "import", Line: 4, Res: resolver.Result{Type: resolver.TypePackage, Package: "z-pkg"}, Symbols: []string{"Z"}},
			{From: from, Specifier: "aliaspkg", Kind: "import", Line: 3, Res: resolver.Result{Type: resolver.TypePackage, Package: "aliaspkg"}, Symbols: []string{"A"}},
			{From: other, Specifier: "./missing", Kind: "import", Line: 2, Res: resolver.Result{Type: resolver.TypeMissing}},
			{From: from, Specifier: "./a.css", Kind: "side-effect", Line: 1, Res: resolver.Result{Type: resolver.TypeStyle, Path: filepath.Join(root, "src", "a.css")}},
			{From: from, Specifier: "./b.svg", Kind: "import", Line: 1, Res: resolver.Result{Type: resolver.TypeAsset, Path: filepath.Join(root, "src", "b.svg")}},
		},
		Orphans: []string{filepath.Join(root, "src", "z.ts"), filepath.Join(root, "src", "a.ts")},
		Skipped: []string{
			filepath.Join(root, "src", "unreadable-b.ts"),
			filepath.Join(root, "src", "unreadable-a.ts"),
		},
		SrcFiles: []string{from, other},
	}

	first, err := json.Marshal(Analyze(g, p))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		next, err := json.Marshal(Analyze(g, p))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("Analyze JSON changed between runs:\nfirst: %s\n next: %s", first, next)
		}
	}
}

// TestPeerAndOptionalDependenciesAreDeclared guards against packages listed
// only in peerDependencies/optionalDependencies being reported as undeclared.
func TestPeerAndOptionalDependenciesAreDeclared(t *testing.T) {
	root := t.TempDir()
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = realRoot
	}
	files := map[string]string{
		"package.json": `{"name":"demo",
			"dependencies":{"react":"^18.0.0"},
			"devDependencies":{"jest":"^29.0.0"},
			"peerDependencies":{"react-dom":"^18.0.0","jest":"^28.0.0"},
			"optionalDependencies":{"classnames":"^2.3.0"}}`,
		"src/index.js": `import React from 'react';
import { render } from 'react-dom';
import cn from 'classnames';
import jest from 'jest';
import axios from 'axios';
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

	p, err := project.Detect(root, "")
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Walk(graph.Options{Entry: p.Entry, Src: p.SrcRoot, Res: resolver.New(p.Root, p.SrcRoot, nil)})
	if err != nil {
		t.Fatal(err)
	}
	rep := Analyze(g, p)

	want := map[string]struct{ depType, version string }{
		"react":      {"dependency", "^18.0.0"},
		"react-dom":  {"peerDependency", "^18.0.0"},
		"classnames": {"optionalDependency", "^2.3.0"},
		"jest":       {"devDependency", "^29.0.0"}, // devDependencies win over peerDependencies
		"axios":      {"undeclared", ""},
	}
	for name, exp := range want {
		ps := pkg(rep, name)
		if ps == nil {
			t.Errorf("package %s missing from the report", name)
			continue
		}
		if ps.DepType != exp.depType {
			t.Errorf("%s depType = %q, want %q", name, ps.DepType, exp.depType)
		}
		if ps.Version != exp.version {
			t.Errorf("%s version = %q, want %q", name, ps.Version, exp.version)
		}
	}

	if len(rep.UndeclaredDeps) != 1 || rep.UndeclaredDeps[0] != "axios" {
		t.Errorf("undeclaredDependencies = %v, want [axios]", rep.UndeclaredDeps)
	}
	if len(rep.UnusedDeps) != 0 {
		t.Errorf("unusedDependencies = %v, want none", rep.UnusedDeps)
	}
}

func TestEntryChains(t *testing.T) {
	rep := analyzeFixture(t)

	// Header is reached entry -> App -> Header in the fixture.
	var header *FileStat
	for i := range rep.InternalFiles {
		if rep.InternalFiles[i].File == "src/components/Header.js" {
			header = &rep.InternalFiles[i]
		}
	}
	if header == nil {
		t.Fatal("Header.js missing from internal files")
	}
	want := []string{"src/index.js", "src/app/App.js", "src/components/Header.js"}
	if !reflect.DeepEqual(header.EntryChain, want) {
		t.Errorf("Header chain = %v, want %v", header.EntryChain, want)
	}

	// The entry's own chain is just itself.
	for i := range rep.InternalFiles {
		if rep.InternalFiles[i].File == "src/index.js" {
			if !reflect.DeepEqual(rep.InternalFiles[i].EntryChain, []string{"src/index.js"}) {
				t.Errorf("entry chain = %v", rep.InternalFiles[i].EntryChain)
			}
		}
	}

	// react-dom is imported directly by the entry, so its chain is length 1.
	if ps := pkg(rep, "react-dom"); ps == nil {
		t.Error("react-dom missing")
	} else if !reflect.DeepEqual(ps.EntryChain, []string{"src/index.js"}) {
		t.Errorf("react-dom chain = %v, want [src/index.js]", ps.EntryChain)
	}

	// classnames is only imported by Header, so it reports the full path.
	if ps := pkg(rep, "classnames"); ps == nil {
		t.Error("classnames missing")
	} else if !reflect.DeepEqual(ps.EntryChain, want) {
		t.Errorf("classnames chain = %v, want %v", ps.EntryChain, want)
	}

	// Every chain must start at the entry and end at its own file.
	for _, fs := range rep.InternalFiles {
		if len(fs.EntryChain) == 0 {
			t.Errorf("%s has no entry chain", fs.File)
			continue
		}
		if fs.EntryChain[0] != "src/index.js" {
			t.Errorf("%s chain starts at %s", fs.File, fs.EntryChain[0])
		}
		if fs.EntryChain[len(fs.EntryChain)-1] != fs.File {
			t.Errorf("%s chain ends at %s", fs.File, fs.EntryChain[len(fs.EntryChain)-1])
		}
	}
}

// TestShortestChainsPrefersShortestPath checks the BFS reports the shorter of
// two routes and stays deterministic when both are equally short.
func TestShortestChainsPrefersShortestPath(t *testing.T) {
	adj := map[string][]string{
		"entry": {"a", "b"},
		"a":     {"deep"},
		"b":     {"deep"},
		"deep":  {"leaf"},
	}
	chains := shortestChains("entry", adj)

	if got := chains["deep"]; !reflect.DeepEqual(got, []string{"entry", "a", "deep"}) {
		t.Errorf("deep chain = %v, want [entry a deep]", got)
	}
	if got := chains["leaf"]; !reflect.DeepEqual(got, []string{"entry", "a", "deep", "leaf"}) {
		t.Errorf("leaf chain = %v", got)
	}
	if _, ok := chains["unreachable"]; ok {
		t.Error("unreachable node should have no chain")
	}
}

// TestShortestChainsTerminatesOnCycle guards the BFS against import cycles.
func TestShortestChainsTerminatesOnCycle(t *testing.T) {
	adj := map[string][]string{
		"entry": {"a"},
		"a":     {"b"},
		"b":     {"a", "c"},
		"c":     {"a"},
	}
	chains := shortestChains("entry", adj)
	if got := chains["c"]; !reflect.DeepEqual(got, []string{"entry", "a", "b", "c"}) {
		t.Errorf("c chain = %v", got)
	}
	if len(chains) != 4 {
		t.Errorf("chains = %d, want 4", len(chains))
	}
}
