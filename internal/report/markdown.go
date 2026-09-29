package report

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"importstats/internal/analyzer"
)

// WriteMarkdown writes a PR-friendly summary of the report to path.
func WriteMarkdown(rep *analyzer.Report, path string, top int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return writeMarkdown(rep, f, top)
}

func writeMarkdown(rep *analyzer.Report, f io.WriteCloser, top int) error {
	_, writeErr := io.WriteString(f, Markdown(rep, top))
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

// Markdown renders the summary and returns it, so callers can embed it.
func Markdown(rep *analyzer.Report, top int) string {
	if rep == nil {
		rep = &analyzer.Report{}
	}

	var b strings.Builder
	s := rep.Summary
	project := s.ProjectName
	if project == "" {
		project = "unknown project"
	}
	entry := s.Entry
	if entry == "" {
		entry = "unknown entry"
	}
	generated := s.GeneratedAt
	if generated == "" {
		generated = "unknown time"
	}

	fmt.Fprintf(&b, "# Import stats: %s — %s\n\n", mdText(project), mdText(entry))
	fmt.Fprintf(&b, "Analysed %d files · %d imports · %d packages · generated %s\n\n",
		s.FilesAnalyzed, s.TotalImports, s.UniquePackages, mdText(generated))

	writeSummaryTable(&b, s)
	writeTopPackages(&b, rep.Packages, top)
	writeHealth(&b, rep, top)
	writeInsights(&b, rep.Insights)
	writeUnusedExports(&b, rep.UnusedExports, top)
	writeRuleViolations(&b, rep.RuleViolations, top)

	return b.String()
}

func writeSummaryTable(b *strings.Builder, s analyzer.Summary) {
	b.WriteString("## Summary\n\n")
	b.WriteString("| Metric | Count |\n")
	b.WriteString("| --- | ---: |\n")
	writeCountRow(b, "Files analysed", s.FilesAnalyzed)
	writeCountRow(b, "Source files", s.SrcFiles)
	writeCountRow(b, "Total imports", s.TotalImports)
	writeCountRow(b, "Package imports", s.PackageImports)
	writeCountRow(b, "Local imports", s.LocalImports)
	writeCountRow(b, "Style imports", s.StyleImports)
	writeCountRow(b, "Asset imports", s.AssetImports)
	writeCountRow(b, "Unique packages", s.UniquePackages)
	writeCountRow(b, "Undeclared dependencies", s.UndeclaredDeps)
	writeCountRow(b, "Unused dependencies", s.UnusedDeps)
	writeCountRow(b, "Orphan files", s.OrphanFiles)
	writeCountRow(b, "Cycles", s.Cycles)
	writeCountRow(b, "Warnings", s.Warnings)
	writeCountRow(b, "Insights", s.Insights)
	writeCountRow(b, "Unused exports", s.UnusedExports)
	writeCountRow(b, "Rule violations", s.RuleViolations)
	b.WriteString("\n")
}

func writeCountRow(b *strings.Builder, label string, count int) {
	fmt.Fprintf(b, "| %s | %d |\n", mdTableCell(label), count)
}

func writeTopPackages(b *strings.Builder, packages []analyzer.PackageStat, top int) {
	if len(packages) == 0 {
		return
	}
	beginDetails(b, fmt.Sprintf("Top packages (%d)", len(packages)))
	limit := cappedLen(len(packages), top)
	if limit > 0 {
		b.WriteString("| # | Package | Imports | Files | Version | Dep type | Size |\n")
		b.WriteString("| ---: | --- | ---: | ---: | --- | --- | ---: |\n")
		for i := 0; i < limit; i++ {
			p := packages[i]
			fmt.Fprintf(b, "| %d | %s | %d | %d | %s | %s | %s |\n",
				i+1,
				mdTableCell(p.Name),
				p.Imports,
				p.Files,
				mdTableCell(p.Version),
				mdTableCell(p.DepType),
				mdTableCell(humanSize(p.SizeBytes)),
			)
		}
		b.WriteString("\n")
	}
	writeTruncated(b, len(packages), limit)
	endDetails(b)
}

func writeHealth(b *strings.Builder, rep *analyzer.Report, top int) {
	if len(rep.UndeclaredDeps) == 0 && len(rep.UnusedDeps) == 0 && len(rep.Cycles) == 0 && rep.Summary.OrphanFiles == 0 {
		return
	}

	b.WriteString("## Health\n\n")
	writeStringList(b, "Undeclared dependencies", rep.UndeclaredDeps, top)
	writeStringList(b, "Unused dependencies", rep.UnusedDeps, top)
	if len(rep.Cycles) > 0 {
		beginDetails(b, fmt.Sprintf("Cycles (%d)", len(rep.Cycles)))
		limit := cappedLen(len(rep.Cycles), top)
		for i := 0; i < limit; i++ {
			fmt.Fprintf(b, "- %s\n", mdText(strings.Join(rep.Cycles[i], " → ")))
		}
		writeTruncated(b, len(rep.Cycles), limit)
		endDetails(b)
	}
	if rep.Summary.OrphanFiles > 0 {
		fmt.Fprintf(b, "- Orphan files: %d\n\n", rep.Summary.OrphanFiles)
	}
}

func writeStringList(b *strings.Builder, title string, values []string, top int) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s (%d)\n\n", mdText(title), len(values))
	limit := cappedLen(len(values), top)
	for i := 0; i < limit; i++ {
		fmt.Fprintf(b, "- %s\n", mdText(values[i]))
	}
	writeTruncated(b, len(values), limit)
	b.WriteString("\n")
}

func writeInsights(b *strings.Builder, insights []analyzer.Insight) {
	if len(insights) == 0 {
		return
	}
	b.WriteString("## Insights\n\n")
	for _, in := range insights {
		subject := in.Subject
		if subject == "" {
			subject = in.ID
		}
		if subject == "" {
			subject = "insight"
		}
		fmt.Fprintf(b, "- **%s** %s: %s\n", mdText(in.Severity), mdText(subject), mdText(in.Message))
	}
	b.WriteString("\n")
}

func writeUnusedExports(b *strings.Builder, exports []analyzer.ExportStat, top int) {
	if len(exports) == 0 {
		return
	}
	beginDetails(b, fmt.Sprintf("Unused exports (%d)", len(exports)))
	limit := cappedLen(len(exports), top)
	if limit > 0 {
		b.WriteString("| File | Line | Name | Kind |\n")
		b.WriteString("| --- | ---: | --- | --- |\n")
		for i := 0; i < limit; i++ {
			ex := exports[i]
			fmt.Fprintf(b, "| %s | %d | %s | %s |\n",
				mdTableCell(ex.File),
				ex.Line,
				mdTableCell(ex.Name),
				mdTableCell(ex.Kind),
			)
		}
		b.WriteString("\n")
	}
	writeTruncated(b, len(exports), limit)
	endDetails(b)
}

func writeRuleViolations(b *strings.Builder, violations []analyzer.RuleViolation, top int) {
	if len(violations) == 0 {
		return
	}
	beginDetails(b, fmt.Sprintf("Rule violations (%d)", len(violations)))
	limit := cappedLen(len(violations), top)
	if limit > 0 {
		b.WriteString("| Rule | From | To | Line |\n")
		b.WriteString("| --- | --- | --- | ---: |\n")
		for i := 0; i < limit; i++ {
			v := violations[i]
			fmt.Fprintf(b, "| %s | %s | %s | %d |\n",
				mdTableCell(v.Rule),
				mdTableCell(v.From),
				mdTableCell(v.To),
				v.Line,
			)
		}
		b.WriteString("\n")
	}
	writeTruncated(b, len(violations), limit)
	endDetails(b)
}

func beginDetails(b *strings.Builder, summary string) {
	fmt.Fprintf(b, "<details>\n<summary>%s</summary>\n\n", mdText(summary))
}

func endDetails(b *strings.Builder) {
	b.WriteString("</details>\n\n")
}

func writeTruncated(b *strings.Builder, total, shown int) {
	if total > shown {
		fmt.Fprintf(b, "…and %d more.\n\n", total-shown)
	}
}

func cappedLen(total, top int) int {
	if top <= 0 || top > total {
		return total
	}
	return top
}

func humanSize(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}

	value := float64(bytes) / 1024
	unit := "KB"
	for _, next := range []string{"MB", "GB"} {
		if value < 1024 {
			break
		}
		value /= 1024
		unit = next
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func mdTableCell(s string) string {
	return mdText(s)
}

func mdText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\\", "\\\\",
		"`", "\\`",
		"|", "\\|",
		"*", "\\*",
		"_", "\\_",
		"[", "\\[",
		"]", "\\]",
	)
	return replacer.Replace(s)
}
