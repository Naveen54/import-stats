package weight

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writePackageJSON(t *testing.T, root, name, contents string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "node_modules", filepath.FromSlash(name), "package.json"), contents)
}

func TestShakeClassifiesFormats(t *testing.T) {
	root := testRoot(t)
	writePackageJSON(t, root, "module-pkg", `{"module":"dist/index.js"}`)
	writePackageJSON(t, root, "exports-pkg", `{"exports":{".":{"import":"./esm.js","default":"./cjs.js"}}}`)
	writePackageJSON(t, root, "type-pkg", `{"type":"module"}`)
	writePackageJSON(t, root, "dual-pkg", `{"module":"dist/index.mjs","main":"dist/index.cjs"}`)
	writePackageJSON(t, root, "cjs-pkg", `{"main":"index.js"}`)
	writePackageJSON(t, root, "unknown-pkg", `{}`)

	got := Shake(root, []string{"unknown-pkg", "cjs-pkg", "dual-pkg", "type-pkg", "exports-pkg", "module-pkg"}, 3)
	want := []struct {
		name      string
		format    string
		shakeable bool
	}{
		{"module-pkg", "esm", true},
		{"exports-pkg", "esm", true},
		{"type-pkg", "esm", true},
		{"dual-pkg", "dual", true},
		{"cjs-pkg", "cjs", false},
		{"unknown-pkg", "unknown", false},
	}

	for _, want := range want {
		info := got[want.name]
		if info == nil {
			t.Fatalf("%s missing from result", want.name)
		}
		if info.Format != want.format || info.Shakeable != want.shakeable || info.Reason == "" {
			t.Fatalf("%s = %+v, want format %q shakeable %v with reason", want.name, info, want.format, want.shakeable)
		}
	}
	if !got["module-pkg"].HasModule {
		t.Fatalf("module-pkg HasModule = false, want true")
	}
	if !got["exports-pkg"].HasExports {
		t.Fatalf("exports-pkg HasExports = false, want true")
	}
}

func TestShakeSideEffectsShapes(t *testing.T) {
	root := testRoot(t)
	writePackageJSON(t, root, "bool-false", `{"module":"index.js","sideEffects":false}`)
	writePackageJSON(t, root, "bool-true", `{"module":"index.js","sideEffects":true}`)
	writePackageJSON(t, root, "array", `{"module":"index.js","sideEffects":["*.css","polyfill.js"]}`)
	writePackageJSON(t, root, "string-false", `{"module":"index.js","sideEffects":"false"}`)

	got := Shake(root, []string{"bool-false", "bool-true", "array", "string-false"}, 0)
	if !got["bool-false"].SideEffectsFalse {
		t.Fatalf("bool-false SideEffectsFalse = false, want true")
	}
	if got["bool-true"].SideEffectsFalse {
		t.Fatalf("bool-true SideEffectsFalse = true, want false")
	}
	if got["array"].SideEffectsFalse {
		t.Fatalf("array SideEffectsFalse = true, want false")
	}
	if !got["string-false"].SideEffectsFalse {
		t.Fatalf("string-false SideEffectsFalse = false, want true")
	}
}

func TestShakeScopedPackage(t *testing.T) {
	root := testRoot(t)
	writePackageJSON(t, root, "@scope/pkg", `{"module":"index.js"}`)

	got := Shake(root, []string{"@scope/pkg"}, 1)
	if got["@scope/pkg"] == nil || got["@scope/pkg"].Format != "esm" {
		t.Fatalf("scoped package = %#v, want esm", got["@scope/pkg"])
	}
}

func TestShakeMissingAndUnreadableInputs(t *testing.T) {
	root := testRoot(t)
	writePackageJSON(t, root, "present", `{"module":"index.js"}`)
	writePackageJSON(t, root, "malformed", `{`)
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkgjson-dir", "package.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "no-pkgjson"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := Shake(root, []string{"present", "missing", "malformed", "pkgjson-dir", "no-pkgjson"}, 4)
	if _, ok := got["missing"]; ok {
		t.Fatalf("missing package present in result: %#v", got)
	}
	for _, name := range []string{"malformed", "pkgjson-dir", "no-pkgjson"} {
		if got[name] == nil || got[name].Format != "unknown" || got[name].Shakeable {
			t.Fatalf("%s = %#v, want unknown unshakeable", name, got[name])
		}
	}
	if got["present"] == nil || got["present"].Format != "esm" {
		t.Fatalf("present = %#v, want esm", got["present"])
	}
}

func TestShakeMissingNodeModules(t *testing.T) {
	root := testRoot(t)

	got := Shake(root, []string{"react"}, 0)
	if len(got) != 0 {
		t.Fatalf("Shake with missing node_modules = %#v, want empty", got)
	}
}

func TestShakeExportsShapes(t *testing.T) {
	root := testRoot(t)
	writePackageJSON(t, root, "plain-string", `{"exports":"./index.js"}`)
	writePackageJSON(t, root, "deep-import", `{"exports":{"./feature":{"node":{"development":{"import":"./feature.mjs"},"require":"./feature.cjs"}}}}`)

	got := Shake(root, []string{"plain-string", "deep-import"}, 2)
	if got["plain-string"] == nil || got["plain-string"].Format != "unknown" || !got["plain-string"].HasExports {
		t.Fatalf("plain-string = %#v, want unknown with HasExports", got["plain-string"])
	}
	if got["deep-import"] == nil || got["deep-import"].Format != "dual" || !got["deep-import"].Shakeable {
		t.Fatalf("deep-import = %#v, want dual shakeable", got["deep-import"])
	}
}

func TestShakeDeterministicAndConcurrent(t *testing.T) {
	root := testRoot(t)
	writePackageJSON(t, root, "a", `{"module":"index.js","sideEffects":false}`)
	writePackageJSON(t, root, "b", `{"main":"index.js"}`)
	writePackageJSON(t, root, "c", `{"exports":{".":{"import":"./index.mjs","require":"./index.cjs"}}}`)

	names := []string{"c", "b", "a", "missing", "a"}
	first := Shake(root, names, 1)
	second := Shake(root, names, 1)
	concurrent := Shake(root, names, 8)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Shake not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if !reflect.DeepEqual(first, concurrent) {
		t.Fatalf("Shake differs by worker count:\none:   %#v\neight: %#v", first, concurrent)
	}
}

func TestShakeRealDataSanity(t *testing.T) {
	root := os.Getenv("IMPORTSTATS_REAL_ROOT")
	if root == "" {
		t.Skip("set IMPORTSTATS_REAL_ROOT to run real-data sanity check")
	}
	names := []string{"react", "lodash", "antd", "moment", "@ant-design/icons"}
	got := Shake(root, names, 4)

	ordered := append([]string(nil), names...)
	sort.Strings(ordered)
	for _, name := range ordered {
		if info := got[name]; info != nil {
			fmt.Printf("%s: format=%s shakeable=%v sideEffectsFalse=%v reason=%s\n", name, info.Format, info.Shakeable, info.SideEffectsFalse, info.Reason)
		} else {
			fmt.Printf("%s: missing\n", name)
		}
	}
	if got["lodash"] == nil || got["lodash"].Format != "cjs" || got["lodash"].Shakeable {
		t.Fatalf("lodash = %#v, want cjs and not shakeable", got["lodash"])
	}
	if got["antd"] == nil || !got["antd"].Shakeable {
		t.Fatalf("antd = %#v, want shakeable", got["antd"])
	}
}
