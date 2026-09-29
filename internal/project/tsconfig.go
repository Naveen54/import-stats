package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxTSExtendsDepth = 32

// Alias is a resolver-compatible key=path module alias. Key is the import
// prefix matched by the resolver, and Path is the absolute replacement path.
type Alias struct {
	Key  string
	Path string
}

// TSPaths holds module aliases discovered in a tsconfig/jsconfig file.
type TSPaths struct {
	BaseURL string
	Aliases []Alias
	Source  string
}

// AliasMap returns aliases in the map[string]string shape consumed by resolver.New.
func (p *TSPaths) AliasMap() map[string]string {
	if p == nil || len(p.Aliases) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.Aliases))
	for _, a := range p.Aliases {
		out[a.Key] = a.Path
	}
	return out
}

// LoadTSPaths looks for tsconfig.json then jsconfig.json starting at dir and
// walking up to projectRoot, follows relative "extends", and returns aliases
// implied by compilerOptions.baseUrl and compilerOptions.paths.
func LoadTSPaths(dir, projectRoot string) (*TSPaths, error) {
	start, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(start); err == nil && !st.IsDir() {
		start = filepath.Dir(start)
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	start = filepath.Clean(start)
	root = filepath.Clean(root)
	if !sameOrBelow(start, root) {
		return nil, nil
	}

	for cur := start; ; cur = filepath.Dir(cur) {
		for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
			candidate := filepath.Join(cur, name)
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				loaded, err := loadTSConfig(candidate, map[string]bool{}, 0)
				if err != nil {
					return nil, err
				}
				return loaded.toTSPaths(candidate), nil
			}
		}
		if cur == root {
			break
		}
		next := filepath.Dir(cur)
		if next == cur || !sameOrBelow(next, root) {
			break
		}
	}
	return nil, nil
}

type rawTSConfig struct {
	Extends         string              `json:"extends"`
	CompilerOptions *rawCompilerOptions `json:"compilerOptions"`
}

type rawCompilerOptions struct {
	BaseURL *string             `json:"baseUrl"`
	Paths   map[string][]string `json:"paths"`
}

type pathEntry struct {
	targets []string
	baseDir string
}

type loadedTSConfig struct {
	baseURL string
	paths   map[string]pathEntry
}

func loadTSConfig(path string, seen map[string]bool, depth int) (*loadedTSConfig, error) {
	if depth > maxTSExtendsDepth {
		return nil, fmt.Errorf("tsconfig extends chain is too deep at %s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	if seen[abs] {
		return nil, fmt.Errorf("circular tsconfig extends chain involving %s", abs)
	}
	seen[abs] = true
	defer delete(seen, abs)

	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	var raw rawTSConfig
	if err := json.Unmarshal(stripJSONC(b), &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", abs, err)
	}

	loaded := &loadedTSConfig{paths: map[string]pathEntry{}}
	if parentPath, ok := resolveTSExtends(abs, raw.Extends); ok {
		parent, err := loadTSConfig(parentPath, seen, depth+1)
		if err != nil {
			return nil, err
		}
		loaded.baseURL = parent.baseURL
		for k, v := range parent.paths {
			loaded.paths[k] = v
		}
	}

	configDir := filepath.Dir(abs)
	if raw.CompilerOptions == nil {
		return loaded, nil
	}
	if raw.CompilerOptions.BaseURL != nil {
		loaded.baseURL = cleanAbs(configDir, *raw.CompilerOptions.BaseURL)
	}
	if raw.CompilerOptions.Paths != nil {
		baseDir := loaded.baseURL
		if baseDir == "" {
			baseDir = configDir
		}
		for pattern, targets := range raw.CompilerOptions.Paths {
			copyTargets := append([]string(nil), targets...)
			loaded.paths[pattern] = pathEntry{targets: copyTargets, baseDir: baseDir}
		}
	}
	return loaded, nil
}

func (c *loadedTSConfig) toTSPaths(source string) *TSPaths {
	out := &TSPaths{BaseURL: c.baseURL, Source: filepath.Clean(source)}
	patterns := make([]string, 0, len(c.paths))
	for pattern := range c.paths {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		entry := c.paths[pattern]
		if len(entry.targets) == 0 {
			continue
		}
		key, target, ok := aliasPair(pattern, entry.targets[0], entry.baseDir)
		if !ok {
			continue
		}
		out.Aliases = append(out.Aliases, Alias{Key: key, Path: target})
	}
	return out
}

func aliasPair(pattern, target, baseDir string) (string, string, bool) {
	if strings.Count(pattern, "*") > 1 || strings.Count(target, "*") > 1 {
		return "", "", false
	}
	key := strings.TrimSpace(pattern)
	value := strings.TrimSpace(target)
	if key == "" || value == "" {
		return "", "", false
	}
	if strings.Contains(key, "*") {
		key = strings.TrimSuffix(strings.TrimSuffix(key, "*"), "/")
	}
	if strings.Contains(value, "*") {
		value = strings.TrimSuffix(strings.TrimSuffix(value, "*"), "/")
	}
	if key == "" {
		return "", "", false
	}
	return key, cleanAbs(baseDir, value), true
}

func resolveTSExtends(configPath, spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", false
	}
	if !filepath.IsAbs(spec) && !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return "", false
	}
	candidate := spec
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(filepath.Dir(configPath), candidate)
	}
	candidate = filepath.Clean(candidate)
	candidates := []string{candidate}
	if filepath.Ext(candidate) == "" {
		candidates = append(candidates, candidate+".json", filepath.Join(candidate, "tsconfig.json"))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

func cleanAbs(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, filepath.FromSlash(p)))
}

func sameOrBelow(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func stripJSONC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	escaped := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			out = append(out, c)
		case '/':
			if i+1 < len(in) && in[i+1] == '/' {
				out = append(out, ' ', ' ')
				i += 2
				for ; i < len(in); i++ {
					if in[i] == '\n' || in[i] == '\r' {
						out = append(out, in[i])
						break
					}
					out = append(out, ' ')
				}
			} else if i+1 < len(in) && in[i+1] == '*' {
				out = append(out, ' ', ' ')
				i += 2
				for ; i < len(in); i++ {
					if i+1 < len(in) && in[i] == '*' && in[i+1] == '/' {
						out = append(out, ' ', ' ')
						i++
						break
					}
					if in[i] == '\n' || in[i] == '\r' {
						out = append(out, in[i])
					} else {
						out = append(out, ' ')
					}
				}
			} else {
				out = append(out, c)
			}
		default:
			out = append(out, c)
		}
	}
	return stripTrailingJSONCommas(out)
}

func stripTrailingJSONCommas(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	escaped := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				out = append(out, ' ')
				continue
			}
		}
		out = append(out, c)
	}
	return out
}
