// Package weight measures installed npm package disk usage.
package weight

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Info is the measured on-disk cost of one installed package.
type Info struct {
	SizeBytes      int64
	FileCount      int
	TransitiveDeps int // count of entries in that package's own "dependencies"
}

type packageResult struct {
	name string
	info Info
	ok   bool
}

type canonicalInfo struct {
	path string
	info fs.FileInfo
}

type canonicalizer struct {
	paths map[string]string
	infos []canonicalInfo
}

// Measure reports the installed size of each named package under
// <projectRoot>/node_modules. Packages that are not installed are simply
// absent from the result. A missing node_modules directory yields an empty
// map and no error.
func Measure(projectRoot string, names []string, workers int) map[string]Info {
	out := map[string]Info{}
	nodeModules := filepath.Join(projectRoot, "node_modules")
	if info, err := os.Stat(nodeModules); err != nil || !info.IsDir() {
		return out
	}

	uniq := uniqueNames(names)
	if len(uniq) == 0 {
		return out
	}
	if workers <= 0 {
		workers = runtime.NumCPU() * 2
	}
	if workers > len(uniq) {
		workers = len(uniq)
	}

	results := measurePackages(nodeModules, uniq, workers)
	for _, r := range results {
		if r.ok {
			out[r.name] = r.info
		}
	}
	return out
}

func uniqueNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func measurePackages(nodeModules string, names []string, workers int) []packageResult {
	results := make([]packageResult, len(names))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				name := names[i]
				info, ok := measurePackage(packagePath(nodeModules, name))
				results[i] = packageResult{name: name, info: info, ok: ok}
			}
		}()
	}
	for i := range names {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func packagePath(nodeModules, name string) string {
	rel := filepath.Clean(filepath.FromSlash(name))
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.Join(nodeModules, rel)
}

func measurePackage(path string) (Info, bool) {
	if path == "" {
		return Info{}, false
	}
	canon := newCanonicalizer()
	root, err := canon.path(path)
	if err != nil {
		return Info{}, false
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return Info{}, false
	}

	info := walkPackage(root, canon)
	info.TransitiveDeps = dependencyCount(filepath.Join(root, "package.json"))
	return info, true
}

func walkPackage(root string, canon *canonicalizer) Info {
	var out Info
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if tooDeep(path) {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			// Nested installs are dependencies' cost, not this package's own cost.
			if path != root && d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if path != root && canon.seenDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		out.SizeBytes += info.Size()
		out.FileCount++
		return nil
	})
	return out
}

func dependencyCount(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pkg struct {
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return 0
	}
	return len(pkg.Dependencies)
}

func newCanonicalizer() *canonicalizer {
	return &canonicalizer{paths: map[string]string{}}
}

func (c *canonicalizer) path(path string) (string, error) {
	clean := filepath.Clean(path)
	if got, ok := c.paths[clean]; ok {
		return got, nil
	}
	eval, evalErr := filepath.EvalSymlinks(clean)
	if evalErr == nil {
		eval = filepath.Clean(eval)
		if got, ok := c.paths[eval]; ok {
			c.paths[clean] = got
			return got, nil
		}
	}
	statPath := clean
	if evalErr == nil {
		statPath = eval
	}
	info, statErr := os.Stat(statPath)
	if statErr != nil {
		info, statErr = os.Stat(clean)
	}
	if statErr == nil {
		for _, existing := range c.infos {
			if os.SameFile(info, existing.info) {
				c.paths[clean] = existing.path
				if evalErr == nil {
					c.paths[eval] = existing.path
				}
				return existing.path, nil
			}
		}
	}
	canon := clean
	if evalErr == nil {
		canon = eval
	}
	c.paths[clean] = canon
	if evalErr == nil {
		c.paths[eval] = canon
	}
	if statErr == nil {
		c.infos = append(c.infos, canonicalInfo{path: canon, info: info})
	}
	if evalErr != nil {
		return canon, evalErr
	}
	return canon, nil
}

func (c *canonicalizer) seenDir(path string) bool {
	clean := filepath.Clean(path)
	if _, ok := c.paths[clean]; ok {
		return true
	}
	eval, evalErr := filepath.EvalSymlinks(clean)
	if evalErr == nil {
		eval = filepath.Clean(eval)
		if _, ok := c.paths[eval]; ok {
			c.paths[clean] = eval
			return true
		}
	}
	statPath := clean
	if evalErr == nil {
		statPath = eval
	}
	info, statErr := os.Stat(statPath)
	if statErr != nil {
		return false
	}
	for _, existing := range c.infos {
		if os.SameFile(info, existing.info) {
			c.paths[clean] = existing.path
			if evalErr == nil {
				c.paths[eval] = existing.path
			}
			return true
		}
	}
	canon := clean
	if evalErr == nil {
		canon = eval
	}
	c.paths[clean] = canon
	if evalErr == nil {
		c.paths[eval] = canon
	}
	c.infos = append(c.infos, canonicalInfo{path: canon, info: info})
	return false
}

func tooDeep(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	return len(parts) > 256
}
