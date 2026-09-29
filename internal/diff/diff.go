// Package diff compares saved analyzer reports.
package diff

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"importstats/internal/analyzer"
)

// Load reads a previously saved report from disk.
func Load(path string) (*analyzer.Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep analyzer.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}

// PackageChange is one package that appeared, vanished, or changed.
type PackageChange struct {
	Name           string `json:"name"`
	Before         int    `json:"before"`
	After          int    `json:"after"`
	DeltaImports   int    `json:"deltaImports"`
	BeforeVersion  string `json:"beforeVersion,omitempty"`
	AfterVersion   string `json:"afterVersion,omitempty"`
	DeltaSizeBytes int64  `json:"deltaSizeBytes,omitempty"`
}

// Diff is the comparison of a new report against a baseline.
type Diff struct {
	BaselineGeneratedAt string `json:"baselineGeneratedAt"`
	CurrentGeneratedAt  string `json:"currentGeneratedAt"`

	AddedPackages   []PackageChange `json:"addedPackages"`
	RemovedPackages []PackageChange `json:"removedPackages"`
	ChangedPackages []PackageChange `json:"changedPackages"`

	NewCycles       [][]string               `json:"newCycles"`
	ResolvedCycles  [][]string               `json:"resolvedCycles"`
	NewUndeclared   []string                 `json:"newUndeclared"`
	NewUnusedDeps   []string                 `json:"newUnusedDeps"`
	NewOrphans      []string                 `json:"newOrphans"`
	ResolvedOrphans []string                 `json:"resolvedOrphans"`
	NewViolations   []analyzer.RuleViolation `json:"newViolations"`

	SummaryDeltas map[string]int `json:"summaryDeltas"`
}

// Compute compares current against baseline.
func Compute(baseline, current *analyzer.Report) *Diff {
	b := reportOrEmpty(baseline)
	c := reportOrEmpty(current)
	d := &Diff{
		BaselineGeneratedAt: b.Summary.GeneratedAt,
		CurrentGeneratedAt:  c.Summary.GeneratedAt,
		SummaryDeltas:       summaryDeltas(b.Summary, c.Summary),
	}

	d.AddedPackages, d.RemovedPackages, d.ChangedPackages = packageChanges(b.Packages, c.Packages)
	d.NewCycles, d.ResolvedCycles = cycleChanges(b.Cycles, c.Cycles)
	d.NewUndeclared = addedStrings(b.UndeclaredDeps, c.UndeclaredDeps)
	d.NewUnusedDeps = addedStrings(b.UnusedDeps, c.UnusedDeps)
	d.NewOrphans, d.ResolvedOrphans = stringChanges(b.Orphans, c.Orphans)
	d.NewViolations = newViolations(b.RuleViolations, c.RuleViolations)
	return d
}

// Check reports which of the requested gate names are currently failing.
func Check(d *Diff, gates []string) (failed []string, err error) {
	if d == nil {
		d = &Diff{SummaryDeltas: map[string]int{}}
	}
	valid := []string{"new-cycles", "new-undeclared", "new-orphans", "rules", "imports-up", "packages-up"}
	validSet := make(map[string]bool, len(valid))
	for _, gate := range valid {
		validSet[gate] = true
	}
	for _, gate := range gates {
		if !validSet[gate] {
			return nil, fmt.Errorf("unknown diff gate %q (valid gates: %s)", gate, strings.Join(valid, ", "))
		}
		switch gate {
		case "new-cycles":
			if len(d.NewCycles) > 0 {
				failed = append(failed, gate)
			}
		case "new-undeclared":
			if len(d.NewUndeclared) > 0 {
				failed = append(failed, gate)
			}
		case "new-orphans":
			if len(d.NewOrphans) > 0 {
				failed = append(failed, gate)
			}
		case "rules":
			if len(d.NewViolations) > 0 {
				failed = append(failed, gate)
			}
		case "imports-up":
			if d.SummaryDeltas["totalImports"] > 0 {
				failed = append(failed, gate)
			}
		case "packages-up":
			if d.SummaryDeltas["uniquePackages"] > 0 {
				failed = append(failed, gate)
			}
		}
	}
	return failed, nil
}

// WriteText renders a human-readable diff for the terminal.
func WriteText(w io.Writer, d *Diff) {
	if d == nil {
		d = &Diff{SummaryDeltas: map[string]int{}}
	}
	fmt.Fprintln(w, "Baseline diff")
	fmt.Fprintf(w, "Baseline generated  %s\n", blank(d.BaselineGeneratedAt))
	fmt.Fprintf(w, "Current generated   %s\n\n", blank(d.CurrentGeneratedAt))

	if diffEmpty(d) {
		fmt.Fprintln(w, "No changes.")
		return
	}

	writePackageChanges(w, "Added packages", "+", d.AddedPackages)
	writePackageChanges(w, "Removed packages", "-", d.RemovedPackages)
	writePackageChanges(w, "Changed packages", "~", d.ChangedPackages)
	writeCycles(w, "New cycles", "+", d.NewCycles)
	writeCycles(w, "Resolved cycles", "-", d.ResolvedCycles)
	writeStrings(w, "New undeclared dependencies", "+", d.NewUndeclared)
	writeStrings(w, "New unused dependencies", "+", d.NewUnusedDeps)
	writeStrings(w, "New orphans", "+", d.NewOrphans)
	writeStrings(w, "Resolved orphans", "-", d.ResolvedOrphans)
	writeViolations(w, d.NewViolations)
	writeSummaryDeltas(w, d.SummaryDeltas)
}

type packageSnapshot struct {
	imports   int
	version   string
	sizeBytes int64
}

func reportOrEmpty(rep *analyzer.Report) *analyzer.Report {
	if rep == nil {
		return &analyzer.Report{}
	}
	return rep
}

func packageChanges(before, after []analyzer.PackageStat) (added, removed, changed []PackageChange) {
	b := packageMap(before)
	c := packageMap(after)
	names := unionKeys(b, c)
	for _, name := range names {
		bp, bok := b[name]
		cp, cok := c[name]
		switch {
		case !bok:
			added = append(added, PackageChange{Name: name, After: cp.imports, DeltaImports: cp.imports, AfterVersion: cp.version, DeltaSizeBytes: cp.sizeBytes})
		case !cok:
			removed = append(removed, PackageChange{Name: name, Before: bp.imports, DeltaImports: -bp.imports, BeforeVersion: bp.version, DeltaSizeBytes: -bp.sizeBytes})
		case bp.imports != cp.imports || bp.version != cp.version || bp.sizeBytes != cp.sizeBytes:
			changed = append(changed, PackageChange{
				Name:           name,
				Before:         bp.imports,
				After:          cp.imports,
				DeltaImports:   cp.imports - bp.imports,
				BeforeVersion:  bp.version,
				AfterVersion:   cp.version,
				DeltaSizeBytes: cp.sizeBytes - bp.sizeBytes,
			})
		}
	}
	return added, removed, changed
}

func packageMap(pkgs []analyzer.PackageStat) map[string]packageSnapshot {
	sorted := append([]analyzer.PackageStat(nil), pkgs...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		if sorted[i].Version != sorted[j].Version {
			return sorted[i].Version < sorted[j].Version
		}
		return sorted[i].Imports < sorted[j].Imports
	})
	out := make(map[string]packageSnapshot, len(sorted))
	for _, p := range sorted {
		s := out[p.Name]
		s.imports += p.Imports
		s.sizeBytes += p.SizeBytes
		if s.version == "" {
			s.version = p.Version
		}
		out[p.Name] = s
	}
	return out
}

func cycleChanges(before, after [][]string) (newCycles, resolved [][]string) {
	b := cycleMap(before)
	c := cycleMap(after)
	for _, key := range sortedMissingKeys(c, b) {
		newCycles = append(newCycles, append([]string(nil), c[key]...))
	}
	for _, key := range sortedMissingKeys(b, c) {
		resolved = append(resolved, append([]string(nil), b[key]...))
	}
	return newCycles, resolved
}

func cycleMap(cycles [][]string) map[string][]string {
	out := make(map[string][]string, len(cycles))
	for _, cycle := range cycles {
		norm := append([]string(nil), cycle...)
		sort.Strings(norm)
		out[cycleKey(norm)] = norm
	}
	return out
}

func cycleKey(cycle []string) string {
	return strings.Join(cycle, "\x00")
}

func stringChanges(before, after []string) (added, removed []string) {
	return addedStrings(before, after), addedStrings(after, before)
}

func addedStrings(before, after []string) []string {
	b := stringSet(before)
	c := stringSet(after)
	out := sortedMissingKeys(c, b)
	return out
}

func stringSet(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, value := range values {
		out[value] = value
	}
	return out
}

func newViolations(before, after []analyzer.RuleViolation) []analyzer.RuleViolation {
	b := violationSet(before)
	var out []analyzer.RuleViolation
	for _, v := range after {
		if !b[violationKey(v)] {
			out = append(out, v)
		}
	}
	sortViolations(out)
	return out
}

func violationSet(values []analyzer.RuleViolation) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[violationKey(v)] = true
	}
	return out
}

func violationKey(v analyzer.RuleViolation) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d", v.From, v.To, v.Rule, v.Line)
}

func sortViolations(values []analyzer.RuleViolation) {
	sort.Slice(values, func(i, j int) bool {
		a, b := values[i], values[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Rule < b.Rule
	})
}

func summaryDeltas(before, after analyzer.Summary) map[string]int {
	checks := []struct {
		key           string
		before, after int
	}{
		{"filesAnalyzed", before.FilesAnalyzed, after.FilesAnalyzed},
		{"totalImports", before.TotalImports, after.TotalImports},
		{"packageImports", before.PackageImports, after.PackageImports},
		{"localImports", before.LocalImports, after.LocalImports},
		{"uniquePackages", before.UniquePackages, after.UniquePackages},
		{"orphanFiles", before.OrphanFiles, after.OrphanFiles},
		{"cycles", before.Cycles, after.Cycles},
		{"undeclaredDependencies", before.UndeclaredDeps, after.UndeclaredDeps},
		{"unusedDependencies", before.UnusedDeps, after.UnusedDeps},
		{"warnings", before.Warnings, after.Warnings},
		{"unusedExports", before.UnusedExports, after.UnusedExports},
		{"ruleViolations", before.RuleViolations, after.RuleViolations},
	}
	out := make(map[string]int)
	for _, check := range checks {
		if delta := check.after - check.before; delta != 0 {
			out[check.key] = delta
		}
	}
	return out
}

func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for key := range a {
		seen[key] = true
	}
	for key := range b {
		seen[key] = true
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMissingKeys[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a))
	for key := range a {
		if _, ok := b[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func diffEmpty(d *Diff) bool {
	return len(d.AddedPackages) == 0 &&
		len(d.RemovedPackages) == 0 &&
		len(d.ChangedPackages) == 0 &&
		len(d.NewCycles) == 0 &&
		len(d.ResolvedCycles) == 0 &&
		len(d.NewUndeclared) == 0 &&
		len(d.NewUnusedDeps) == 0 &&
		len(d.NewOrphans) == 0 &&
		len(d.ResolvedOrphans) == 0 &&
		len(d.NewViolations) == 0 &&
		len(d.SummaryDeltas) == 0
}

func blank(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func writePackageChanges(w io.Writer, title, prefix string, changes []PackageChange) {
	if len(changes) == 0 {
		return
	}
	fmt.Fprintf(w, "%s:\n", title)
	for _, c := range changes {
		version := ""
		switch {
		case c.BeforeVersion != "" && c.AfterVersion != "" && c.BeforeVersion != c.AfterVersion:
			version = fmt.Sprintf("  %s -> %s", c.BeforeVersion, c.AfterVersion)
		case c.AfterVersion != "":
			version = "  " + c.AfterVersion
		case c.BeforeVersion != "":
			version = "  " + c.BeforeVersion
		}
		size := ""
		if c.DeltaSizeBytes != 0 {
			size = fmt.Sprintf("  size %+d", c.DeltaSizeBytes)
		}
		fmt.Fprintf(w, "  %s %-32s %4d -> %4d  (%+d imports)%s%s\n", prefix, c.Name, c.Before, c.After, c.DeltaImports, version, size)
	}
	fmt.Fprintln(w)
}

func writeCycles(w io.Writer, title, prefix string, cycles [][]string) {
	if len(cycles) == 0 {
		return
	}
	fmt.Fprintf(w, "%s:\n", title)
	for _, cycle := range cycles {
		fmt.Fprintf(w, "  %s %s\n", prefix, strings.Join(cycle, " -> "))
	}
	fmt.Fprintln(w)
}

func writeStrings(w io.Writer, title, prefix string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(w, "%s:\n", title)
	for _, value := range values {
		fmt.Fprintf(w, "  %s %s\n", prefix, value)
	}
	fmt.Fprintln(w)
}

func writeViolations(w io.Writer, violations []analyzer.RuleViolation) {
	if len(violations) == 0 {
		return
	}
	fmt.Fprintln(w, "New rule violations:")
	for _, v := range violations {
		fmt.Fprintf(w, "  + %-24s %s:%d -> %s\n", v.Rule, v.From, v.Line, v.To)
	}
	fmt.Fprintln(w)
}

func writeSummaryDeltas(w io.Writer, deltas map[string]int) {
	if len(deltas) == 0 {
		return
	}
	keys := make([]string, 0, len(deltas))
	for key := range deltas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "Summary deltas:")
	for _, key := range keys {
		fmt.Fprintf(w, "  %-24s %+d\n", key, deltas[key])
	}
	fmt.Fprintln(w)
}
