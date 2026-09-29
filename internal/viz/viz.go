// Package viz renders analyzer reports as visual module graphs.
package viz

import (
	"os"
	"sort"
	"strconv"
	"strings"

	"importstats/internal/analyzer"
)

// Options control how the graph is rendered.
type Options struct {
	// Collapse renders one node per top-level directory instead of one per
	// file, which keeps large projects legible.
	Collapse bool
	// IncludePackages adds npm packages as leaf nodes.
	IncludePackages bool
	// MaxNodes caps the output; the highest fan-in nodes win. 0 = no cap.
	MaxNodes int
}

type nodeKind string

const (
	fileNode    nodeKind = "file"
	packageNode nodeKind = "package"
)

type node struct {
	id     string
	label  string
	kind   nodeKind
	fanIn  int
	entry  bool
	serial int
}

type edge struct {
	from  string
	to    string
	count int
}

type graph struct {
	nodes []node
	edges []edge
}

// DOT renders the module graph in Graphviz DOT format.
func DOT(rep *analyzer.Report, opts Options) string {
	g := build(rep, opts)

	var b strings.Builder
	b.WriteString("digraph importgraph {\n")
	b.WriteString("  rankdir=LR;\n")
	for _, n := range g.nodes {
		attrs := []string{"label=" + dotQuote(n.label)}
		if n.entry {
			attrs = append(attrs, `shape="box"`, `style="filled"`, `fillcolor="#dbeafe"`)
		} else if n.kind == packageNode {
			attrs = append(attrs, `shape="component"`, `style="filled"`, `fillcolor="#fef3c7"`)
		}
		b.WriteString("  ")
		b.WriteString(dotQuote(n.id))
		b.WriteString(" [")
		b.WriteString(strings.Join(attrs, ", "))
		b.WriteString("];\n")
	}
	for _, e := range g.edges {
		b.WriteString("  ")
		b.WriteString(dotQuote(e.from))
		b.WriteString(" -> ")
		b.WriteString(dotQuote(e.to))
		if opts.Collapse {
			b.WriteString(" [label=")
			b.WriteString(dotQuote(strconv.Itoa(e.count)))
			b.WriteString("]")
		}
		b.WriteString(";\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// Mermaid renders the module graph as a Mermaid flowchart.
func Mermaid(rep *analyzer.Report, opts Options) string {
	g := build(rep, opts)
	ids := make(map[string]string, len(g.nodes))
	for i, n := range g.nodes {
		ids[n.id] = "n" + strconv.Itoa(i)
	}

	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for _, n := range g.nodes {
		id := ids[n.id]
		b.WriteString("  ")
		b.WriteString(id)
		b.WriteString("[\"")
		b.WriteString(mermaidLabel(n.label))
		b.WriteString("\"]\n")
	}
	if len(g.nodes) > 0 {
		b.WriteString("  classDef entry fill:#dbeafe,stroke:#2563eb,stroke-width:2px;\n")
		b.WriteString("  classDef package fill:#fef3c7,stroke:#d97706;\n")
	}
	for _, e := range g.edges {
		b.WriteString("  ")
		b.WriteString(ids[e.from])
		if opts.Collapse {
			b.WriteString(" -- \"")
			b.WriteString(strconv.Itoa(e.count))
			b.WriteString("\" --> ")
		} else {
			b.WriteString(" --> ")
		}
		b.WriteString(ids[e.to])
		b.WriteString("\n")
	}
	for _, n := range g.nodes {
		switch {
		case n.entry:
			b.WriteString("  class ")
			b.WriteString(ids[n.id])
			b.WriteString(" entry\n")
		case n.kind == packageNode:
			b.WriteString("  class ")
			b.WriteString(ids[n.id])
			b.WriteString(" package\n")
		}
	}
	return b.String()
}

// WriteDOT writes the Graphviz DOT rendering to path.
func WriteDOT(rep *analyzer.Report, opts Options, path string) error {
	return os.WriteFile(path, []byte(DOT(rep, opts)), 0o644)
}

// WriteMermaid writes the Mermaid flowchart rendering to path.
func WriteMermaid(rep *analyzer.Report, opts Options, path string) error {
	return os.WriteFile(path, []byte(Mermaid(rep, opts)), 0o644)
}

func build(rep *analyzer.Report, opts Options) graph {
	if rep == nil {
		rep = &analyzer.Report{}
	}

	nodes := map[string]*node{}
	edges := map[string]*edge{}
	seq := 0

	addNode := func(id, label string, kind nodeKind, fanIn int, entry bool) {
		if id == "" {
			return
		}
		n, ok := nodes[id]
		if !ok {
			n = &node{id: id, label: label, kind: kind, serial: seq}
			nodes[id] = n
			seq++
		}
		if kind == packageNode {
			n.kind = packageNode
			n.label = label
		}
		n.fanIn += fanIn
		n.entry = n.entry || entry
	}

	fileID := func(path string) string {
		if opts.Collapse {
			return collapsePath(path)
		}
		return path
	}
	addEdge := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		key := from + "\x00" + to
		e, ok := edges[key]
		if !ok {
			e = &edge{from: from, to: to}
			edges[key] = e
		}
		e.count++
	}

	for _, fs := range rep.InternalFiles {
		id := fileID(fs.File)
		addNode(id, id, fileNode, fs.FanIn, id == fileID(rep.Summary.Entry))
		for _, site := range fs.Sites {
			from := fileID(site.File)
			to := id
			addNode(from, from, fileNode, 0, from == fileID(rep.Summary.Entry))
			addEdge(from, to)
		}
	}
	if rep.Summary.Entry != "" {
		entry := fileID(rep.Summary.Entry)
		addNode(entry, entry, fileNode, 0, true)
	}

	if opts.IncludePackages {
		for _, ps := range rep.Packages {
			id := "pkg:" + ps.Name
			fanIn := ps.Files
			if fanIn == 0 {
				fanIn = distinctSiteFiles(ps.Sites)
			}
			addNode(id, ps.Name, packageNode, fanIn, false)
			for _, site := range ps.Sites {
				from := fileID(site.File)
				addNode(from, from, fileNode, 0, from == fileID(rep.Summary.Entry))
				addEdge(from, id)
			}
		}
	}

	if opts.MaxNodes > 0 && len(nodes) > opts.MaxNodes {
		keep := cappedNodes(nodes, opts.MaxNodes)
		for id := range nodes {
			if !keep[id] {
				delete(nodes, id)
			}
		}
		for key, e := range edges {
			if nodes[e.from] == nil || nodes[e.to] == nil {
				delete(edges, key)
			}
		}
	}

	return graph{nodes: sortedNodes(nodes), edges: sortedEdges(edges)}
}

// collapsePath groups files by the first two path segments. React projects
// commonly place source files under "src/<area>/...", so depth two yields
// nodes such as "src/app" while keeping diagrams small.
func collapsePath(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		if strings.Contains(parts[1], ".") {
			return parts[0]
		}
		return parts[0] + "/" + parts[1]
	default:
		return parts[0] + "/" + parts[1]
	}
}

func distinctSiteFiles(sites []analyzer.ImportSite) int {
	seen := map[string]bool{}
	for _, site := range sites {
		if site.File != "" {
			seen[site.File] = true
		}
	}
	return len(seen)
}

func cappedNodes(nodes map[string]*node, max int) map[string]bool {
	list := sortedNodes(nodes)
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].fanIn != list[j].fanIn {
			return list[i].fanIn > list[j].fanIn
		}
		return list[i].id < list[j].id
	})
	keep := map[string]bool{}
	for i := 0; i < max && i < len(list); i++ {
		keep[list[i].id] = true
	}
	return keep
}

func sortedNodes(nodes map[string]*node) []node {
	out := make([]node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].id < out[j].id
	})
	return out
}

func sortedEdges(edges map[string]*edge) []edge {
	out := make([]edge, 0, len(edges))
	for _, e := range edges {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].from != out[j].from {
			return out[i].from < out[j].from
		}
		return out[i].to < out[j].to
	})
	return out
}

func dotQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func mermaidLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
