package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxWorkspaceWalkLevels = 64
	maxDoubleStarDepth     = 8
)

// Workspace describes a monorepo workspace discovered above the project.
type Workspace struct {
	Root           string    // absolute path of the workspace root
	Manager        string    // "npm" | "yarn" | "pnpm" | "lerna"
	Packages       []Package // every local package, sorted by name
	DuplicateNames []string  // duplicate package names skipped after the first directory-sorted match
}

// Package is one local package in the workspace.
type Package struct {
	Name string // package.json "name", e.g. "@acme/ui-kit"
	Dir  string // absolute directory
	Main string // absolute entry file if one can be determined, else ""
}

// DetectWorkspace walks up from dir looking for a workspace root. A project
// that is not in a workspace yields (nil, nil) — that is not an error.
func DetectWorkspace(dir string) (*Workspace, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		abs = filepath.Dir(abs)
	}

	for levels := 0; levels < maxWorkspaceWalkLevels; levels++ {
		if patterns := readPNPMWorkspacePatterns(abs); len(patterns) > 0 {
			return buildWorkspace(abs, "pnpm", patterns)
		}
		if patterns, manager, ok := readPackageWorkspacePatterns(abs); ok {
			return buildWorkspace(abs, manager, patterns)
		}
		if patterns, ok := readLernaWorkspacePatterns(abs); ok {
			return buildWorkspace(abs, "lerna", patterns)
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return nil, nil
}

func buildWorkspace(root, manager string, patterns []string) (*Workspace, error) {
	dirs, err := expandWorkspacePatterns(root, patterns)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	duplicateSet := make(map[string]bool)
	packages := make([]Package, 0, len(dirs))
	for _, dir := range dirs {
		pkg, ok := readWorkspacePackage(dir)
		if !ok {
			continue
		}
		if seen[pkg.Name] {
			duplicateSet[pkg.Name] = true
			continue
		}
		seen[pkg.Name] = true
		packages = append(packages, pkg)
	}
	sort.SliceStable(packages, func(i, j int) bool {
		if packages[i].Name == packages[j].Name {
			return packages[i].Dir < packages[j].Dir
		}
		return packages[i].Name < packages[j].Name
	})

	duplicates := make([]string, 0, len(duplicateSet))
	for name := range duplicateSet {
		duplicates = append(duplicates, name)
	}
	sort.Strings(duplicates)

	return &Workspace{Root: root, Manager: manager, Packages: packages, DuplicateNames: duplicates}, nil
}

func readPNPMWorkspacePatterns(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		return nil
	}
	return parsePNPMWorkspacePatterns(string(b))
}

func parsePNPMWorkspacePatterns(data string) []string {
	var patterns []string
	inPackages := false
	packagesIndent := 0

	for _, raw := range strings.Split(data, "\n") {
		line := stripYAMLComment(strings.TrimRight(raw, "\r"))
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := leadingIndent(line)

		if !inPackages {
			if trimmed == "packages:" {
				inPackages = true
				packagesIndent = indent
			}
			continue
		}

		if indent <= packagesIndent && !strings.HasPrefix(trimmed, "-") {
			break
		}
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		item = strings.TrimSpace(stripYAMLComment(item))
		item = stripMatchingQuotes(item)
		if item != "" {
			patterns = append(patterns, item)
		}
	}
	return patterns
}

func stripYAMLComment(s string) string {
	var quote rune
	escaped := false
	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '\'', '"':
			if quote == 0 {
				quote = r
			} else if quote == r {
				quote = 0
			}
		case '#':
			if quote == 0 {
				return s[:i]
			}
		}
	}
	return s
}

func leadingIndent(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

func stripMatchingQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	return s
}

func readPackageWorkspacePatterns(root string) ([]string, string, bool) {
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil, "", false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, "", false
	}
	workspaces, ok := raw["workspaces"]
	if !ok {
		return nil, "", false
	}

	var array []string
	if err := json.Unmarshal(workspaces, &array); err == nil {
		return array, packageWorkspaceManager(root, raw, false), true
	}

	var object struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(workspaces, &object); err == nil && object.Packages != nil {
		return object.Packages, packageWorkspaceManager(root, raw, true), true
	}
	return nil, "", false
}

func packageWorkspaceManager(root string, raw map[string]json.RawMessage, objectForm bool) string {
	var packageManager string
	if b, ok := raw["packageManager"]; ok && json.Unmarshal(b, &packageManager) == nil {
		if strings.HasPrefix(packageManager, "yarn@") {
			return "yarn"
		}
	}
	if _, err := os.Stat(filepath.Join(root, "yarn.lock")); err == nil {
		return "yarn"
	}
	if objectForm {
		return "yarn"
	}
	return "npm"
}

func readLernaWorkspacePatterns(root string) ([]string, bool) {
	b, err := os.ReadFile(filepath.Join(root, "lerna.json"))
	if err != nil {
		return nil, false
	}
	var config struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(b, &config); err != nil || config.Packages == nil {
		return nil, false
	}
	return config.Packages, true
}

func expandWorkspacePatterns(root string, patterns []string) ([]string, error) {
	include := make(map[string]bool)
	var excludePatterns []string
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, "!") {
			excludePatterns = append(excludePatterns, strings.TrimSpace(strings.TrimPrefix(pattern, "!")))
			continue
		}
		matches, err := expandWorkspacePattern(root, pattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			include[match] = true
		}
	}

	for _, pattern := range excludePatterns {
		matches, err := expandWorkspacePattern(root, pattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			delete(include, match)
		}
	}

	dirs := make([]string, 0, len(include))
	for dir := range include {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func expandWorkspacePattern(root, pattern string) ([]string, error) {
	pattern = filepath.Clean(filepath.FromSlash(pattern))
	if pattern == "." {
		pattern = ""
	}
	if filepath.IsAbs(pattern) {
		if rel, err := filepath.Rel(root, pattern); err == nil && !strings.HasPrefix(rel, "..") {
			pattern = rel
		}
	}
	var parts []string
	if pattern != "" {
		parts = strings.Split(pattern, string(filepath.Separator))
	}
	var matches []string
	var walk func(string, int, int) error
	walk = func(current string, idx, doubleStarDepth int) error {
		if isNodeModulesPath(root, current) {
			return nil
		}
		if idx == len(parts) {
			if isRealDir(current) {
				matches = append(matches, current)
			}
			return nil
		}

		part := parts[idx]
		if part == "**" {
			if err := walk(current, idx+1, 0); err != nil {
				return err
			}
			if doubleStarDepth >= maxDoubleStarDepth {
				return nil
			}
			entries, err := os.ReadDir(current)
			if err != nil {
				return nil
			}
			for _, entry := range entries {
				if entry.Name() == "node_modules" || !entry.IsDir() {
					continue
				}
				next := filepath.Join(current, entry.Name())
				if !isRealDir(next) {
					continue
				}
				if err := walk(next, idx, doubleStarDepth+1); err != nil {
					return err
				}
			}
			return nil
		}

		if hasGlobMeta(part) {
			entries, err := os.ReadDir(current)
			if err != nil {
				return nil
			}
			for _, entry := range entries {
				if entry.Name() == "node_modules" || !entry.IsDir() {
					continue
				}
				ok, err := filepath.Match(part, entry.Name())
				if err != nil || !ok {
					continue
				}
				next := filepath.Join(current, entry.Name())
				if !isRealDir(next) {
					continue
				}
				if err := walk(next, idx+1, 0); err != nil {
					return err
				}
			}
			return nil
		}

		if part == "node_modules" {
			return nil
		}
		next := filepath.Join(current, part)
		if !isRealDir(next) {
			return nil
		}
		return walk(next, idx+1, 0)
	}
	if err := walk(root, 0, 0); err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func isRealDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func isNodeModulesPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "node_modules" {
			return true
		}
	}
	return false
}

type workspacePackageJSON struct {
	Name    string          `json:"name"`
	Main    string          `json:"main"`
	Module  string          `json:"module"`
	Exports json.RawMessage `json:"exports"`
}

func readWorkspacePackage(dir string) (Package, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return Package{}, false
	}
	var pkgJSON workspacePackageJSON
	if err := json.Unmarshal(b, &pkgJSON); err != nil || pkgJSON.Name == "" {
		return Package{}, false
	}
	return Package{Name: pkgJSON.Name, Dir: dir, Main: determinePackageMain(dir, pkgJSON)}, true
}

func determinePackageMain(dir string, pkgJSON workspacePackageJSON) string {
	for _, candidate := range packageJSONEntryCandidates(pkgJSON) {
		if path := existingEntryPath(dir, candidate); path != "" {
			return path
		}
	}
	for _, base := range []string{"src/index", "index"} {
		for _, ext := range entryExtensions() {
			path := filepath.Join(dir, filepath.FromSlash(base)+ext)
			if isRegularFile(path) {
				return path
			}
		}
	}
	return ""
}

func packageJSONEntryCandidates(pkgJSON workspacePackageJSON) []string {
	var candidates []string
	if pkgJSON.Main != "" {
		candidates = append(candidates, pkgJSON.Main)
	}
	if pkgJSON.Module != "" {
		candidates = append(candidates, pkgJSON.Module)
	}
	if len(pkgJSON.Exports) > 0 && string(pkgJSON.Exports) != "null" {
		var value interface{}
		if json.Unmarshal(pkgJSON.Exports, &value) == nil {
			candidates = append(candidates, exportEntryCandidates(value)...)
		}
	}
	return candidates
}

func exportEntryCandidates(value interface{}) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []interface{}:
		var candidates []string
		for _, item := range v {
			candidates = append(candidates, exportEntryCandidates(item)...)
		}
		return candidates
	case map[string]interface{}:
		var candidates []string
		if dot, ok := v["."]; ok {
			candidates = append(candidates, exportEntryCandidates(dot)...)
			return candidates
		}
		for _, key := range []string{"import", "require", "default", "browser", "node"} {
			if item, ok := v[key]; ok {
				candidates = append(candidates, exportEntryCandidates(item)...)
			}
		}
		if len(candidates) > 0 {
			return candidates
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			candidates = append(candidates, exportEntryCandidates(v[key])...)
		}
		return candidates
	}
	return nil
}

func existingEntryPath(dir, candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	path := filepath.Join(dir, filepath.FromSlash(candidate))
	if isRegularFile(path) {
		return path
	}
	if filepath.Ext(path) == "" {
		for _, ext := range entryExtensions() {
			if isRegularFile(path + ext) {
				return path + ext
			}
		}
	}
	if isRealDir(path) {
		for _, ext := range entryExtensions() {
			index := filepath.Join(path, "index"+ext)
			if isRegularFile(index) {
				return index
			}
		}
	}
	return ""
}

func entryExtensions() []string {
	return []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts"}
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
