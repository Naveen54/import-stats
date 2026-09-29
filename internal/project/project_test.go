package project

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeProjectFile(t *testing.T, root, name, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectExplicitEntryFilePath(t *testing.T) {
	root := t.TempDir()
	entry := writeProjectFile(t, root, "src/custom.js", "")
	writeProjectFile(t, root, "package.json", `{"name":"demo"}`)

	p, err := Detect(entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Entry != entry {
		t.Fatalf("entry = %s, want %s", p.Entry, entry)
	}
	if p.Root != root || p.SrcRoot != filepath.Join(root, "src") {
		t.Fatalf("root/src = %s/%s", p.Root, p.SrcRoot)
	}
}

func TestDetectDirectoryDefaultEntryFallbacks(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{"src index jsx", "src/index.jsx"},
		{"src main tsx", "src/main.tsx"},
		{"root index ts", "index.ts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			entry := writeProjectFile(t, root, tt.file, "")
			p, err := Detect(root, "")
			if err != nil {
				t.Fatal(err)
			}
			if p.Entry != entry {
				t.Fatalf("entry = %s, want %s", p.Entry, entry)
			}
		})
	}
}

func TestDetectDirectoryNoEntryError(t *testing.T) {
	root := t.TempDir()
	_, err := Detect(root, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no entry file found") || !strings.Contains(err.Error(), "pass the entry file explicitly") {
		t.Fatalf("error = %v", err)
	}
}

func TestSrcRootInference(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{"src index", "src/index.js", "src"},
		{"deeply nested in src", "src/app/main.js", "src"},
		{"no src dir", "app/main.js", "app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			entry := writeProjectFile(t, root, tt.file, "")
			writeProjectFile(t, root, "package.json", `{}`)
			p, err := Detect(entry, "")
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(root, filepath.FromSlash(tt.want))
			if p.SrcRoot != want {
				t.Fatalf("srcRoot = %s, want %s", p.SrcRoot, want)
			}
		})
	}
}

func TestSrcRootOverrideWins(t *testing.T) {
	root := t.TempDir()
	entry := writeProjectFile(t, root, "src/index.js", "")
	override := filepath.Join(root, "custom-src")
	p, err := Detect(entry, override)
	if err != nil {
		t.Fatal(err)
	}
	if p.SrcRoot != override {
		t.Fatalf("srcRoot = %s, want override %s", p.SrcRoot, override)
	}
}

func TestProjectRootNearestPackageJSON(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "package.json", `{"name":"outer"}`)
	app := filepath.Join(root, "packages", "app")
	writeProjectFile(t, app, "package.json", `{"name":"inner"}`)
	entry := writeProjectFile(t, app, "src/features/home/index.js", "")

	p, err := Detect(entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != app {
		t.Fatalf("root = %s, want nearest %s", p.Root, app)
	}
	if p.Name != "inner" {
		t.Fatalf("name = %s, want inner", p.Name)
	}
}

func TestPackageJSONParsing(t *testing.T) {
	root := t.TempDir()
	entry := writeProjectFile(t, root, "src/index.js", "")
	pkg := writeProjectFile(t, root, "package.json", `{"name":"demo","dependencies":{"react":"18"},"devDependencies":{"vite":"5"}}`)

	p, err := Detect(entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo" || p.PackageJSON != pkg {
		t.Fatalf("name/packageJSON = %q/%q", p.Name, p.PackageJSON)
	}
	if want := map[string]string{"react": "18"}; !reflect.DeepEqual(p.Deps, want) {
		t.Fatalf("deps = %v, want %v", p.Deps, want)
	}
	if want := map[string]string{"vite": "5"}; !reflect.DeepEqual(p.DevDeps, want) {
		t.Fatalf("devDeps = %v, want %v", p.DevDeps, want)
	}
}

func TestPackageJSONMalformedOrMissingDoesNotError(t *testing.T) {
	for _, tt := range []struct {
		name string
		pkg  *string
	}{
		{"missing", nil},
		{"malformed", ptr(`{"name":`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			entry := writeProjectFile(t, root, "src/index.js", "")
			if tt.pkg != nil {
				writeProjectFile(t, root, "package.json", *tt.pkg)
			}
			p, err := Detect(entry, "")
			if err != nil {
				t.Fatal(err)
			}
			if p.Name != "" || p.Deps != nil || p.DevDeps != nil || p.PackageJSON != "" {
				t.Fatalf("package fields should be empty, got %+v", p)
			}
		})
	}
}

func TestRel(t *testing.T) {
	root := t.TempDir()
	p := &Info{Root: root}
	inside := filepath.Join(root, "src", "app", "index.js")
	if got, want := p.Rel(inside), "src/app/index.js"; got != want {
		t.Fatalf("Rel(inside) = %q, want %q", got, want)
	}
	outsideRoot := t.TempDir()
	outside := filepath.Join(outsideRoot, "elsewhere.js")
	if got, want := p.Rel(outside), filepath.ToSlash(outside); got != want {
		t.Fatalf("Rel(outside) = %q, want %q", got, want)
	}
}

func ptr(s string) *string { return &s }
