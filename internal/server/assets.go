package server

import (
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Asset returns the embedded dashboard file at name (relative to web/).
func Asset(name string) (string, error) {
	b, err := fs.ReadFile(assets, path.Join("web", name))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Scripts returns every embedded dashboard script other than app.js, sorted by
// name so the standalone export stays deterministic. app.js is excluded because
// callers inline it separately, after these.
func Scripts() (map[string]string, error) {
	entries, err := fs.ReadDir(assets, "web")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == "app.js" || !strings.HasSuffix(n, ".js") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	out := make(map[string]string, len(names))
	for _, n := range names {
		s, err := Asset(n)
		if err != nil {
			return nil, err
		}
		out[n] = s
	}
	return out, nil
}
