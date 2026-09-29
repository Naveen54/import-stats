// Command importstats analyses a React application's import graph starting
// from an entry file and presents the statistics in a local web dashboard.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"importstats/internal/analysis"
	"importstats/internal/analyzer"
	"importstats/internal/compare"
	"importstats/internal/diff"
	"importstats/internal/history"
	"importstats/internal/report"
	"importstats/internal/rules"
	"importstats/internal/server"
	"importstats/internal/viz"
)

type aliasFlag map[string]string

func (a aliasFlag) String() string { return "" }

func (a aliasFlag) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	if len(parts) != 2 || parts[0] == "" {
		return fmt.Errorf("alias must be key=path, got %q", v)
	}
	a[parts[0]] = parts[1]
	return nil
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func main() {
	var (
		entryFlag  = flag.String("entry", "", "entry file (default: <dir>/src/index.js)")
		srcRoot    = flag.String("src-root", "", "root for bare local imports (default: the src dir on the entry's path)")
		port       = flag.Int("port", 0, "dashboard port (0 = pick a free one)")
		noOpen     = flag.Bool("no-open", false, "do not open a browser")
		noServe    = flag.Bool("no-serve", false, "print the summary and exit without serving the dashboard")
		jsonOut    = flag.String("json", "", "write the full report to a JSON file")
		csvOut     = flag.String("csv", "", "write package stats to a CSV file")
		top        = flag.Int("top", 15, "number of packages in the terminal summary")
		workers    = flag.Int("workers", 0, "parser workers (0 = 2x CPU count)")
		noSize     = flag.Bool("no-size", false, "skip measuring installed package size from node_modules")
		baseline   = flag.String("baseline", "", "compare against a previously saved JSON report")
		saveBase   = flag.String("save-baseline", "", "write this run's report for a later --baseline comparison")
		failOn     = flag.String("fail-on", "", "comma-separated gates that exit non-zero: new-cycles, new-undeclared, new-orphans, rules, imports-up, packages-up")
		mdOut      = flag.String("md", "", "write a Markdown summary to a file")
		htmlOut    = flag.String("html", "", "write a self-contained offline HTML report to a file")
		sarifOut   = flag.String("sarif", "", "write CI findings as SARIF 2.1.0")
		annotate   = flag.Bool("annotate", false, "write GitHub Actions annotations to stdout")
		historyN   = flag.Int("history", 0, "sample this many commits from git history")
		watchMode  = flag.Bool("watch", false, "watch source files and re-analyse the dashboard automatically")
		dotOut     = flag.String("dot", "", "write the module graph in Graphviz DOT format")
		mermaidOut = flag.String("mermaid", "", "write the module graph as a Mermaid flowchart")
		collapse   = flag.Bool("collapse", false, "collapse the graph export to one node per directory")
		compareIn  = listFlag{}
		aliases    = aliasFlag{}
		ignore     = listFlag{}
		ruleFlags  = listFlag{}
	)
	flag.Var(aliases, "alias", "extra module alias, key=path (repeatable)")
	flag.Var(&compareIn, "compare", "additional project to compare against (repeatable); enables comparison mode")
	flag.Var(&ruleFlags, "rule", "architecture constraint, e.g. \"src/components !-> src/pages\" (repeatable)")
	flag.Var(&ignore, "ignore", "skip paths containing this substring or matching this basename glob (repeatable)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `importstats — React import graph statistics

Usage:
  importstats [flags] [entry-file-or-directory]

Examples:
  importstats ~/projects/my-app/src/index.js
  importstats ~/projects/my-app                 # defaults to src/index.js
  importstats --json report.json --csv packages.csv --no-serve ./src/index.js
  importstats --compare ../admin-app --compare ../inbox-app ./my-app
  importstats --baseline base.json --fail-on new-cycles,rules ./src/index.js

Note: flags must come before the project path.

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	// Go's flag package stops parsing at the first positional argument, so a
	// path placed before the flags silently swallows them. Catch that instead
	// of quietly ignoring half the command line.
	if flag.NArg() > 1 {
		fmt.Fprintf(os.Stderr,
			"error: unexpected extra arguments: %s\nflags must come before the project path, e.g.\n  importstats --compare ../other-app ./my-app\n",
			strings.Join(flag.Args()[1:], " "))
		os.Exit(2)
	}

	input := *entryFlag
	if input == "" && flag.NArg() > 0 {
		input = flag.Arg(0)
	}

	parsedRules, err := rules.Parse(ruleFlags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	opts := analysis.Options{
		Input:   input,
		SrcRoot: *srcRoot,
		Aliases: aliases,
		Ignore:  ignore,
		Workers: *workers,
		NoSize:  *noSize,
		Rules:   parsedRules,
	}

	if len(compareIn) > 0 {
		runComparison(append([]string{input}, compareIn...), opts, *jsonOut, *top, *port, !*noServe, !*noOpen)
		return
	}

	rep, err := analysis.Run(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	report.PrintSummary(os.Stdout, rep, *top)

	if *jsonOut != "" {
		if err := report.WriteJSON(rep, *jsonOut); err != nil {
			fmt.Fprintln(os.Stderr, "json export failed:", err)
		} else {
			fmt.Printf("JSON report written to %s\n", *jsonOut)
		}
	}
	if *csvOut != "" {
		if err := report.WriteCSV(rep, *csvOut); err != nil {
			fmt.Fprintln(os.Stderr, "csv export failed:", err)
		} else {
			fmt.Printf("CSV report written to %s\n", *csvOut)
		}
	}

	if *mdOut != "" {
		if err := report.WriteMarkdown(rep, *mdOut, *top); err != nil {
			fmt.Fprintln(os.Stderr, "markdown export failed:", err)
		} else {
			fmt.Printf("Markdown summary written to %s\n", *mdOut)
		}
	}
	if *htmlOut != "" {
		if err := writeStandaloneHTML(rep, *htmlOut); err != nil {
			fmt.Fprintln(os.Stderr, "html export failed:", err)
		} else {
			fmt.Printf("Standalone HTML report written to %s\n", *htmlOut)
		}
	}
	if *sarifOut != "" {
		if err := report.WriteSARIF(rep, *sarifOut); err != nil {
			fmt.Fprintln(os.Stderr, "SARIF export failed:", err)
		} else {
			fmt.Printf("SARIF report written to %s\n", *sarifOut)
		}
		if *annotate {
			if err := report.WriteAnnotations(os.Stdout, rep); err != nil {
				fmt.Fprintln(os.Stderr, "annotation output failed:", err)
			}
		}
		if *historyN > 0 {
			hrep, err := history.Run(history.Options{
				Repo: opts.Input, Commits: *historyN, Every: "commit",
				Analyze: func(worktree string) (*analyzer.Report, error) {
					hopts := opts
					hopts.Input = worktree
					hopts.NoSize = true
					return analysis.Run(hopts)
				},
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, "history failed:", err)
			} else {
				if err := history.WriteText(os.Stdout, hrep); err != nil {
					fmt.Fprintln(os.Stderr, "history output failed:", err)
				}
			}
		}
	}
	vizOpts := viz.Options{Collapse: *collapse}
	if *dotOut != "" {
		if err := viz.WriteDOT(rep, vizOpts, *dotOut); err != nil {
			fmt.Fprintln(os.Stderr, "dot export failed:", err)
		} else {
			fmt.Printf("DOT graph written to %s\n", *dotOut)
		}
	}
	if *mermaidOut != "" {
		if err := viz.WriteMermaid(rep, vizOpts, *mermaidOut); err != nil {
			fmt.Fprintln(os.Stderr, "mermaid export failed:", err)
		} else {
			fmt.Printf("Mermaid graph written to %s\n", *mermaidOut)
		}
	}
	if *saveBase != "" {
		if err := report.WriteJSON(rep, *saveBase); err != nil {
			fmt.Fprintln(os.Stderr, "baseline write failed:", err)
		} else {
			fmt.Printf("Baseline written to %s\n", *saveBase)
		}
	}

	// A failing gate must still exit non-zero even though the dashboard would
	// otherwise block forever, so the comparison runs before serving.
	if *baseline != "" {
		old, err := diff.Load(*baseline)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: baseline:", err)
			os.Exit(2)
		}
		d := diff.Compute(old, rep)
		fmt.Println()
		diff.WriteText(os.Stdout, d)

		if *failOn != "" {
			gates := strings.Split(*failOn, ",")
			for i := range gates {
				gates[i] = strings.TrimSpace(gates[i])
			}
			failed, err := diff.Check(d, gates)
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(2)
			}
			if len(failed) > 0 {
				fmt.Fprintf(os.Stderr, "\nfailing gates: %s\n", strings.Join(failed, ", "))
				os.Exit(1)
			}
		}
	} else if *failOn != "" {
		fmt.Fprintln(os.Stderr, "error: --fail-on requires --baseline")
		os.Exit(2)
	}

	if *noServe {
		return
	}
	// The dashboard's re-analyse button re-runs this from scratch, so edits
	// made after startup are picked up without restarting the CLI.
	again := func() (*analyzer.Report, error) {
		fresh, err := analysis.Run(opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "re-analysis failed:", err)
			return nil, err
		}
		fmt.Printf("Re-analysed %s — %d files, %d imports\n",
			fresh.Summary.Entry, fresh.Summary.FilesAnalyzed, fresh.Summary.TotalImports)
		return fresh, nil
	}

	if *watchMode {
		if err := server.ServeWatching(rep, again, []string{opts.Input}, *port, !*noOpen); err != nil {
			fmt.Fprintln(os.Stderr, "server error:", err)
			os.Exit(1)
		}
		return
	}
	if err := server.Serve(rep, again, *port, !*noOpen); err != nil {
		fmt.Fprintln(os.Stderr, "server error:", err)
		os.Exit(1)
	}
}

// runComparison analyses several projects in parallel and contrasts them.
// A project that fails to analyse is reported in place rather than aborting
// the whole comparison.
func runComparison(inputs []string, base analysis.Options, jsonOut string, top, port int, serve, open bool) {
	results := make([]compare.Input, len(inputs))
	var wg sync.WaitGroup

	for i, in := range inputs {
		wg.Add(1)
		go func(i int, in string) {
			defer wg.Done()
			opts := base
			opts.Input = in
			// Sizing every project's node_modules multiplies the slowest part
			// of the run for data the comparison never shows.
			opts.NoSize = true
			rep, err := analysis.Run(opts)
			results[i] = compare.Input{Name: projectLabel(in, rep), Root: in, Report: rep, Err: err}
		}(i, in)
	}
	wg.Wait()

	cmp := compare.Build(results)
	compare.WriteText(os.Stdout, cmp, top)

	if jsonOut != "" {
		if err := writeComparisonJSON(cmp, jsonOut); err != nil {
			fmt.Fprintln(os.Stderr, "json export failed:", err)
		} else {
			fmt.Printf("\nComparison JSON written to %s\n", jsonOut)
		}
	}

	if !serve {
		return
	}
	if err := server.ServeComparison(cmp, port, open); err != nil {
		fmt.Fprintln(os.Stderr, "server failed:", err)
		os.Exit(1)
	}
}

// projectLabel prefers the package.json name, falling back to the directory
// so two projects are never labelled identically by accident.
func projectLabel(in string, rep *analyzer.Report) string {
	if rep != nil && rep.Summary.ProjectName != "" {
		return rep.Summary.ProjectName
	}
	if abs, err := filepath.Abs(in); err == nil {
		return filepath.Base(abs)
	}
	return in
}

// writeStandaloneHTML inlines the dashboard assets and bakes the report into a
// single file that opens offline.
func writeStandaloneHTML(rep *analyzer.Report, path string) error {
	index, err := server.Asset("index.html")
	if err != nil {
		return err
	}
	appJS, err := server.Asset("app.js")
	if err != nil {
		return err
	}
	appCSS, err := server.Asset("app.css")
	if err != nil {
		return err
	}
	scripts, err := server.Scripts()
	if err != nil {
		return err
	}
	return report.WriteHTML(rep, report.Assets{
		IndexHTML: index,
		AppJS:     appJS,
		AppCSS:    appCSS,
		Scripts:   scripts,
	}, path)
}

// writeComparisonJSON writes and closes the file before any server blocks.
func writeComparisonJSON(cmp *compare.Report, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return errors.Join(enc.Encode(cmp), f.Close())
}
