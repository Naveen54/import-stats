package project

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeTSFile(t *testing.T, root, name, body string) string {
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

func TestLoadTSPathsBaseURLOnly(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "tsconfig.json", `{"compilerOptions":{"baseUrl":"src"}}`)
	got, err := LoadTSPaths(filepath.Join(root, "src"), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != filepath.Join(root, "src") {
		t.Fatalf("BaseURL = %q", got.BaseURL)
	}
	if len(got.Aliases) != 0 {
		t.Fatalf("Aliases = %+v, want none", got.Aliases)
	}
}

func TestLoadTSPathsPathsOnly(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "tsconfig.json", `{"compilerOptions":{"paths":{"@/*":["src/*"]}}}`)
	got, err := LoadTSPaths(root, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Alias{{Key: "@", Path: filepath.Join(root, "src")}}
	if !reflect.DeepEqual(got.Aliases, want) {
		t.Fatalf("Aliases = %+v, want %+v", got.Aliases, want)
	}
}

func TestLoadTSPathsBaseURLAndPaths(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "config/tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/app/*"]}}}`)
	got, err := LoadTSPaths(filepath.Join(root, "config", "src"), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Alias{{Key: "@app", Path: filepath.Join(root, "config", "src", "app")}}
	if !reflect.DeepEqual(got.Aliases, want) {
		t.Fatalf("Aliases = %+v, want %+v", got.Aliases, want)
	}
}

func TestLoadTSPathsWildcardExactAndMultiTarget(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "tsconfig.json", `{
		"compilerOptions": {
			"baseUrl": ".",
			"paths": {
				"config": ["src/config/index.ts", "src/config/fallback.ts"],
				"@app/*": ["src/app/*"],
				"@utils/*": ["src/utils/*"]
			}
		}
	}`)
	got, err := LoadTSPaths(root, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Alias{
		{Key: "@app", Path: filepath.Join(root, "src", "app")},
		{Key: "@utils", Path: filepath.Join(root, "src", "utils")},
		{Key: "config", Path: filepath.Join(root, "src", "config", "index.ts")},
	}
	if !reflect.DeepEqual(got.Aliases, want) {
		t.Fatalf("Aliases = %+v, want %+v", got.Aliases, want)
	}
	if got.AliasMap()["config"] != filepath.Join(root, "src", "config", "index.ts") {
		t.Fatalf("AliasMap did not preserve first target: %#v", got.AliasMap())
	}
}

func TestLoadTSPathsExtendsOneAndTwoLevels(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "base/tsconfig.json", `{"compilerOptions":{"baseUrl":"base-src","paths":{"base/*":["lib/*"],"shared/*":["old/*"]}}}`)
	writeTSFile(t, root, "mid/tsconfig.json", `{"extends":"../base/tsconfig","compilerOptions":{"paths":{"mid/*":["mid-src/*"]}}}`)
	writeTSFile(t, root, "app/tsconfig.json", `{"extends":"../mid/tsconfig.json","compilerOptions":{"baseUrl":"app-src","paths":{"shared/*":["new/*"]}}}`)
	got, err := LoadTSPaths(filepath.Join(root, "app"), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Alias{
		{Key: "base", Path: filepath.Join(root, "base", "base-src", "lib")},
		{Key: "mid", Path: filepath.Join(root, "base", "base-src", "mid-src")},
		{Key: "shared", Path: filepath.Join(root, "app", "app-src", "new")},
	}
	if !reflect.DeepEqual(got.Aliases, want) {
		t.Fatalf("Aliases = %+v, want %+v", got.Aliases, want)
	}
}

func TestLoadTSPathsCircularExtendsTerminates(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "a.json", `{"extends":"./b.json"}`)
	writeTSFile(t, root, "b.json", `{"extends":"./a.json"}`)
	writeTSFile(t, root, "tsconfig.json", `{"extends":"./a.json"}`)

	done := make(chan error, 1)
	go func() {
		_, err := LoadTSPaths(root, root)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "circular") {
			t.Fatalf("err = %v, want circular error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoadTSPaths hung on circular extends")
	}
}

func TestLoadTSPathsJSONCTolerance(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "tsconfig.json", `{
		// line comment
		"compilerOptions": {
			"note": "https://example.com//base/*not-comment*",
			"baseUrl": ".",
			"paths": {
				/* block comment */
				"url": ["https://example.com//thing/*"],
				"win": ["C:\\src\\//thing\\/*"],
			},
		},
	}`)
	got, err := LoadTSPaths(root, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Alias{
		{Key: "url", Path: filepath.Join(root, "https:", "example.com", "thing")},
		{Key: "win", Path: filepath.Join(root, "C:\\src\\", "thing\\")},
	}
	if !reflect.DeepEqual(got.Aliases, want) {
		t.Fatalf("Aliases = %#v, want %#v", got.Aliases, want)
	}
}

func TestLoadTSPathsJSConfigFallbackAndTSConfigWins(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "jsconfig.json", `{"compilerOptions":{"paths":{"js/*":["js-src/*"]}}}`)
	got, err := LoadTSPaths(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != filepath.Join(root, "jsconfig.json") || got.Aliases[0].Key != "js" {
		t.Fatalf("got source/aliases = %q/%+v", got.Source, got.Aliases)
	}
	writeTSFile(t, root, "tsconfig.json", `{"compilerOptions":{"paths":{"ts/*":["ts-src/*"]}}}`)
	got, err = LoadTSPaths(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != filepath.Join(root, "tsconfig.json") || got.Aliases[0].Key != "ts" {
		t.Fatalf("got source/aliases = %q/%+v", got.Source, got.Aliases)
	}
}

func TestLoadTSPathsMissingConfig(t *testing.T) {
	root := t.TempDir()
	got, err := LoadTSPaths(filepath.Join(root, "nested"), root)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestLoadTSPathsMalformedJSONNamesFile(t *testing.T) {
	root := t.TempDir()
	path := writeTSFile(t, root, "tsconfig.json", `{"compilerOptions":`)
	_, err := LoadTSPaths(root, root)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name %q", err.Error(), path)
	}
}

func TestLoadTSPathsDeterministic(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "tsconfig.json", `{"compilerOptions":{"paths":{"z/*":["z/*"],"a/*":["a/*"],"m/*":["m/*"]}}}`)
	var first []Alias
	for i := 0; i < 25; i++ {
		got, err := LoadTSPaths(root, root)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = append([]Alias(nil), got.Aliases...)
			continue
		}
		if !reflect.DeepEqual(got.Aliases, first) {
			t.Fatalf("iteration %d aliases = %+v, want %+v", i, got.Aliases, first)
		}
	}
	wantKeys := []string{"a", "m", "z"}
	for i, key := range wantKeys {
		if first[i].Key != key {
			t.Fatalf("aliases not sorted: %+v", first)
		}
	}
}

func TestLoadTSPathsSkipsPackageExtends(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "tsconfig.json", `{"extends":"@tsconfig/react/tsconfig.json","compilerOptions":{"paths":{"local/*":["src/*"]}}}`)
	got, err := LoadTSPaths(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Aliases) != 1 || got.Aliases[0].Key != "local" {
		t.Fatalf("Aliases = %+v", got.Aliases)
	}
}

func TestLoadTSPathsDoesNotWalkAboveProjectRoot(t *testing.T) {
	root := t.TempDir()
	writeTSFile(t, root, "tsconfig.json", `{"compilerOptions":{"paths":{"root/*":["src/*"]}}}`)
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTSPaths(child, child)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestLoadTSPathsNoPanicForEmptyOptions(t *testing.T) {
	root := t.TempDir()
	cases := []string{`{}`, `{"compilerOptions":{}}`, `{"compilerOptions":{"paths":{}}}`}
	for i, body := range cases {
		if err := os.Remove(filepath.Join(root, "tsconfig.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		writeTSFile(t, root, "tsconfig.json", body)
		got, err := LoadTSPaths(root, root)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got == nil {
			t.Fatalf("case %d: got nil", i)
		}
	}
}
