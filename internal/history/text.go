package history

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// WriteText renders the trend as a compact ASCII table.
func WriteText(w io.Writer, rep *Report) error {
	if rep == nil || len(rep.Points) == 0 {
		_, err := fmt.Fprintln(w, "No history points.")
		return err
	}

	if _, err := fmt.Fprintf(w, "Import trend for %s (%d points)\n\n", rep.Repo, len(rep.Points)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Packages spark: %s\n\n", sparkline(rep.Points)); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "DATE\tCOMMIT\tFILES\tIMPORTS\tPACKAGES\tORPHANS\tCYCLES\tSTATUS"); err != nil {
		return err
	}
	var prev *Point
	for i := range rep.Points {
		pt := &rep.Points[i]
		status := "ok"
		if pt.Err != "" {
			status = "error: " + compact(pt.Err, 32)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			pt.When.Format("2006-01-02"), pt.Short,
			metric(prev, pt, func(p *Point) int { return p.Files }),
			metric(prev, pt, func(p *Point) int { return p.Imports }),
			metric(prev, pt, func(p *Point) int { return p.Packages }),
			metric(prev, pt, func(p *Point) int { return p.Orphans }),
			metric(prev, pt, func(p *Point) int { return p.Cycles }),
			status); err != nil {
			return err
		}
		if pt.Err == "" {
			prev = pt
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if err := writeChanges(w, "Added packages", "+", rep.Added); err != nil {
		return err
	}
	if err := writeChanges(w, "Removed packages", "-", rep.Removed); err != nil {
		return err
	}
	return nil
}

func delta(prev, pt *Point, get func(*Point) int) (int, bool) {
	if prev == nil || pt.Err != "" {
		return 0, false
	}
	return get(pt) - get(prev), true
}

func metric(prev, pt *Point, get func(*Point) int) string {
	d, ok := delta(prev, pt, get)
	return valueWithDelta(get(pt), d, ok, pt.Err)
}

func valueWithDelta(v int, d int, ok bool, errText string) string {
	if errText != "" {
		return "-"
	}
	if !ok {
		return fmt.Sprintf("%d", v)
	}
	if d == 0 {
		return fmt.Sprintf("%d (+0)", v)
	}
	if d > 0 {
		return fmt.Sprintf("%d (+%d)", v, d)
	}
	return fmt.Sprintf("%d (%d)", v, d)
}

func writeChanges(w io.Writer, title, prefix string, changes []PackageChange) error {
	if len(changes) == 0 {
		_, err := fmt.Fprintf(w, "%s: none\n", title)
		return err
	}
	if _, err := fmt.Fprintf(w, "%s:\n", title); err != nil {
		return err
	}
	limit := len(changes)
	if limit > 12 {
		limit = 12
	}
	for i := 0; i < limit; i++ {
		c := changes[i]
		if _, err := fmt.Fprintf(w, "  %s %s at %s (%s)\n", prefix, c.Package, c.Short, c.When.Format("2006-01-02")); err != nil {
			return err
		}
	}
	if len(changes) > limit {
		_, err := fmt.Fprintf(w, "  ...and %d more\n", len(changes)-limit)
		return err
	}
	return nil
}

func compact(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func sparkline(points []Point) string {
	const marks = "._:-=+*#%@"
	if len(points) == 0 {
		return ""
	}
	min, max := 0, 0
	have := false
	for _, pt := range points {
		if pt.Err != "" {
			continue
		}
		if !have || pt.Packages < min {
			min = pt.Packages
		}
		if !have || pt.Packages > max {
			max = pt.Packages
		}
		have = true
	}
	if !have {
		return strings.Repeat("?", len(points))
	}
	var b strings.Builder
	for _, pt := range points {
		if pt.Err != "" {
			b.WriteByte('?')
			continue
		}
		if max == min {
			b.WriteByte('-')
			continue
		}
		idx := (pt.Packages - min) * (len(marks) - 1) / (max - min)
		b.WriteByte(marks[idx])
	}
	return b.String()
}
