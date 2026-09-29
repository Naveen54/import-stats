package compare

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// WriteText renders the comparison for the terminal, leading with version
// drift because that is the actionable part.
func WriteText(w io.Writer, rep *Report, top int) {
	if rep == nil || len(rep.Projects) == 0 {
		fmt.Fprintln(w, "Nothing to compare.")
		return
	}

	fmt.Fprintf(w, "\nComparing %d projects\n\n", len(rep.Projects))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tENTRY\tFILES\tIMPORTS\tPACKAGES")
	for _, p := range rep.Projects {
		if p.Error != "" {
			fmt.Fprintf(tw, "%s\t%s\t-\t-\t-\n", p.Name, "error: "+p.Error)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\n", p.Name, p.Entry, p.Files, p.Imports, p.Packages)
	}
	tw.Flush()

	var names []string
	for _, p := range rep.Projects {
		if p.Error == "" {
			names = append(names, p.Name)
		}
	}

	fmt.Fprintf(w, "\n%d packages shared by all · %d used by only one · %d with version drift\n",
		rep.SharedCount, rep.UniqueCount, rep.DriftCount)

	if rep.DriftCount > 0 {
		fmt.Fprintf(w, "\nVersion drift\n\n")
		dt := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(dt, "PACKAGE\t%s\n", strings.Join(upper(names), "\t"))
		shown := 0
		for _, r := range rep.Rows {
			if !r.Drift {
				break // drift rows sort first
			}
			if top > 0 && shown >= top {
				fmt.Fprintf(dt, "…and %d more\t\n", rep.DriftCount-shown)
				break
			}
			cells := make([]string, 0, len(names))
			for _, n := range names {
				if c, ok := r.Cells[n]; ok {
					cells = append(cells, versionOrDash(c))
				} else {
					cells = append(cells, "—")
				}
			}
			fmt.Fprintf(dt, "%s\t%s\n", r.Package, strings.Join(cells, "\t"))
			shown++
		}
		dt.Flush()
	}
}

func versionOrDash(c Cell) string {
	if c.Version == "" {
		return "(undeclared)"
	}
	return c.Version
}

func upper(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ToUpper(n)
	}
	return out
}
