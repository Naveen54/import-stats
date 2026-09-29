// Package graph walks the module import graph starting from an entry file.
package graph

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"importstats/internal/resolver"
	"importstats/internal/scanner"
)

// Edge is one resolved import from a source file.
type Edge struct {
	From      string          // absolute path of the importing file
	Specifier string          // raw specifier as written
	Kind      scanner.Kind    // import/require/dynamic/...
	Symbols   []string        // imported bindings
	Line      int             // line of the statement
	Res       resolver.Result // resolution outcome
}

// Graph is the walk result.
type Graph struct {
	Entry    string
	Files    []string // reachable source files (absolute), sorted
	Edges    []Edge
	Ignored  []string // local import targets skipped by explicit ignore patterns
	Orphans  []string // source files under src not reachable from the entry
	Skipped  []string // files that could not be read
	SrcFiles []string // all source files under src root
	// Exports maps a reachable file to the bindings it exports under its own
	// name, which drives unused-export detection.
	Exports map[string][]scanner.Export
	// HasJSX maps a reachable file to whether the scanner found JSX syntax in
	// it; one of the signals used for component detection.
	HasJSX map[string]bool
}

// Options configure a walk.
type Options struct {
	Entry   string
	Src     string
	Res     *resolver.Resolver
	Ignore  []string // substring or basename-glob patterns applied to paths
	Workers int
}

type fileResult struct {
	path    string
	edges   []Edge
	exports []scanner.Export
	hasJSX  bool
	err     error
}

type canonicalInfo struct {
	path string
	info fs.FileInfo
}

type canonicalizer struct {
	paths map[string]string
	infos []canonicalInfo
}

// Walk performs a breadth-first traversal from the entry file, parsing each
// level concurrently.
func Walk(opts Options) (*Graph, error) {
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU() * 2
	}

	canon := newCanonicalizer()
	entry, _ := canon.path(opts.Entry)
	g := &Graph{Entry: entry, Exports: map[string][]scanner.Export{}, HasJSX: map[string]bool{}}
	visited := map[string]bool{entry: true}
	level := []string{entry}

	for len(level) > 0 {
		results := parseLevel(level, opts.Res, workers)
		var next []string
		for _, r := range results {
			if r.err != nil {
				g.Skipped = append(g.Skipped, r.path)
				if r.path == entry {
					return g, fmt.Errorf("read entry %s: %w", r.path, r.err)
				}
				continue
			}
			g.Files = append(g.Files, r.path)
			if len(r.exports) > 0 {
				g.Exports[r.path] = r.exports
			}
			if r.hasJSX {
				g.HasJSX[r.path] = true
			}
			edges := make([]Edge, 0, len(r.edges))
			for _, e := range r.edges {
				rawPath := e.Res.Path
				if e.Res.Type == resolver.TypeLocal && resolver.IsSourceFile(e.Res.Path) {
					e.Res.Path, _ = canon.path(e.Res.Path)
				}
				if e.Res.Type == resolver.TypeLocal && (explicitIgnored(rawPath, opts.Ignore) || explicitIgnored(e.Res.Path, opts.Ignore)) {
					g.Ignored = append(g.Ignored, e.Res.Path)
					continue
				}
				edges = append(edges, e)
				if e.Res.Type != resolver.TypeLocal || !resolver.IsSourceFile(e.Res.Path) {
					continue
				}
				if visited[e.Res.Path] || isIgnored(e.Res.Path, opts.Ignore) || tooDeep(e.Res.Path) {
					continue
				}
				visited[e.Res.Path] = true
				next = append(next, e.Res.Path)
			}
			g.Edges = append(g.Edges, edges...)
		}
		level = next
	}

	sort.Strings(g.Files)
	sort.Strings(g.Ignored)
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].Line < g.Edges[j].Line
	})

	g.SrcFiles = canonicalSourceFiles(listSourceFiles(opts.Src, opts.Ignore), canon)
	for _, f := range g.SrcFiles {
		if !visited[f] {
			g.Orphans = append(g.Orphans, f)
		}
	}
	sort.Strings(g.Orphans)
	return g, nil
}

func canonicalSourceFiles(files []string, canon *canonicalizer) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(files))
	for _, f := range files {
		cf, _ := canon.path(f)
		if seen[cf] {
			continue
		}
		seen[cf] = true
		out = append(out, cf)
	}
	sort.Strings(out)
	return out
}

func newCanonicalizer() *canonicalizer {
	return &canonicalizer{paths: map[string]string{}}
}

func (c *canonicalizer) path(path string) (string, error) {
	clean := filepath.Clean(path)
	if got, ok := c.paths[clean]; ok {
		return got, nil
	}
	eval, evalErr := filepath.EvalSymlinks(clean)
	if evalErr == nil {
		eval = filepath.Clean(eval)
		if got, ok := c.paths[eval]; ok {
			c.paths[clean] = got
			return got, nil
		}
	}
	statPath := clean
	if evalErr == nil {
		statPath = eval
	}
	info, statErr := os.Stat(statPath)
	if statErr != nil {
		info, statErr = os.Stat(clean)
	}
	if statErr == nil {
		for _, existing := range c.infos {
			if os.SameFile(info, existing.info) {
				c.paths[clean] = existing.path
				if evalErr == nil {
					c.paths[eval] = existing.path
				}
				return existing.path, nil
			}
		}
	}
	canon := clean
	if evalErr == nil {
		canon = eval
	}
	c.paths[clean] = canon
	if evalErr == nil {
		c.paths[eval] = canon
	}
	if statErr == nil {
		c.infos = append(c.infos, canonicalInfo{path: canon, info: info})
	}
	if evalErr != nil {
		return canon, evalErr
	}
	return canon, nil
}

func parseLevel(paths []string, res *resolver.Resolver, workers int) []fileResult {
	results := make([]fileResult, len(paths))
	if workers > len(paths) {
		workers = len(paths)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex // the resolver's stat cache is not concurrency-safe

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				path := paths[i]
				sr, err := scanner.ScanFileResult(path)
				if err != nil {
					results[i] = fileResult{path: path, err: err}
					continue
				}
				recs := sr.Records
				edges := make([]Edge, 0, len(recs))
				for _, rec := range recs {
					mu.Lock()
					r := res.Resolve(rec.Specifier, path)
					mu.Unlock()
					edges = append(edges, Edge{
						From:      path,
						Specifier: rec.Specifier,
						Kind:      rec.Kind,
						Symbols:   rec.Symbols,
						Line:      rec.Line,
						Res:       r,
					})
				}
				results[i] = fileResult{path: path, edges: edges, exports: sr.Exports, hasJSX: sr.HasJSX}
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func isIgnored(path string, patterns []string) bool {
	slash := filepath.ToSlash(path)
	if strings.Contains(slash, "/node_modules/") {
		return true
	}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.Contains(slash, p) {
			return true
		}
		if ok, _ := filepath.Match(p, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

func explicitIgnored(path string, patterns []string) bool {
	slash := filepath.ToSlash(path)
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.Contains(slash, p) {
			return true
		}
		if ok, _ := filepath.Match(p, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

func tooDeep(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	return len(parts) > 256
}

// listSourceFiles enumerates traversable source files under root.
func listSourceFiles(root string, ignore []string) []string {
	var out []string
	if root == "" {
		return out
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "dist", "build", "coverage":
				return filepath.SkipDir
			}
			return nil
		}
		if resolver.IsSourceFile(path) && !isIgnored(path, ignore) {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
