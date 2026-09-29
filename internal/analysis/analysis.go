// Package analysis wires project detection, the graph walk and aggregation
// into a single reusable run so both the CLI and the dashboard's re-analyse
// action share one code path.
package analysis

import (
	"time"

	"importstats/internal/analyzer"
	"importstats/internal/graph"
	"importstats/internal/project"
	"importstats/internal/resolver"
	"importstats/internal/rules"
	"importstats/internal/weight"
)

// Options describe one analysis run.
type Options struct {
	Input   string            // entry file or directory
	SrcRoot string            // optional src-root override
	Aliases map[string]string // optional module aliases
	Ignore  []string          // ignore patterns
	Workers int               // parser workers (0 = default)
	NoSize  bool              // skip the node_modules size measurement
	Rules   []rules.Rule      // architecture constraints to enforce
}

// Run performs a full analysis. It re-reads the filesystem on every call, so
// repeated runs pick up source changes.
func Run(opts Options) (*analyzer.Report, error) {
	start := time.Now()

	p, err := project.Detect(opts.Input, opts.SrcRoot)
	if err != nil {
		return nil, err
	}
	if ts, tsErr := project.LoadTSPaths(p.Entry, p.Root); tsErr == nil && ts != nil {
		if opts.Aliases == nil {
			opts.Aliases = map[string]string{}
		}
		for k, v := range ts.AliasMap() {
			if _, exists := opts.Aliases[k]; !exists {
				opts.Aliases[k] = v
			}
		}
		if ws, wsErr := project.DetectWorkspace(p.Root); wsErr == nil && ws != nil {
			if opts.Aliases == nil {
				opts.Aliases = map[string]string{}
			}
			for _, pkg := range ws.Packages {
				if _, exists := opts.Aliases[pkg.Name]; !exists {
					opts.Aliases[pkg.Name] = pkg.Dir
				}
			}
		}
	}

	g, err := graph.Walk(graph.Options{
		Entry:   p.Entry,
		Src:     p.SrcRoot,
		Res:     resolver.New(p.Root, p.SrcRoot, opts.Aliases),
		Ignore:  opts.Ignore,
		Workers: opts.Workers,
	})
	if err != nil {
		return nil, err
	}

	rep := analyzer.Analyze(g, p)
	if !opts.NoSize {
		addWeights(rep, p.Root, opts.Workers)
	}
	checkRules(rep, g, p, opts.Rules)
	rep.Summary.GeneratedAt = time.Now().Format(time.RFC3339)
	rep.Summary.DurationMillis = time.Since(start).Milliseconds()
	return rep, nil
}

// addWeights enriches package stats with their installed size. It is
// best-effort: a project without node_modules simply keeps zero sizes.
func addWeights(rep *analyzer.Report, root string, workers int) {
	names := make([]string, 0, len(rep.Packages))
	for _, ps := range rep.Packages {
		if ps.DepType != "builtin" {
			names = append(names, ps.Name)
		}
	}
	sizes := weight.Measure(root, names, workers)
	shakes := weight.Shake(root, names, workers)
	for i := range rep.Packages {
		if info, ok := sizes[rep.Packages[i].Name]; ok {
			rep.Packages[i].SizeBytes = info.SizeBytes
			rep.Packages[i].FileCount = info.FileCount
			rep.Packages[i].TransitiveDeps = info.TransitiveDeps
		}
		if info, ok := shakes[rep.Packages[i].Name]; ok {
			rep.Packages[i].Shake = info
		}
	}
}

// checkRules evaluates architecture constraints over the local import edges.
func checkRules(rep *analyzer.Report, g *graph.Graph, p *project.Info, rs []rules.Rule) {
	if len(rs) == 0 {
		return
	}
	edges := make([]rules.Edge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if e.Res.Type != resolver.TypeLocal {
			continue
		}
		edges = append(edges, rules.Edge{From: p.Rel(e.From), To: p.Rel(e.Res.Path), Line: e.Line})
	}
	for _, v := range rules.Check(rs, edges) {
		rep.RuleViolations = append(rep.RuleViolations, analyzer.RuleViolation{
			Rule: v.Rule, From: v.From, To: v.To, Line: v.Line,
		})
	}
	rep.Summary.RuleViolations = len(rep.RuleViolations)
}
