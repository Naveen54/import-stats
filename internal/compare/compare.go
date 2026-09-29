// Package compare contrasts several analysed projects so sibling apps can be
// checked for dependency drift.
package compare

import (
	"sort"
	"time"

	"importstats/internal/analyzer"
)

// Project is one analysed app in the comparison.
type Project struct {
	Name     string `json:"name"`
	Entry    string `json:"entry"`
	Root     string `json:"root"`
	Files    int    `json:"files"`
	Imports  int    `json:"imports"`
	Packages int    `json:"packages"`
	Error    string `json:"error,omitempty"`
}

// Cell is one package's usage within one project.
type Cell struct {
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"`
	Imports int    `json:"imports"`
	Files   int    `json:"files"`
}

// Row is a single package across every project.
type Row struct {
	Package  string          `json:"package"`
	Cells    map[string]Cell `json:"cells"`
	Used     int             `json:"used"`     // projects importing it
	Imports  int             `json:"imports"`  // total across projects
	Drift    bool            `json:"drift"`    // declared versions disagree
	Versions []string        `json:"versions"` // distinct declared versions
}

// Report is the full comparison.
type Report struct {
	GeneratedAt string    `json:"generatedAt"`
	Projects    []Project `json:"projects"`
	Rows        []Row     `json:"rows"`
	DriftCount  int       `json:"driftCount"`
	SharedCount int       `json:"sharedCount"` // used by every project
	UniqueCount int       `json:"uniqueCount"` // used by exactly one
}

// Input pairs a project label with its analysis outcome.
type Input struct {
	Name   string
	Root   string
	Report *analyzer.Report
	Err    error
}

// Build contrasts the given projects. Projects that failed to analyse are
// still listed, carrying their error, so one broken app does not hide the
// rest of the comparison.
func Build(inputs []Input) *Report {
	rep := &Report{GeneratedAt: time.Now().Format(time.RFC3339)}

	rows := map[string]*Row{}
	var ok []string

	for _, in := range inputs {
		p := Project{Name: in.Name, Root: in.Root}
		switch {
		case in.Err != nil:
			p.Error = in.Err.Error()
			rep.Projects = append(rep.Projects, p)
			continue
		case in.Report == nil:
			p.Error = "no report produced"
			rep.Projects = append(rep.Projects, p)
			continue
		}
		p.Entry = in.Report.Summary.Entry
		p.Files = in.Report.Summary.FilesAnalyzed
		p.Imports = in.Report.Summary.TotalImports
		p.Packages = in.Report.Summary.UniquePackages
		rep.Projects = append(rep.Projects, p)
		ok = append(ok, in.Name)

		for _, ps := range in.Report.Packages {
			if ps.DepType == "builtin" {
				continue
			}
			r, exists := rows[ps.Name]
			if !exists {
				r = &Row{Package: ps.Name, Cells: map[string]Cell{}}
				rows[ps.Name] = r
			}
			r.Cells[in.Name] = Cell{Present: true, Version: ps.Version, Imports: ps.Imports, Files: ps.Files}
			r.Used++
			r.Imports += ps.Imports
		}
	}

	sort.Slice(rep.Projects, func(i, j int) bool { return rep.Projects[i].Name < rep.Projects[j].Name })
	sort.Strings(ok)

	for _, r := range rows {
		seen := map[string]bool{}
		for _, name := range ok {
			// Only a declared version counts as drift evidence; an undeclared
			// package has no version to disagree about.
			if c, used := r.Cells[name]; used && c.Version != "" && !seen[c.Version] {
				seen[c.Version] = true
				r.Versions = append(r.Versions, c.Version)
			}
		}
		sort.Strings(r.Versions)
		r.Drift = len(r.Versions) > 1
		if r.Drift {
			rep.DriftCount++
		}
		if len(ok) > 0 && r.Used == len(ok) {
			rep.SharedCount++
		}
		if r.Used == 1 {
			rep.UniqueCount++
		}
		rep.Rows = append(rep.Rows, *r)
	}

	// Drift first, then the most widely used, so actionable rows lead.
	sort.Slice(rep.Rows, func(i, j int) bool {
		a, b := rep.Rows[i], rep.Rows[j]
		if a.Drift != b.Drift {
			return a.Drift
		}
		if a.Used != b.Used {
			return a.Used > b.Used
		}
		if a.Imports != b.Imports {
			return a.Imports > b.Imports
		}
		return a.Package < b.Package
	})
	return rep
}
