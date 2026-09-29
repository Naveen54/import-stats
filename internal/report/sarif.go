package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"importstats/internal/analyzer"
)

const (
	sarifRuleViolationID = "importstats/rule-violation"
	sarifCycleID         = "importstats/cycle"
	sarifUndeclaredDepID = "importstats/undeclared-dependency"
	sarifUnusedDepID     = "importstats/unused-dependency"
	sarifOrphanID        = "importstats/orphan-file"
	sarifInsightID       = "importstats/insight"
	importstatsInfoURI   = "https://github.com/github/importstats"
	sarifSchemaURI       = "https://json.schemastore.org/sarif-2.1.0.json"
	sarifVersion         = "2.1.0"
)

// WriteSARIF writes the report's findings as a SARIF 2.1.0 log.
func WriteSARIF(rep *analyzer.Report, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return writeSARIF(rep, f)
}

func writeSARIF(rep *analyzer.Report, f io.WriteCloser) error {
	out, renderErr := SARIF(rep)
	if renderErr != nil {
		return errors.Join(renderErr, f.Close())
	}
	_, writeErr := io.WriteString(f, out)
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

// SARIF renders the log and returns it.
func SARIF(rep *analyzer.Report) (string, error) {
	if rep == nil {
		rep = &analyzer.Report{}
	}

	log := sarifLog{
		Schema:  sarifSchemaURI,
		Version: sarifVersion,
		Runs: []sarifRun{
			{
				Tool: sarifTool{
					Driver: sarifDriver{
						Name:           "importstats",
						InformationURI: importstatsInfoURI,
						Rules:          sarifRules(),
					},
				},
				Results: sarifResults(rep),
			},
		},
	}

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		return "", err
	}
	return b.String(), nil
}

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string                     `json:"name"`
	InformationURI string                     `json:"informationUri"`
	Rules          []sarifReportingDescriptor `json:"rules"`
}

type sarifReportingDescriptor struct {
	ID               string        `json:"id"`
	ShortDescription sarifText     `json:"shortDescription"`
	Help             sarifMarkdown `json:"help"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifMarkdown struct {
	Text     string `json:"text"`
	Markdown string `json:"markdown,omitempty"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

func sarifRules() []sarifReportingDescriptor {
	return []sarifReportingDescriptor{
		sarifRule(sarifRuleViolationID, "Architecture rule violation", "A local import breaks a configured architecture rule.", "Keep imports within the configured architecture boundaries."),
		sarifRule(sarifCycleID, "Circular dependency", "A group of files imports each other circularly.", "Break cycles so modules can be reasoned about and bundled predictably."),
		sarifRule(sarifUndeclaredDepID, "Undeclared dependency", "A package is imported but is not declared in package.json.", "Declare imported packages in package.json or configure an alias if the import is not an npm package."),
		sarifRule(sarifUnusedDepID, "Unused dependency", "A runtime dependency is declared but not imported by reachable files.", "Remove unused dependencies or verify they are required outside the analysed graph."),
		sarifRule(sarifOrphanID, "Orphan file", "A source file is not reachable from the analysed entry point.", "Remove orphaned files or import them from reachable application code."),
		sarifRule(sarifInsightID, "Import insight", "Importstats found an advisory import-structure insight.", "Review the insight and decide whether it is worth acting on."),
	}
}

func sarifRule(id, short, text, help string) sarifReportingDescriptor {
	return sarifReportingDescriptor{
		ID:               id,
		ShortDescription: sarifText{Text: short},
		Help:             sarifMarkdown{Text: text, Markdown: help},
	}
}

func sarifResults(rep *analyzer.Report) []sarifResult {
	findings := collectFindings(rep)
	results := make([]sarifResult, 0, len(findings))
	for _, f := range findings {
		result := sarifResult{
			RuleID:  f.ruleID,
			Level:   f.sarifLevel,
			Message: sarifText{Text: f.message},
		}
		if f.file != "" {
			loc := sarifLocation{
				PhysicalLocation: sarifPhysicalLocation{
					ArtifactLocation: sarifArtifactLocation{URI: f.file},
				},
			}
			if f.line >= 1 {
				loc.PhysicalLocation.Region = &sarifRegion{StartLine: f.line}
			}
			result.Locations = []sarifLocation{loc}
		}
		results = append(results, result)
	}
	return results
}

type ciFinding struct {
	category   string
	ruleID     string
	sarifLevel string
	annotation string
	file       string
	line       int
	message    string
}

func collectFindings(rep *analyzer.Report) []ciFinding {
	if rep == nil {
		rep = &analyzer.Report{}
	}

	var out []ciFinding
	for _, v := range rep.RuleViolations {
		file := reportRelPath(rep, v.From)
		msg := fmt.Sprintf("%s must not import %s (rule: %s)", fileOrUnknown(file), reportRelPath(rep, v.To), v.Rule)
		out = append(out, ciFinding{
			category:   "architecture-rule-violation",
			ruleID:     sarifRuleViolationID,
			sarifLevel: "error",
			annotation: "error",
			file:       file,
			line:       v.Line,
			message:    msg,
		})
	}

	for _, cycle := range rep.Cycles {
		relCycle := make([]string, 0, len(cycle))
		for _, file := range cycle {
			relCycle = append(relCycle, reportRelPath(rep, file))
		}
		msg := "Circular dependency"
		if len(relCycle) > 0 {
			path := append([]string{}, relCycle...)
			path = append(path, relCycle[0])
			msg = "Circular dependency: " + strings.Join(path, " → ")
		}
		file := ""
		if len(relCycle) > 0 {
			file = relCycle[0]
		}
		out = append(out, ciFinding{
			category:   "cycle",
			ruleID:     sarifCycleID,
			sarifLevel: "warning",
			annotation: "warning",
			file:       file,
			message:    msg,
		})
	}

	sites := undeclaredSites(rep)
	for _, dep := range sortedStrings(rep.UndeclaredDeps) {
		site := sites[dep]
		msg := fmt.Sprintf("Imported package %q is not declared in package.json", dep)
		out = append(out, ciFinding{
			category:   "undeclared-dependency",
			ruleID:     sarifUndeclaredDepID,
			sarifLevel: "warning",
			annotation: "warning",
			file:       reportRelPath(rep, site.File),
			line:       site.Line,
			message:    msg,
		})
	}

	for _, dep := range sortedStrings(rep.UnusedDeps) {
		out = append(out, ciFinding{
			category:   "unused-dependency",
			ruleID:     sarifUnusedDepID,
			sarifLevel: "warning",
			annotation: "warning",
			message:    fmt.Sprintf("Declared dependency %q is not imported by reachable files", dep),
		})
	}

	for _, file := range sortedStrings(rep.Orphans) {
		rel := reportRelPath(rep, file)
		out = append(out, ciFinding{
			category:   "orphan-file",
			ruleID:     sarifOrphanID,
			sarifLevel: "note",
			annotation: "notice",
			file:       rel,
			message:    fmt.Sprintf("Orphan file is not reachable from the entry point: %s", rel),
		})
	}

	for _, in := range sortedInsights(rep.Insights) {
		subject := in.Subject
		if subject == "" {
			subject = in.ID
		}
		if subject == "" {
			subject = "insight"
		}
		msg := fmt.Sprintf("%s: %s", subject, in.Message)
		file := ""
		if looksLikePath(subject) {
			file = reportRelPath(rep, subject)
		}
		out = append(out, ciFinding{
			category:   "insight",
			ruleID:     sarifInsightID,
			sarifLevel: "note",
			annotation: "notice",
			file:       file,
			message:    msg,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].category != out[j].category {
			return out[i].category < out[j].category
		}
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		if out[i].line != out[j].line {
			return out[i].line < out[j].line
		}
		if out[i].message != out[j].message {
			return out[i].message < out[j].message
		}
		return out[i].ruleID < out[j].ruleID
	})
	return out
}

func undeclaredSites(rep *analyzer.Report) map[string]analyzer.ImportSite {
	out := map[string]analyzer.ImportSite{}
	for _, p := range rep.Packages {
		if !containsString(rep.UndeclaredDeps, p.Name) {
			continue
		}
		sites := append([]analyzer.ImportSite(nil), p.Sites...)
		sort.Slice(sites, func(i, j int) bool {
			if sites[i].File != sites[j].File {
				return sites[i].File < sites[j].File
			}
			if sites[i].Line != sites[j].Line {
				return sites[i].Line < sites[j].Line
			}
			if sites[i].Kind != sites[j].Kind {
				return sites[i].Kind < sites[j].Kind
			}
			return sites[i].Spec < sites[j].Spec
		})
		if len(sites) > 0 {
			out[p.Name] = sites[0]
		}
	}
	return out
}

func reportRelPath(rep *analyzer.Report, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		for _, root := range []string{rep.Summary.ProjectRoot, rep.Summary.SrcRoot} {
			if root == "" {
				continue
			}
			if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
				path = rel
				break
			}
		}
		if filepath.IsAbs(path) {
			path = filepath.Base(path)
		}
	}
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")
	if path == "." {
		return ""
	}
	return path
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func sortedInsights(values []analyzer.Insight) []analyzer.Insight {
	out := append([]analyzer.Insight(nil), values...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		if out[i].Severity != out[j].Severity {
			return out[i].Severity < out[j].Severity
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func looksLikePath(s string) bool {
	return strings.Contains(s, "/") || strings.Contains(s, "\\")
}

func fileOrUnknown(file string) string {
	if file == "" {
		return "unknown file"
	}
	return file
}
