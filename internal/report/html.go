package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"importstats/internal/analyzer"
)

// Assets holds the dashboard source needed to build a standalone page.
type Assets struct {
	IndexHTML, AppJS, AppCSS string
	Scripts                  map[string]string // extra scripts keyed by filename, e.g. "graph.js"
}

var (
	linkTagRE      = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	scriptSrcTagRE = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)')[^>]*>\s*</script\s*>`)
)

// WriteHTML writes a self-contained single-file report to path.
func WriteHTML(rep *analyzer.Report, a Assets, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return writeHTML(rep, a, f)
}

func writeHTML(rep *analyzer.Report, a Assets, f io.WriteCloser) error {
	html, renderErr := HTML(rep, a)
	if renderErr != nil {
		return errors.Join(renderErr, f.Close())
	}
	_, writeErr := io.WriteString(f, html)
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

// HTML renders a self-contained single-file report and returns it.
func HTML(rep *analyzer.Report, a Assets) (string, error) {
	if rep == nil {
		rep = &analyzer.Report{}
	}
	if strings.TrimSpace(a.IndexHTML) == "" {
		return "", errors.New("html report asset index html is empty")
	}
	if strings.TrimSpace(a.AppCSS) == "" {
		return "", errors.New("html report asset app css is empty")
	}

	payload, err := reportJSON(rep)
	if err != nil {
		return "", err
	}

	out := a.IndexHTML
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "\n")

	var styleFound bool
	out = linkTagRE.ReplaceAllStringFunc(out, func(tag string) string {
		if styleFound || !sameAttr(tag, "rel", "stylesheet") || !sameAttr(tag, "href", "app.css") {
			return tag
		}
		styleFound = true
		return "<style>\n" + a.AppCSS + "\n" + offlineCSS() + "\n</style>"
	})
	if !styleFound {
		return "", errors.New(`html report index missing stylesheet link for "app.css"`)
	}

	var scriptErr error
	var sawAppScript bool
	out = scriptSrcTagRE.ReplaceAllStringFunc(out, func(tag string) string {
		if scriptErr != nil {
			return tag
		}
		matches := scriptSrcTagRE.FindStringSubmatch(tag)
		if len(matches) != 3 {
			scriptErr = fmt.Errorf("html report could not parse script tag %q", tag)
			return tag
		}
		name := matches[1]
		if name == "" {
			name = matches[2]
		}
		body, err := scriptBody(a, name)
		if err != nil {
			scriptErr = err
			return tag
		}
		inline := "<script>\n" + body + "\n</SCRIPT>"
		if name == "app.js" {
			sawAppScript = true
			return bootstrapScript(payload) + "\n" + inline
		}
		return inline
	})
	if scriptErr != nil {
		return "", scriptErr
	}
	if !sawAppScript {
		return "", errors.New(`html report index missing app script tag for "app.js"`)
	}

	const body = "<body>"
	if !strings.Contains(out, body) {
		return "", fmt.Errorf("html report index missing %q", body)
	}
	out = strings.Replace(out, body, body+"\n"+snapshotBanner(), 1)

	return out, nil
}

func scriptBody(a Assets, name string) (string, error) {
	if name == "app.js" && strings.TrimSpace(a.AppJS) != "" {
		return a.AppJS, nil
	}
	body, ok := a.Scripts[name]
	if !ok || strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("html report script asset %q is missing", name)
	}
	return body, nil
}

func sameAttr(tag, name, value string) bool {
	got, ok := attrValue(tag, name)
	return ok && strings.EqualFold(got, value)
}

func attrValue(tag, name string) (string, bool) {
	attrRE := regexp.MustCompile(`(?is)\b` + regexp.QuoteMeta(name) + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	matches := attrRE.FindStringSubmatch(tag)
	if len(matches) == 0 {
		return "", false
	}
	for i := 1; i < len(matches); i++ {
		if matches[i] != "" {
			return matches[i], true
		}
	}
	return "", true
}

func reportJSON(rep *analyzer.Report) (string, error) {
	payload, err := json.Marshal(rep)
	if err != nil {
		return "", err
	}
	s := string(payload)
	s = strings.ReplaceAll(s, "\u2028", "\\u2028")
	s = strings.ReplaceAll(s, "\u2029", "\\u2029")
	return s, nil
}

func bootstrapScript(payload string) string {
	return "<script>\n" +
		"window.__IMPORT_STATS_REPORT__ = " + payload + ";\n" +
		"(function () {\n" +
		"  var report = window.__IMPORT_STATS_REPORT__;\n" +
		"  window.fetch = function (input) {\n" +
		"    var url = typeof input === 'string' ? input : (input && input.url) || '';\n" +
		"    if (url === '/api/report' || url.endsWith('/api/report')) {\n" +
		"      return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve(report); }, text: function () { return Promise.resolve(JSON.stringify(report)); } });\n" +
		"    }\n" +
		"    return Promise.reject(new Error('This static import-stats report cannot access ' + url));\n" +
		"  };\n" +
		"}());\n" +
		"</SCRIPT>"
}

func offlineCSS() string {
	return `
#reanalyze,
#export-json {
  display: none !important;
}

.static-snapshot {
  background: rgba(88, 166, 255, .14);
  border-bottom: 1px solid rgba(88, 166, 255, .35);
  color: var(--text);
  font-size: 13px;
  padding: 8px 28px;
}
`
}

func snapshotBanner() string {
	return `<div class="static-snapshot" role="note">Static offline snapshot — data is baked into this file and will not re-analyse or download JSON.</div>`
}
