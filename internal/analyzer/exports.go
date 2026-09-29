package analyzer

import (
	"sort"
	"strings"

	"importstats/internal/graph"
	"importstats/internal/resolver"
	"importstats/internal/scanner"
)

// unusedExports reports exported bindings that no reachable file imports.
//
// It is deliberately conservative: anything that could route a name to a
// consumer indirectly (a namespace import, a star re-export, a barrel) makes
// the whole file opt out, because a false positive here would have someone
// delete live code. Results are therefore "possibly unused", never certain.
func unusedExports(g *graph.Graph, rel func(string) string) []ExportStat {
	consumed := map[string]map[string]bool{}
	opaque := map[string]bool{}

	mark := func(target, name string) {
		if consumed[target] == nil {
			consumed[target] = map[string]bool{}
		}
		consumed[target][name] = true
	}

	for _, e := range g.Edges {
		if e.Res.Type != resolver.TypeLocal {
			continue
		}
		target := e.Res.Path
		// A re-export forwards names we cannot follow to their final consumer.
		if e.Kind == scanner.KindExportFrom {
			opaque[target] = true
			continue
		}
		for _, sym := range e.Symbols {
			switch {
			case strings.HasPrefix(sym, "*:"):
				// import * as ns — any property may be touched at runtime.
				opaque[target] = true
			case strings.HasPrefix(sym, "default:"):
				mark(target, "default")
			default:
				// "b as c" is imported under the exported name b.
				if i := strings.Index(sym, " as "); i >= 0 {
					mark(target, strings.TrimSpace(sym[:i]))
				} else {
					mark(target, sym)
				}
			}
		}
	}

	var out []ExportStat
	for file, exports := range g.Exports {
		if file == g.Entry || opaque[file] {
			continue
		}
		for _, ex := range exports {
			// The sentinel for a wholesale `module.exports = expr` carries no
			// usable name, so it can never be judged unused.
			if ex.Name == "" || ex.Name == "module.exports" {
				continue
			}
			if consumed[file][ex.Name] {
				continue
			}
			out = append(out, ExportStat{
				File:   rel(file),
				Name:   ex.Name,
				Line:   ex.Line,
				Kind:   ex.Kind,
				Reason: "no file reachable from the entry imports this name; importers that are themselves orphans do not count",
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Name < out[j].Name
	})
	return out
}
