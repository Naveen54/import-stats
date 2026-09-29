// Package report writes analysis results to JSON and CSV, and renders a
// terminal summary.
package report

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"importstats/internal/analyzer"
)

// WriteJSON writes the full report to path.
func WriteJSON(rep *analyzer.Report, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return writeJSON(rep, f)
}

func writeJSON(rep *analyzer.Report, f io.WriteCloser) error {
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	encErr := enc.Encode(rep)
	closeErr := f.Close()
	return errors.Join(encErr, closeErr)
}

// WriteCSV writes one row per package with its importing files.
func WriteCSV(rep *analyzer.Report, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return writeCSV(rep, f)
}

func writeCSV(rep *analyzer.Report, f io.WriteCloser) error {
	w := csv.NewWriter(f)

	if err := w.Write([]string{"package", "imports", "files", "depType", "version", "topSymbols", "importingFiles"}); err != nil {
		return errors.Join(err, f.Close())
	}
	for _, p := range rep.Packages {
		files := map[string]bool{}
		for _, s := range p.Sites {
			files[s.File] = true
		}
		list := make([]string, 0, len(files))
		for f := range files {
			list = append(list, f)
		}
		sort.Strings(list)

		symbols := make([]string, 0, 5)
		for i, s := range p.Symbols {
			if i == 5 {
				break
			}
			symbols = append(symbols, fmt.Sprintf("%s(%d)", s.Name, s.Count))
		}
		row := []string{
			csvSafeCell(p.Name),
			strconv.Itoa(p.Imports),
			strconv.Itoa(p.Files),
			p.DepType,
			csvSafeCell(p.Version),
			csvSafeCell(strings.Join(symbols, " ")),
			csvSafeCell(strings.Join(list, " ")),
		}
		if err := w.Write(row); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	w.Flush()
	err := w.Error()
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}

func csvSafeCell(s string) string {
	// RFC-4180 quoting does not stop spreadsheets from evaluating formulas.
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	default:
		return s
	}
}

// PrintSummary renders a compact terminal overview.
func PrintSummary(out io.Writer, rep *analyzer.Report, top int) {
	s := rep.Summary
	fmt.Fprintf(out, "\nEntry            %s\n", s.Entry)
	if s.ProjectName != "" {
		fmt.Fprintf(out, "Project          %s\n", s.ProjectName)
	}
	fmt.Fprintf(out, "Files analysed   %d (of %d under src)\n", s.FilesAnalyzed, s.SrcFiles)
	fmt.Fprintf(out, "Imports          %d total — %d package, %d local, %d style, %d asset\n",
		s.TotalImports, s.PackageImports, s.LocalImports, s.StyleImports, s.AssetImports)
	fmt.Fprintf(out, "Packages         %d unique (%d undeclared, %d unused deps)\n",
		s.UniquePackages, s.UndeclaredDeps, s.UnusedDeps)
	fmt.Fprintf(out, "Orphans          %d   Cycles %d   Warnings %d\n\n", s.OrphanFiles, s.Cycles, s.Warnings)

	if top > 0 && len(rep.Packages) > 0 {
		fmt.Fprintf(out, "Top packages by imports:\n")
		for i, p := range rep.Packages {
			if i == top {
				break
			}
			fmt.Fprintf(out, "  %-34s %4d imports  %4d files  %s\n", p.Name, p.Imports, p.Files, p.DepType)
		}
		fmt.Fprintln(out)
	}
}
