package report

import (
	"fmt"
	"io"
	"strings"

	"importstats/internal/analyzer"
)

// WriteAnnotations writes GitHub Actions workflow commands to w.
func WriteAnnotations(w io.Writer, rep *analyzer.Report) error {
	for _, f := range collectFindings(rep) {
		if _, err := fmt.Fprint(w, annotationLine(f)); err != nil {
			return err
		}
	}
	return nil
}

func annotationLine(f ciFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "::%s", f.annotation)
	props := annotationProps(f)
	if len(props) > 0 {
		b.WriteByte(' ')
		b.WriteString(strings.Join(props, ","))
	}
	b.WriteString("::")
	b.WriteString(escapeAnnotationMessage(f.message))
	b.WriteByte('\n')
	return b.String()
}

func annotationProps(f ciFinding) []string {
	var props []string
	if f.file != "" {
		props = append(props, "file="+escapeAnnotationProperty(f.file))
	}
	if f.line >= 1 {
		props = append(props, fmt.Sprintf("line=%d", f.line))
	}
	return props
}

func escapeAnnotationMessage(s string) string {
	replacer := strings.NewReplacer(
		"%", "%25",
		"\r", "%0D",
		"\n", "%0A",
	)
	return replacer.Replace(s)
}

func escapeAnnotationProperty(s string) string {
	replacer := strings.NewReplacer(
		"%", "%25",
		"\r", "%0D",
		"\n", "%0A",
		":", "%3A",
		",", "%2C",
	)
	return replacer.Replace(s)
}
