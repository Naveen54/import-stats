// Package project detects the entry file, src root and project root without
// reading any build-tool configuration.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Info describes the analysed project layout.
type Info struct {
	Entry       string            `json:"entry"`
	SrcRoot     string            `json:"srcRoot"`
	Root        string            `json:"root"`
	Name        string            `json:"name,omitempty"`
	Deps        map[string]string `json:"dependencies,omitempty"`
	DevDeps     map[string]string `json:"devDependencies,omitempty"`
	PeerDeps    map[string]string `json:"peerDependencies,omitempty"`
	OptDeps     map[string]string `json:"optionalDependencies,omitempty"`
	PackageJSON string            `json:"packageJson,omitempty"`
}

// Detect resolves the entry file and project layout.
//
// input may be an entry file or a directory; when empty the current directory
// is used. For a directory, src/index.js (then other src/index.* and
// ./index.*) is tried. srcRootFlag overrides src-root inference.
func Detect(input, srcRootFlag string) (*Info, error) {
	if input == "" {
		input = "."
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}

	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot access %s: %w", input, err)
	}

	entry := abs
	if st.IsDir() {
		entry, err = defaultEntry(abs)
		if err != nil {
			return nil, err
		}
	}

	p := &Info{Entry: entry}
	p.Root = findProjectRoot(entry)
	p.SrcRoot = inferSrcRoot(entry, p.Root, srcRootFlag)
	p.loadPackageJSON()
	return p, nil
}

func defaultEntry(dir string) (string, error) {
	var candidates []string
	for _, base := range []string{"src/index", "src/main", "index", "main"} {
		for _, ext := range []string{".js", ".jsx", ".ts", ".tsx"} {
			candidates = append(candidates, filepath.Join(dir, filepath.FromSlash(base)+ext))
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("no entry file found in %s (looked for src/index.js and similar); pass the entry file explicitly", dir)
}

// findProjectRoot walks up from the entry to the nearest directory holding a
// package.json, falling back to the parent of src.
func findProjectRoot(entry string) string {
	dir := filepath.Dir(entry)
	for {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	d := filepath.Dir(entry)
	if filepath.Base(d) == "src" {
		return filepath.Dir(d)
	}
	return d
}

// inferSrcRoot picks the "src" directory on the entry's path, else the entry's
// own directory. An explicit flag always wins.
func inferSrcRoot(entry, root, flag string) string {
	if flag != "" {
		if abs, err := filepath.Abs(flag); err == nil {
			return abs
		}
	}
	dir := filepath.Dir(entry)
	probe := dir
	for {
		if filepath.Base(probe) == "src" {
			return probe
		}
		parent := filepath.Dir(probe)
		if parent == probe || len(parent) < len(root) {
			break
		}
		probe = parent
	}
	if st, err := os.Stat(filepath.Join(root, "src")); err == nil && st.IsDir() {
		return filepath.Join(root, "src")
	}
	return dir
}

type pkgJSON struct {
	Name     string            `json:"name"`
	Deps     map[string]string `json:"dependencies"`
	DevDeps  map[string]string `json:"devDependencies"`
	PeerDeps map[string]string `json:"peerDependencies"`
	OptDeps  map[string]string `json:"optionalDependencies"`
}

func (p *Info) loadPackageJSON() {
	path := filepath.Join(p.Root, "package.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pj pkgJSON
	if err := json.Unmarshal(b, &pj); err != nil {
		return
	}
	p.Name = pj.Name
	p.Deps = pj.Deps
	p.DevDeps = pj.DevDeps
	p.PeerDeps = pj.PeerDeps
	p.OptDeps = pj.OptDeps
	p.PackageJSON = path
}

// Rel renders a path relative to the project root for display.
func (p *Info) Rel(path string) string {
	if rel, err := filepath.Rel(p.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
