package report

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"importstats/internal/analyzer"
)

func TestHTMLInlinesAssets(t *testing.T) {
	out, err := HTML(reportFixture(), htmlAssetsFixture())
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}

	for _, want := range []string{
		"<style>\n/* css sentinel */",
		"console.log('js sentinel');",
		"window.__IMPORT_STATS_REPORT__ = ",
		"Static offline snapshot",
		"#reanalyze",
		"#export-json",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("HTML() missing %q in:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"<link", "<script src="} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("HTML() still contains %q in:\n%s", unwanted, out)
		}
	}
	assertNoExternalAssetRefs(t, out)
}

func TestHTMLBakedJSONParses(t *testing.T) {
	out, err := HTML(reportFixture(), htmlAssetsFixture())
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}

	var got analyzer.Report
	if err := json.Unmarshal([]byte(extractBakedJSON(t, out)), &got); err != nil {
		t.Fatalf("baked JSON did not parse: %v", err)
	}
	if got.Summary.ProjectName != "fixture-app" || got.Packages[0].Name != "@scope/pkg, \"quoted\"" {
		t.Fatalf("baked JSON did not match report: %+v", got)
	}
}

func TestHTMLEscapesScriptBreakingJSON(t *testing.T) {
	evil := "</script><img src=x onerror=alert(1)>"
	rep := &analyzer.Report{
		Summary: analyzer.Summary{ProjectName: evil},
		Packages: []analyzer.PackageStat{
			{Name: evil, Kinds: map[string]int{"named": 1}},
		},
		InternalFiles: []analyzer.FileStat{
			{File: "src/" + evil + ".tsx"},
		},
	}

	out, err := HTML(rep, htmlAssetsFixture())
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	if strings.Contains(out, evil) {
		t.Fatalf("HTML() contains unescaped script-breaking payload:\n%s", out)
	}
	if strings.Contains(out, "</script>") {
		t.Fatalf("HTML() contains a raw lower-case script close sequence:\n%s", out)
	}
	if got, want := strings.Count(out, "<script>"), 2; got != want {
		t.Fatalf("script open count = %d, want %d", got, want)
	}
	if got, want := strings.Count(out, "</SCRIPT>"), 2; got != want {
		t.Fatalf("script close count = %d, want %d", got, want)
	}

	var parsed analyzer.Report
	if err := json.Unmarshal([]byte(extractBakedJSON(t, out)), &parsed); err != nil {
		t.Fatalf("escaped baked JSON did not parse: %v", err)
	}
	if parsed.Packages[0].Name != evil {
		t.Fatalf("escaped JSON value = %q, want %q", parsed.Packages[0].Name, evil)
	}
}

func TestHTMLDeterministic(t *testing.T) {
	a := htmlAssetsFixture()
	first, err := HTML(reportFixture(), a)
	if err != nil {
		t.Fatalf("HTML() first error = %v", err)
	}
	second, err := HTML(reportFixture(), a)
	if err != nil {
		t.Fatalf("HTML() second error = %v", err)
	}
	if first != second {
		t.Fatal("HTML() output differs across calls")
	}
}

func TestHTMLInlinesExtraScriptsInIndexOrderDeterministically(t *testing.T) {
	assets := Assets{
		IndexHTML: `<!doctype html>
<html>
<head><link href='app.css' rel='stylesheet'></head>
<body>
<script defer src="app.js"></script>
<script data-name="graph" src='graph.js' defer></script>
<script type="text/javascript" src="charts.js"></script>
</body>
</html>`,
		AppJS:  "window.order = ['app'];",
		AppCSS: "body { color: blue; }",
		Scripts: map[string]string{
			"charts.js": "window.order.push('charts');",
			"graph.js":  "window.order.push('graph');",
			"unused.js": "window.order.push('unused');",
		},
	}

	first, err := HTML(reportFixture(), assets)
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := HTML(reportFixture(), assets)
		if err != nil {
			t.Fatalf("HTML() repeat %d error = %v", i, err)
		}
		if got != first {
			t.Fatalf("HTML() repeat %d was not deterministic", i)
		}
	}

	appAt := strings.Index(first, "window.order = ['app'];")
	graphAt := strings.Index(first, "window.order.push('graph');")
	chartsAt := strings.Index(first, "window.order.push('charts');")
	if appAt < 0 || graphAt < 0 || chartsAt < 0 {
		t.Fatalf("inlined scripts missing from:\n%s", first)
	}
	if !(appAt < graphAt && graphAt < chartsAt) {
		t.Fatalf("script order was not preserved: app=%d graph=%d charts=%d\n%s", appAt, graphAt, chartsAt, first)
	}
	if strings.Contains(first, "window.order.push('unused');") || strings.Contains(first, "unused.js") {
		t.Fatalf("unreferenced script was appended:\n%s", first)
	}
	assertNoExternalAssetRefs(t, first)
}

func TestHTMLNilReport(t *testing.T) {
	out, err := HTML(nil, htmlAssetsFixture())
	if err != nil {
		t.Fatalf("HTML(nil) error = %v", err)
	}
	var got analyzer.Report
	if err := json.Unmarshal([]byte(extractBakedJSON(t, out)), &got); err != nil {
		t.Fatalf("nil report baked JSON did not parse: %v", err)
	}
}

func TestHTMLMissingReferencedScriptErrors(t *testing.T) {
	assets := htmlAssetsFixture()
	assets.IndexHTML = strings.Replace(assets.IndexHTML, `<script src="app.js"></script>`, `<script src="app.js"></script><script src="graph.js"></script>`, 1)

	_, err := HTML(reportFixture(), assets)
	if err == nil {
		t.Fatal("HTML() error = nil, want missing graph.js error")
	}
	if !strings.Contains(err.Error(), "graph.js") {
		t.Fatalf("HTML() error = %v, want it to name graph.js", err)
	}
}

func TestHTMLRejectsMissingAssets(t *testing.T) {
	cases := []struct {
		name   string
		assets Assets
	}{
		{name: "index", assets: Assets{AppJS: "js", AppCSS: "css"}},
		{name: "js", assets: Assets{IndexHTML: htmlIndexFixture(), AppCSS: "css"}},
		{name: "css", assets: Assets{IndexHTML: htmlIndexFixture(), AppJS: "js"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := HTML(reportFixture(), c.assets); err == nil {
				t.Fatal("HTML() error = nil, want missing asset error")
			}
		})
	}
}

func TestWriteHTMLWritesFileAndSurfacesErrors(t *testing.T) {
	dir := filepath.Join(".", ".html-test-output")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("RemoveAll() error = %v", err)
		}
	})

	path := filepath.Join(dir, "report.html")
	if err := WriteHTML(reportFixture(), htmlAssetsFixture(), path); err != nil {
		t.Fatalf("WriteHTML() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "window.__IMPORT_STATS_REPORT__ = ") {
		t.Fatalf("written HTML does not contain baked report:\n%s", data)
	}

	if err := WriteHTML(reportFixture(), htmlAssetsFixture(), filepath.Join(dir, "missing", "report.html")); err == nil {
		t.Fatal("WriteHTML() error = nil, want create error")
	}

	closeErr := errors.New("close failed")
	if err := writeHTML(reportFixture(), htmlAssetsFixture(), closeOnlyFailWriter{err: closeErr}); !errors.Is(err, closeErr) {
		t.Fatalf("writeHTML() error = %v, want close error", err)
	}
}

func extractBakedJSON(t *testing.T, out string) string {
	t.Helper()
	const prefix = "window.__IMPORT_STATS_REPORT__ = "
	start := strings.Index(out, prefix)
	if start < 0 {
		t.Fatalf("missing baked JSON assignment in:\n%s", out)
	}
	start += len(prefix)
	end := strings.Index(out[start:], ";\n(function")
	if end < 0 {
		t.Fatalf("missing baked JSON terminator in:\n%s", out[start:])
	}
	return out[start : start+end]
}

func htmlAssetsFixture() Assets {
	return Assets{
		IndexHTML: htmlIndexFixture(),
		AppJS:     "console.log('js sentinel');",
		AppCSS:    "/* css sentinel */\nbody { color: red; }",
		Scripts:   map[string]string{"unused.js": "console.log('unused');"},
	}
}

func htmlIndexFixture() string {
	return `<!doctype html>
<html lang="en">
<head>
<link rel="stylesheet" href="app.css" />
</head>
<body>
<h1>Report</h1>
<script src="app.js"></script>
</body>
</html>`
}

func assertNoExternalAssetRefs(t *testing.T, out string) {
	t.Helper()
	scriptTagRE := regexp.MustCompile(`(?is)<script\b[^>]*>`)
	for _, tag := range scriptTagRE.FindAllString(out, -1) {
		if strings.Contains(strings.ToLower(tag), "src=") {
			t.Fatalf("script tag still has src attribute %q in:\n%s", tag, out)
		}
	}
	for _, tag := range linkTagRE.FindAllString(out, -1) {
		rel, ok := attrValue(tag, "rel")
		if ok && strings.EqualFold(rel, "stylesheet") {
			t.Fatalf("stylesheet link remains %q in:\n%s", tag, out)
		}
	}
}

type closeOnlyFailWriter struct {
	err error
}

func (w closeOnlyFailWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w closeOnlyFailWriter) Close() error {
	return w.err
}
