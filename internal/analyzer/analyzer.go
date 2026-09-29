// Package analyzer aggregates a walked import graph into report statistics.
package analyzer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"importstats/internal/graph"
	"importstats/internal/project"
	"importstats/internal/resolver"
	"importstats/internal/scanner"
)

// ImportSite is a single import statement referencing a target.
type ImportSite struct {
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Kind    string   `json:"kind"`
	Spec    string   `json:"specifier"`
	Symbols []string `json:"symbols,omitempty"`
}

// SymbolUse counts how often a named binding is imported from a package.
type SymbolUse struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// PackageStat aggregates all imports of one npm package.
type PackageStat struct {
	Name     string   `json:"name"`
	Imports  int      `json:"imports"` // number of import statements
	Files    int      `json:"files"`   // distinct importing files
	Version  string   `json:"version,omitempty"`
	DepType  string   `json:"depType"` // dependency | devDependency | peerDependency | optionalDependency | undeclared | alias? | builtin
	Subpaths []string `json:"subpaths,omitempty"`
	// EntryChain is the shortest import chain from the entry to the nearest
	// file importing this package, answering "why is this here?".
	EntryChain []string       `json:"entryChain,omitempty"`
	Symbols    []SymbolUse    `json:"symbols,omitempty"`
	Kinds      map[string]int `json:"kinds"`
	// Weight fields are measured from node_modules when it is installed.
	SizeBytes      int64 `json:"sizeBytes,omitempty"`
	FileCount      int   `json:"fileCount,omitempty"`
	TransitiveDeps int   `json:"transitiveDeps,omitempty"`
	// Shake describes how tree-shakeable the installed package is, derived
	// from its package.json. Nil when node_modules is absent or unread.
	Shake *ShakeInfo   `json:"shake,omitempty"`
	Sites []ImportSite `json:"sites"`
}

// ShakeInfo reports whether a package can be tree-shaken by a bundler, based
// on the fields its own package.json declares.
type ShakeInfo struct {
	// Format is the best module format the package offers: "esm", "dual",
	// "cjs" or "unknown".
	Format string `json:"format"`
	// HasModule is true when a "module" field is present, Exports when an
	// "exports" map is present, and SideEffectsFalse when the package
	// declares "sideEffects": false.
	HasModule        bool `json:"hasModule,omitempty"`
	HasExports       bool `json:"hasExports,omitempty"`
	SideEffectsFalse bool `json:"sideEffectsFalse,omitempty"`
	// Shakeable is true when a bundler can reasonably drop unused exports.
	Shakeable bool `json:"shakeable"`
	// Reason explains the verdict in one human-readable sentence.
	Reason string `json:"reason,omitempty"`
}

// FileStat describes one internal module.
type FileStat struct {
	File    string `json:"file"`
	FanIn   int    `json:"fanIn"`
	FanOut  int    `json:"fanOut"`
	Imports int    `json:"imports"` // import statements pointing at this file
	// EntryChain is the shortest import chain from the entry to this file,
	// starting at the entry and ending at the file itself.
	EntryChain []string     `json:"entryChain,omitempty"`
	Sites      []ImportSite `json:"sites,omitempty"`
	// IsComponent is a heuristic guess that this file is a React component,
	// used to build the Component Tree dashboard tab. It is true when the
	// scanner found JSX syntax in the file, or when the file exports at
	// least one PascalCase-named binding (default or named). It will miss
	// components built purely with React.createElement and no JSX and no
	// PascalCase export name; this is a deliberate, documented limitation
	// rather than an attempt at full type-aware component detection.
	IsComponent bool `json:"isComponent,omitempty"`
}

// Insight is advisory guidance about how imports are structured. Unlike a
// Warning, nothing went wrong; the analysis merely spotted something that is
// usually worth acting on.
type Insight struct {
	ID       string   `json:"id"`       // deep-import | barrel | duplicate-purpose
	Severity string   `json:"severity"` // info | warn
	Subject  string   `json:"subject"`  // package or file the insight is about
	Message  string   `json:"message"`
	Evidence []string `json:"evidence,omitempty"`
}

// ExportStat is an exported binding that no reachable file imports.
type ExportStat struct {
	File   string `json:"file"`
	Name   string `json:"name"`
	Line   int    `json:"line"`
	Kind   string `json:"kind"`   // const | function | class | default | ...
	Reason string `json:"reason"` // why it is only "possibly" unused
}

// RuleViolation is a local import that breaks a --rule layering constraint.
type RuleViolation struct {
	Rule string `json:"rule"`
	From string `json:"from"`
	To   string `json:"to"`
	Line int    `json:"line"`
}

// Warning records something the analysis could not resolve.
type Warning struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Spec string `json:"specifier"`
	Kind string `json:"kind"`
	Type string `json:"type"` // missing | dynamic | unreadable | alias
	Hint string `json:"hint,omitempty"`
}

// Summary holds headline numbers.
type Summary struct {
	Entry          string `json:"entry"`
	ProjectName    string `json:"projectName,omitempty"`
	ProjectRoot    string `json:"projectRoot"`
	SrcRoot        string `json:"srcRoot"`
	FilesAnalyzed  int    `json:"filesAnalyzed"`
	SrcFiles       int    `json:"srcFiles"`
	OrphanFiles    int    `json:"orphanFiles"`
	TotalImports   int    `json:"totalImports"`
	PackageImports int    `json:"packageImports"`
	LocalImports   int    `json:"localImports"`
	StyleImports   int    `json:"styleImports"`
	AssetImports   int    `json:"assetImports"`
	UniquePackages int    `json:"uniquePackages"`
	Cycles         int    `json:"cycles"`
	UnusedDeps     int    `json:"unusedDependencies"`
	UndeclaredDeps int    `json:"undeclaredDependencies"`
	Warnings       int    `json:"warnings"`
	Insights       int    `json:"insights"`
	UnusedExports  int    `json:"unusedExports"`
	RuleViolations int    `json:"ruleViolations"`
	GeneratedAt    string `json:"generatedAt"`
	DurationMillis int64  `json:"durationMs"`
}

// Report is the full analysis result, shared by the CLI, exports and web UI.
type Report struct {
	Summary        Summary         `json:"summary"`
	Packages       []PackageStat   `json:"packages"`
	InternalFiles  []FileStat      `json:"internalFiles"`
	Orphans        []string        `json:"orphans"`
	Cycles         [][]string      `json:"cycles"`
	UnusedDeps     []string        `json:"unusedDependencies"`
	UndeclaredDeps []string        `json:"undeclaredDependencies"`
	Warnings       []Warning       `json:"warnings"`
	Insights       []Insight       `json:"insights"`
	UnusedExports  []ExportStat    `json:"unusedExports"`
	RuleViolations []RuleViolation `json:"ruleViolations"`
	Assets         []PackageStat   `json:"assets"` // styles/assets grouped by extension
}

// Analyze turns a walked graph into a report.
func Analyze(g *graph.Graph, p *project.Info) *Report {
	rep := &Report{}
	rel := p.Rel

	pkgs := map[string]*PackageStat{}
	pkgFiles := map[string]map[string]bool{}
	pkgSymbols := map[string]map[string]int{}
	pkgSubpaths := map[string]map[string]bool{}

	fileStats := map[string]*FileStat{}
	fanInFiles := map[string]map[string]bool{}
	fanOutTargets := map[string]map[string]bool{}
	adj := map[string][]string{}

	extStats := map[string]*PackageStat{}

	getFile := func(path string) *FileStat {
		key := rel(path)
		fs, ok := fileStats[key]
		if !ok {
			fs = &FileStat{File: key}
			fileStats[key] = fs
			fanInFiles[key] = map[string]bool{}
			fanOutTargets[key] = map[string]bool{}
		}
		return fs
	}
	for _, f := range g.Files {
		getFile(f)
	}

	for _, e := range g.Edges {
		fromRel := rel(e.From)
		rep.Summary.TotalImports++
		site := ImportSite{File: fromRel, Line: e.Line, Kind: string(e.Kind), Spec: e.Specifier, Symbols: e.Symbols}

		switch e.Res.Type {
		case resolver.TypePackage, resolver.TypeBuiltin:
			rep.Summary.PackageImports++
			name := e.Res.Package
			ps, ok := pkgs[name]
			if !ok {
				ps = &PackageStat{Name: name, Kinds: map[string]int{}}
				pkgs[name] = ps
				pkgFiles[name] = map[string]bool{}
				pkgSymbols[name] = map[string]int{}
				pkgSubpaths[name] = map[string]bool{}
			}
			ps.Imports++
			ps.Kinds[string(e.Kind)]++
			ps.Sites = append(ps.Sites, site)
			pkgFiles[name][fromRel] = true
			if e.Res.Subpath != "" {
				pkgSubpaths[name][e.Res.Subpath] = true
			}
			for _, s := range e.Symbols {
				pkgSymbols[name][s]++
			}
			if e.Res.Type == resolver.TypeBuiltin {
				ps.DepType = "builtin"
			}

		case resolver.TypeLocal:
			rep.Summary.LocalImports++
			target := rel(e.Res.Path)
			ts := getFile(e.Res.Path)
			ts.Imports++
			ts.Sites = append(ts.Sites, site)
			fanInFiles[target][fromRel] = true
			getFile(e.From)
			fanOutTargets[fromRel][target] = true
			adj[fromRel] = append(adj[fromRel], target)

		case resolver.TypeStyle, resolver.TypeAsset, resolver.TypeJSONFile:
			if e.Res.Type == resolver.TypeStyle {
				rep.Summary.StyleImports++
			} else {
				rep.Summary.AssetImports++
			}
			ext := strings.ToLower(filepath.Ext(e.Res.Path))
			es, ok := extStats[ext]
			if !ok {
				es = &PackageStat{Name: ext, DepType: string(e.Res.Type), Kinds: map[string]int{}}
				extStats[ext] = es
			}
			es.Imports++
			es.Kinds[string(e.Kind)]++
			es.Sites = append(es.Sites, site)

		case resolver.TypeMissing:
			rep.Warnings = append(rep.Warnings, Warning{
				File: fromRel, Line: e.Line, Spec: e.Specifier, Kind: string(e.Kind),
				Type: "missing", Hint: missingHint(e.Specifier),
			})
		case resolver.TypeDynamic:
			rep.Warnings = append(rep.Warnings, Warning{
				File: fromRel, Line: e.Line, Spec: e.Specifier, Kind: string(e.Kind),
				Type: "dynamic", Hint: "non-literal specifier; target unknown",
			})
		}
	}
	for _, skipped := range sortedUniqueRel(g.Skipped, rel) {
		rep.Warnings = append(rep.Warnings, Warning{
			File: skipped,
			Type: "unreadable",
			Hint: "file could not be read; imports from this file may be missing",
		})
	}
	for from, targets := range fanOutTargets {
		fileStats[from].FanOut = len(targets)
	}
	for from := range adj {
		sort.Strings(adj[from])
	}
	chains := shortestChains(rel(g.Entry), adj)

	// Finalise package stats.
	declared := map[string]string{}
	depType := map[string]string{}
	for n, v := range p.Deps {
		declared[n] = v
		depType[n] = "dependency"
	}
	for n, v := range p.DevDeps {
		if _, ok := declared[n]; !ok {
			declared[n] = v
			depType[n] = "devDependency"
		}
	}
	for n, v := range p.PeerDeps {
		if _, ok := declared[n]; !ok {
			declared[n] = v
			depType[n] = "peerDependency"
		}
	}
	for n, v := range p.OptDeps {
		if _, ok := declared[n]; !ok {
			declared[n] = v
			depType[n] = "optionalDependency"
		}
	}

	for name, ps := range pkgs {
		ps.Files = len(pkgFiles[name])
		ps.EntryChain = nearestChain(pkgFiles[name], chains)
		ps.Subpaths = sortedKeys(pkgSubpaths[name])
		ps.Symbols = sortedSymbols(pkgSymbols[name])
		sort.Slice(ps.Sites, func(i, j int) bool {
			if ps.Sites[i].File != ps.Sites[j].File {
				return ps.Sites[i].File < ps.Sites[j].File
			}
			if ps.Sites[i].Line != ps.Sites[j].Line {
				return ps.Sites[i].Line < ps.Sites[j].Line
			}
			if ps.Sites[i].Kind != ps.Sites[j].Kind {
				return ps.Sites[i].Kind < ps.Sites[j].Kind
			}
			return ps.Sites[i].Spec < ps.Sites[j].Spec
		})
		if ps.DepType == "builtin" {
			continue
		}
		if v, ok := declared[name]; ok {
			ps.Version = v
			ps.DepType = depType[name]
		} else {
			ps.DepType = "undeclared"
			rep.UndeclaredDeps = append(rep.UndeclaredDeps, name)
			if hint, ok := aliasHint(p.Root, name); ok {
				ps.DepType = "alias?"
				rep.Warnings = append(rep.Warnings, Warning{
					File: ps.Sites[0].File, Line: ps.Sites[0].Line, Spec: name,
					Kind: "resolution", Type: "alias", Hint: hint,
				})
			}
		}
		rep.Packages = append(rep.Packages, *ps)
	}
	for _, ps := range pkgs {
		if ps.DepType == "builtin" {
			rep.Packages = append(rep.Packages, *ps)
		}
	}
	sort.Slice(rep.Packages, func(i, j int) bool {
		if rep.Packages[i].Imports != rep.Packages[j].Imports {
			return rep.Packages[i].Imports > rep.Packages[j].Imports
		}
		return rep.Packages[i].Name < rep.Packages[j].Name
	})
	sort.Strings(rep.UndeclaredDeps)

	// Unused declared dependencies (runtime deps only; devDeps are noisy).
	used := map[string]bool{}
	for n := range pkgs {
		used[n] = true
	}
	for n := range p.Deps {
		if !used[n] {
			rep.UnusedDeps = append(rep.UnusedDeps, n)
		}
	}
	sort.Strings(rep.UnusedDeps)

	// React-component heuristic: JSX in the file, or a PascalCase named
	// export. Computed against g.HasJSX/g.Exports (absolute-path keyed).
	for _, path := range g.Files {
		key := rel(path)
		fs, ok := fileStats[key]
		if !ok {
			continue
		}
		if g.HasJSX[path] {
			fs.IsComponent = true
			continue
		}
		for _, ex := range g.Exports[path] {
			// Only trust a direct declaration's own name as a component
			// signal. "re-export"/"named" forward a binding declared
			// elsewhere in the file (or a barrel re-export) rather than
			// declaring one, and in practice that list-export form is just
			// as often a bag of PascalCase enum/constant objects
			// (`export { StatusColor, WorkOrderStatuses }`) as a component,
			// so it is excluded to keep this heuristic useful in practice.
			switch ex.Kind {
			case "function", "class", "const", "let", "var":
			default:
				continue
			}
			if isPascalCase(ex.Name) {
				fs.IsComponent = true
				break
			}
		}
	}

	// Internal file stats.
	for key, fs := range fileStats {
		fs.FanIn = len(fanInFiles[key])
		fs.EntryChain = chains[key]
		sort.Slice(fs.Sites, func(i, j int) bool {
			if fs.Sites[i].File != fs.Sites[j].File {
				return fs.Sites[i].File < fs.Sites[j].File
			}
			if fs.Sites[i].Line != fs.Sites[j].Line {
				return fs.Sites[i].Line < fs.Sites[j].Line
			}
			if fs.Sites[i].Kind != fs.Sites[j].Kind {
				return fs.Sites[i].Kind < fs.Sites[j].Kind
			}
			return fs.Sites[i].Spec < fs.Sites[j].Spec
		})
		rep.InternalFiles = append(rep.InternalFiles, *fs)
	}
	sort.Slice(rep.InternalFiles, func(i, j int) bool {
		if rep.InternalFiles[i].Imports != rep.InternalFiles[j].Imports {
			return rep.InternalFiles[i].Imports > rep.InternalFiles[j].Imports
		}
		return rep.InternalFiles[i].File < rep.InternalFiles[j].File
	})

	// Assets by extension.
	for _, es := range extStats {
		sort.Slice(es.Sites, func(i, j int) bool {
			if es.Sites[i].File != es.Sites[j].File {
				return es.Sites[i].File < es.Sites[j].File
			}
			if es.Sites[i].Line != es.Sites[j].Line {
				return es.Sites[i].Line < es.Sites[j].Line
			}
			if es.Sites[i].Kind != es.Sites[j].Kind {
				return es.Sites[i].Kind < es.Sites[j].Kind
			}
			return es.Sites[i].Spec < es.Sites[j].Spec
		})
		files := map[string]bool{}
		for _, s := range es.Sites {
			files[s.File] = true
		}
		es.Files = len(files)
		rep.Assets = append(rep.Assets, *es)
	}
	sort.Slice(rep.Assets, func(i, j int) bool {
		if rep.Assets[i].Imports != rep.Assets[j].Imports {
			return rep.Assets[i].Imports > rep.Assets[j].Imports
		}
		return rep.Assets[i].Name < rep.Assets[j].Name
	})

	rep.Cycles = findCycles(adj)
	for _, o := range g.Orphans {
		rep.Orphans = append(rep.Orphans, rel(o))
	}
	sort.Strings(rep.Orphans)
	sortWarnings(rep.Warnings)

	rep.Summary.Entry = rel(p.Entry)
	rep.Summary.ProjectName = p.Name
	rep.Summary.ProjectRoot = p.Root
	rep.Summary.SrcRoot = p.SrcRoot
	rep.Summary.FilesAnalyzed = len(g.Files)
	rep.Summary.SrcFiles = len(g.SrcFiles)
	rep.Summary.OrphanFiles = len(rep.Orphans)
	rep.Summary.UniquePackages = len(rep.Packages)
	rep.Summary.Cycles = len(rep.Cycles)
	rep.Summary.UnusedDeps = len(rep.UnusedDeps)
	rep.Summary.UndeclaredDeps = len(rep.UndeclaredDeps)
	rep.Summary.Warnings = len(rep.Warnings)

	rep.UnusedExports = unusedExports(g, rel)
	rep.Summary.UnusedExports = len(rep.UnusedExports)

	rep.Insights = BuildInsights(rep)
	rep.Summary.Insights = len(rep.Insights)
	return rep
}

// aliasHint reports whether an undeclared package name matches a directory or
// file in the project root, which usually means it is a build-tool alias
// (e.g. webpack `alias: { config: ... }`) rather than an npm package.
func aliasHint(root, name string) (string, bool) {
	if strings.Contains(name, "/") {
		return "", false
	}
	for _, cand := range []string{name, name + ".js", name + ".ts"} {
		p := filepath.Join(root, cand)
		if _, err := os.Stat(p); err == nil {
			return "\"" + name + "\" is not in package.json but " + cand +
				" exists in the project root — likely a build alias; re-run with --alias " + name + "=" + cand, true
		}
	}
	return "", false
}

func missingHint(spec string) string {
	if strings.HasPrefix(spec, ".") {
		return "relative path not found with .js/.jsx/.ts/.tsx or /index.*"
	}
	return "not found under src root; add --alias " + strings.Split(spec, "/")[0] + "=<path> if it is a build alias"
}

// shortestChains walks the local import graph breadth-first from the entry,
// recording for each reachable file the shortest chain of files leading to it.
// adj must already be sorted so the result is deterministic when several
// equally short chains exist.
func shortestChains(entry string, adj map[string][]string) map[string][]string {
	chains := map[string][]string{entry: {entry}}
	queue := []string{entry}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if _, seen := chains[next]; seen {
				continue
			}
			chain := make([]string, len(chains[cur]), len(chains[cur])+1)
			copy(chain, chains[cur])
			chains[next] = append(chain, next)
			queue = append(queue, next)
		}
	}
	return chains
}

// nearestChain returns the shortest entry chain among the given files, so a
// package can report the cheapest path explaining its presence. Ties are
// broken on the file name to keep the report deterministic.
func nearestChain(files map[string]bool, chains map[string][]string) []string {
	var best []string
	var bestFile string
	for f := range files {
		chain, ok := chains[f]
		if !ok {
			continue
		}
		if best == nil || len(chain) < len(best) || (len(chain) == len(best) && f < bestFile) {
			best, bestFile = chain, f
		}
	}
	return best
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSymbols(m map[string]int) []SymbolUse {
	out := make([]SymbolUse, 0, len(m))
	for k, v := range m {
		out = append(out, SymbolUse{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedUniqueRel(paths []string, rel func(string) string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		seen[rel(p)] = true
	}
	return sortedKeys(seen)
}

func sortWarnings(warnings []Warning) {
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].File != warnings[j].File {
			return warnings[i].File < warnings[j].File
		}
		if warnings[i].Line != warnings[j].Line {
			return warnings[i].Line < warnings[j].Line
		}
		if warnings[i].Type != warnings[j].Type {
			return warnings[i].Type < warnings[j].Type
		}
		if warnings[i].Spec != warnings[j].Spec {
			return warnings[i].Spec < warnings[j].Spec
		}
		return warnings[i].Kind < warnings[j].Kind
	})
}

// findCycles returns the strongly connected components with more than one
// member, plus self-loops, using Tarjan's algorithm.
func findCycles(adj map[string][]string) [][]string {
	index := 0
	idx := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string

	nodes := make([]string, 0, len(adj))
	for n := range adj {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	var strongConnect func(v string)
	strongConnect = func(v string) {
		idx[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range adj[v] {
			if _, seen := idx[w]; !seen {
				strongConnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if idx[w] < low[v] {
					low[v] = idx[w]
				}
			}
		}

		if low[v] == idx[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				sort.Strings(comp)
				out = append(out, comp)
			} else if len(comp) == 1 {
				for _, w := range adj[comp[0]] {
					if w == comp[0] {
						out = append(out, comp)
						break
					}
				}
			}
		}
	}

	for _, n := range nodes {
		if _, seen := idx[n]; !seen {
			strongConnect(n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i][0] < out[j][0]
	})
	return out
}

// KindLabel renders a scanner kind for display.
func KindLabel(k scanner.Kind) string { return string(k) }

// isPascalCase reports whether name looks like a React component identifier:
// starts with an uppercase ASCII letter followed by at least one lowercase
// letter, ruling out all-caps constants (e.g. API, CONFIG) and the literal
// "default" export sentinel.
func isPascalCase(name string) bool {
	if len(name) < 2 || name == "default" {
		return false
	}
	if name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	hasLower := false
	for i := 1; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_' || c == '$':
			// allowed but doesn't establish lower-case mix
		default:
			return false
		}
	}
	return hasLower
}
