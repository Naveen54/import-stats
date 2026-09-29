package analysis

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"package.json": `{"name":"demo","dependencies":{"react":"^18.0.0"}}`,
		"src/index.js": "import React from 'react';\nimport './a';\n",
		"src/a.js":     "import 'lodash';\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRun(t *testing.T) {
	root := fixture(t)
	rep, err := Run(Options{Input: root})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summary.FilesAnalyzed != 2 {
		t.Errorf("filesAnalyzed = %d, want 2", rep.Summary.FilesAnalyzed)
	}
	if rep.Summary.UniquePackages != 2 {
		t.Errorf("uniquePackages = %d, want 2 (react, lodash)", rep.Summary.UniquePackages)
	}
	if _, err := time.Parse(time.RFC3339, rep.Summary.GeneratedAt); err != nil {
		t.Errorf("generatedAt %q is not RFC3339: %v", rep.Summary.GeneratedAt, err)
	}
}

// TestRunPicksUpSourceChanges is the behaviour the dashboard's re-analyse
// button depends on: a second run must reflect edits made after the first.
func TestRunPicksUpSourceChanges(t *testing.T) {
	root := fixture(t)
	opts := Options{Input: root}

	first, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}

	newFile := filepath.Join(root, "src", "b.js")
	if err := os.WriteFile(newFile, []byte("import 'axios';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "src", "index.js")
	if err := os.WriteFile(entry, []byte("import React from 'react';\nimport './a';\nimport './b';\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}

	if second.Summary.FilesAnalyzed != first.Summary.FilesAnalyzed+1 {
		t.Errorf("filesAnalyzed = %d, want %d after adding a file",
			second.Summary.FilesAnalyzed, first.Summary.FilesAnalyzed+1)
	}
	var found bool
	for _, p := range second.Packages {
		if p.Name == "axios" {
			found = true
		}
	}
	if !found {
		t.Error("axios from the newly added file was not picked up by the second run")
	}
	if second.Summary.GeneratedAt == "" {
		t.Error("second run has no timestamp")
	}
}

func TestRunErrorsOnMissingEntry(t *testing.T) {
	if _, err := Run(Options{Input: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("expected an error for a missing entry")
	}
}
