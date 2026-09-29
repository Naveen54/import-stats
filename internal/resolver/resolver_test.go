package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) (string, *Resolver) {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"src/index.js",
		"src/app/App.jsx",
		"src/common/analytics/index.js",
		"src/common/client/qsGateway.ts",
		"src/pages/Home/index.tsx",
		"src/styles/main.scss",
		"src/icons/logo.svg",
		"src/data/config.json",
		"config/index.js",
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("// x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, New(root, filepath.Join(root, "src"), map[string]string{"config": "config"})
}

func TestResolution(t *testing.T) {
	root, r := setup(t)
	entry := filepath.Join(root, "src", "index.js")

	cases := []struct {
		spec     string
		wantType Type
		wantRel  string
		wantPkg  string
	}{
		{"./app/App", TypeLocal, "src/app/App.jsx", ""},
		{"common/analytics", TypeLocal, "src/common/analytics/index.js", ""},
		{"common/client/qsGateway", TypeLocal, "src/common/client/qsGateway.ts", ""},
		{"pages/Home", TypeLocal, "src/pages/Home/index.tsx", ""},
		{"./styles/main.scss", TypeStyle, "src/styles/main.scss", ""},
		{"icons/logo.svg", TypeAsset, "src/icons/logo.svg", ""},
		{"data/config.json", TypeJSONFile, "src/data/config.json", ""},
		{"config", TypeLocal, "config/index.js", ""},
		{"react", TypePackage, "", "react"},
		{"@scope/pkg/sub/path", TypePackage, "", "@scope/pkg"},
		{"path", TypeBuiltin, "", "path"},
		{"fs/promises", TypeBuiltin, "", "fs/promises"},
		{"assert/strict", TypeBuiltin, "", "assert/strict"},
		{"timers/promises", TypeBuiltin, "", "timers/promises"},
		{"./missing/file", TypeMissing, "", ""},
	}

	for _, c := range cases {
		got := r.Resolve(c.spec, entry)
		if got.Type != c.wantType {
			t.Errorf("%s: type = %s, want %s", c.spec, got.Type, c.wantType)
			continue
		}
		if c.wantRel != "" {
			want := filepath.Join(root, filepath.FromSlash(c.wantRel))
			if got.Path != want {
				t.Errorf("%s: path = %s, want %s", c.spec, got.Path, want)
			}
		}
		if c.wantPkg != "" && got.Package != c.wantPkg {
			t.Errorf("%s: package = %s, want %s", c.spec, got.Package, c.wantPkg)
		}
	}
}

func TestSubpath(t *testing.T) {
	_, r := setup(t)
	got := r.Resolve("lodash/fp", "/tmp/x.js")
	if got.Package != "lodash" || got.Subpath != "fp" {
		t.Fatalf("got %+v", got)
	}
	got = r.Resolve("@scope/pkg", "/tmp/x.js")
	if got.Package != "@scope/pkg" || got.Subpath != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestLocalWinsOverPackage(t *testing.T) {
	root, r := setup(t)
	// "config" exists both as an alias target and could look like a package.
	got := r.Resolve("config", filepath.Join(root, "src", "index.js"))
	if got.Type != TypeLocal || got.Alias != "config" {
		t.Fatalf("got %+v", got)
	}
}

func TestIsSourceFile(t *testing.T) {
	for _, p := range []string{"a.js", "a.jsx", "a.ts", "a.tsx", "a.mjs", "a.cjs"} {
		if !IsSourceFile(p) {
			t.Errorf("%s should be a source file", p)
		}
	}
	for _, p := range []string{"a.scss", "a.svg", "a.json", "a.png"} {
		if IsSourceFile(p) {
			t.Errorf("%s should not be a source file", p)
		}
	}
}
