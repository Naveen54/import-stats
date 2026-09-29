package weight

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	return root
}

func writeFile(t *testing.T, path, contents string) int64 {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return int64(len(contents))
}

func TestMeasurePlainPackage(t *testing.T) {
	root := testRoot(t)
	wantSize := writeFile(t, filepath.Join(root, "node_modules", "plain", "index.js"), "abc")
	wantSize += writeFile(t, filepath.Join(root, "node_modules", "plain", "lib", "util.js"), "12345")

	got := Measure(root, []string{"plain"}, 0)
	if got["plain"].SizeBytes != wantSize || got["plain"].FileCount != 2 {
		t.Fatalf("plain info = %+v, want size %d files 2", got["plain"], wantSize)
	}
}

func TestMeasureScopedPackage(t *testing.T) {
	root := testRoot(t)
	wantSize := writeFile(t, filepath.Join(root, "node_modules", "@scope", "name", "index.js"), "scoped")

	got := Measure(root, []string{"@scope/name"}, 1)
	if got["@scope/name"].SizeBytes != wantSize || got["@scope/name"].FileCount != 1 {
		t.Fatalf("scoped info = %+v, want size %d files 1", got["@scope/name"], wantSize)
	}
}

func TestMeasureSkipsNestedNodeModules(t *testing.T) {
	root := testRoot(t)
	wantSize := writeFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "own")
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "node_modules", "dep", "big.js"), "this should not count")

	got := Measure(root, []string{"pkg"}, 0)
	if got["pkg"].SizeBytes != wantSize || got["pkg"].FileCount != 1 {
		t.Fatalf("pkg info = %+v, want size %d files 1", got["pkg"], wantSize)
	}
}

func TestMeasureTransitiveDeps(t *testing.T) {
	root := testRoot(t)
	goodJSON := `{"dependencies":{"a":"1","b":"2"},"devDependencies":{"c":"3"}}`
	writeFile(t, filepath.Join(root, "node_modules", "good", "package.json"), goodJSON)
	writeFile(t, filepath.Join(root, "node_modules", "missing", "index.js"), "x")
	writeFile(t, filepath.Join(root, "node_modules", "malformed", "package.json"), "{")

	got := Measure(root, []string{"good", "missing", "malformed"}, 3)
	if got["good"].TransitiveDeps != 2 {
		t.Fatalf("good deps = %d, want 2", got["good"].TransitiveDeps)
	}
	if got["missing"].TransitiveDeps != 0 {
		t.Fatalf("missing deps = %d, want 0", got["missing"].TransitiveDeps)
	}
	if got["malformed"].TransitiveDeps != 0 {
		t.Fatalf("malformed deps = %d, want 0", got["malformed"].TransitiveDeps)
	}
}

func TestMeasureAbsentPackage(t *testing.T) {
	root := testRoot(t)
	writeFile(t, filepath.Join(root, "node_modules", "present", "index.js"), "x")

	got := Measure(root, []string{"present", "absent"}, 0)
	if _, ok := got["absent"]; ok {
		t.Fatalf("absent package present in result: %#v", got)
	}
	if _, ok := got["present"]; !ok {
		t.Fatalf("present package missing from result: %#v", got)
	}
}

func TestMeasureMissingNodeModules(t *testing.T) {
	root := testRoot(t)

	got := Measure(root, []string{"react"}, 0)
	if len(got) != 0 {
		t.Fatalf("Measure with missing node_modules = %#v, want empty", got)
	}
}

func TestMeasureSymlinkLoop(t *testing.T) {
	root := testRoot(t)
	evil := filepath.Join(root, "node_modules", "evil")
	wantSize := writeFile(t, filepath.Join(evil, "index.js"), "evil")
	if err := os.Symlink(evil, filepath.Join(evil, "self")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	got := Measure(root, []string{"evil"}, 0)
	if got["evil"].SizeBytes != wantSize || got["evil"].FileCount != 1 {
		t.Fatalf("evil info = %+v, want size %d files 1", got["evil"], wantSize)
	}
}

func TestMeasureSymlinkFileOutsideTree(t *testing.T) {
	root := testRoot(t)
	outside := filepath.Join(root, "outside.js")
	writeFile(t, outside, "outside")
	pkg := filepath.Join(root, "node_modules", "linked")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(pkg, "outside.js")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	got := Measure(root, []string{"linked"}, 0)
	if got["linked"].SizeBytes != 0 || got["linked"].FileCount != 0 {
		t.Fatalf("linked info = %+v, want zero", got["linked"])
	}
}

func TestMeasureDeterministic(t *testing.T) {
	root := testRoot(t)
	writeFile(t, filepath.Join(root, "node_modules", "a", "index.js"), "aaa")
	writeFile(t, filepath.Join(root, "node_modules", "b", "package.json"), `{"dependencies":{"x":"1"}}`)
	writeFile(t, filepath.Join(root, "node_modules", "b", "lib", "index.js"), "bbb")

	first := Measure(root, []string{"b", "a", "missing", "a"}, 4)
	second := Measure(root, []string{"b", "a", "missing", "a"}, 4)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Measure not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}
