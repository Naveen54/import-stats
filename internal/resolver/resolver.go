// Package resolver maps import specifiers to files on disk (local modules) or
// to npm package names (external modules). No build-tool config is read: the
// only inputs are the src root, optional explicit aliases, and extension
// probing.
package resolver

import (
	"os"
	"path/filepath"
	"strings"
)

// TryExtensions are probed, in order, for specifiers without an extension.
var TryExtensions = []string{".js", ".jsx", ".ts", ".tsx"}

// IndexFiles are probed when a specifier resolves to a directory.
var IndexFiles = []string{"index.js", "index.jsx", "index.ts", "index.tsx"}

var styleExts = map[string]bool{
	".css": true, ".scss": true, ".sass": true, ".less": true, ".styl": true,
}

var assetExts = map[string]bool{
	".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".avif": true, ".ico": true, ".bmp": true, ".woff": true, ".woff2": true, ".ttf": true,
	".eot": true, ".otf": true, ".mp4": true, ".webm": true, ".mp3": true, ".wav": true,
	".pdf": true, ".csv": true, ".txt": true, ".html": true, ".md": true,
}

// sourceExts are module files whose imports are followed.
var sourceExts = map[string]bool{
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true, ".mts": true, ".cts": true,
}

// IsSourceFile reports whether path is a traversable source module.
func IsSourceFile(path string) bool { return sourceExts[strings.ToLower(filepath.Ext(path))] }

// Type classifies a resolution outcome.
type Type string

const (
	TypeLocal    Type = "local"   // resolved to a file inside the project
	TypeStyle    Type = "style"   // css/scss/less import
	TypeAsset    Type = "asset"   // image/font/json-like asset
	TypePackage  Type = "package" // external npm package
	TypeBuiltin  Type = "builtin" // node builtin module
	TypeMissing  Type = "missing" // looks local but no file found
	TypeDynamic  Type = "dynamic" // non-literal import()/require()
	TypeJSONFile Type = "json"    // .json file
)

// Result describes where a specifier points.
type Result struct {
	Type Type
	// Path is the absolute file path for local/style/asset/json results.
	Path string
	// Package is the npm package name for package results (e.g. "@scope/name").
	Package string
	// Subpath is the part after the package name (e.g. "lib/fp" in "lodash/lib/fp").
	Subpath string
	// Alias records the alias key that was applied, if any.
	Alias string
}

// Resolver resolves specifiers relative to a project.
type Resolver struct {
	SrcRoot     string
	ProjectRoot string
	Aliases     map[string]string // exact key -> absolute path or replacement specifier

	statCache map[string]bool
}

// New builds a resolver. Alias values pointing at existing paths are made
// absolute relative to projectRoot.
func New(projectRoot, srcRoot string, aliases map[string]string) *Resolver {
	r := &Resolver{
		SrcRoot:     srcRoot,
		ProjectRoot: projectRoot,
		Aliases:     map[string]string{},
		statCache:   map[string]bool{},
	}
	for k, v := range aliases {
		if v != "" && !filepath.IsAbs(v) {
			if abs, err := filepath.Abs(filepath.Join(projectRoot, v)); err == nil {
				if _, err := os.Stat(abs); err == nil {
					v = abs
				}
			}
		}
		r.Aliases[k] = v
	}
	return r
}

func (r *Resolver) exists(p string) bool {
	if v, ok := r.statCache[p]; ok {
		return v
	}
	info, err := os.Stat(p)
	ok := err == nil && !info.IsDir()
	r.statCache[p] = ok
	return ok
}

func (r *Resolver) isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Resolve maps a specifier imported from fromFile.
func (r *Resolver) Resolve(specifier, fromFile string) Result {
	if specifier == "" {
		return Result{Type: TypeDynamic}
	}
	if strings.HasPrefix(specifier, "node:") {
		return Result{Type: TypeBuiltin, Package: specifier}
	}

	aliasKey, mapped := r.applyAlias(specifier)

	// Absolute path specifier.
	if filepath.IsAbs(mapped) {
		if res, ok := r.probe(mapped); ok {
			res.Alias = aliasKey
			return res
		}
		return Result{Type: TypeMissing, Path: mapped, Alias: aliasKey}
	}

	// Relative specifier.
	if strings.HasPrefix(mapped, "./") || strings.HasPrefix(mapped, "../") || mapped == "." || mapped == ".." {
		base := filepath.Dir(fromFile)
		if res, ok := r.probe(filepath.Join(base, mapped)); ok {
			res.Alias = aliasKey
			return res
		}
		return Result{Type: TypeMissing, Path: filepath.Join(base, mapped), Alias: aliasKey}
	}

	// Bare specifier: try src-root-relative resolution first (webpack
	// `modules: [src, node_modules]` style), then fall back to a package.
	if r.SrcRoot != "" {
		if res, ok := r.probe(filepath.Join(r.SrcRoot, mapped)); ok {
			res.Alias = aliasKey
			return res
		}
	}

	if builtins[mapped] {
		return Result{Type: TypeBuiltin, Package: mapped, Alias: aliasKey}
	}

	name, sub := SplitPackage(mapped)
	ext := strings.ToLower(filepath.Ext(mapped))
	if sub != "" && (styleExts[ext] || assetExts[ext]) {
		// e.g. "some-pkg/dist/style.css" — still attributed to the package.
		return Result{Type: TypePackage, Package: name, Subpath: sub, Alias: aliasKey}
	}
	return Result{Type: TypePackage, Package: name, Subpath: sub, Alias: aliasKey}
}

// applyAlias rewrites a specifier when it matches an alias key exactly or as a
// path prefix. It returns the matched key (if any) and the rewritten specifier.
func (r *Resolver) applyAlias(spec string) (string, string) {
	if len(r.Aliases) == 0 {
		return "", spec
	}
	if v, ok := r.Aliases[spec]; ok {
		return spec, v
	}
	bestKey, bestVal := "", ""
	for k, v := range r.Aliases {
		if strings.HasPrefix(spec, k+"/") && len(k) > len(bestKey) {
			bestKey, bestVal = k, v
		}
	}
	if bestKey != "" {
		return bestKey, filepath.Join(bestVal, strings.TrimPrefix(spec, bestKey+"/"))
	}
	return "", spec
}

// probe applies extension and index-file probing to a filesystem path.
func (r *Resolver) probe(p string) (Result, bool) {
	p = filepath.Clean(p)
	ext := strings.ToLower(filepath.Ext(p))

	if ext != "" && r.exists(p) {
		return classify(p), true
	}

	// Extensionless (or unknown-extension, e.g. "utils.config") specifier.
	for _, e := range TryExtensions {
		cand := p + e
		if r.exists(cand) {
			return Result{Type: TypeLocal, Path: cand}, true
		}
	}
	if ext != "" && r.exists(p) {
		return classify(p), true
	}
	if r.isDir(p) {
		for _, idx := range IndexFiles {
			cand := filepath.Join(p, idx)
			if r.exists(cand) {
				return Result{Type: TypeLocal, Path: cand}, true
			}
		}
	}
	// Extensions not in the probe list but still on disk (.mjs/.cjs/.json/...).
	for _, e := range []string{".mjs", ".cjs", ".mts", ".cts", ".json"} {
		if r.exists(p + e) {
			return classify(p + e), true
		}
	}
	return Result{}, false
}

func classify(p string) Result {
	ext := strings.ToLower(filepath.Ext(p))
	switch {
	case styleExts[ext]:
		return Result{Type: TypeStyle, Path: p}
	case ext == ".json":
		return Result{Type: TypeJSONFile, Path: p}
	case assetExts[ext]:
		return Result{Type: TypeAsset, Path: p}
	case sourceExts[ext]:
		return Result{Type: TypeLocal, Path: p}
	default:
		return Result{Type: TypeAsset, Path: p}
	}
}

// SplitPackage splits a bare specifier into package name and subpath.
func SplitPackage(spec string) (string, string) {
	parts := strings.Split(spec, "/")
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		name := parts[0] + "/" + parts[1]
		return name, strings.Join(parts[2:], "/")
	}
	return parts[0], strings.Join(parts[1:], "/")
}

// builtins mirrors Node's builtinModules list, with documented builtin
// subpaths that are valid bare specifiers.
var builtins = map[string]bool{
	"_http_agent": true, "_http_client": true, "_http_common": true, "_http_incoming": true,
	"_http_outgoing": true, "_http_server": true, "_stream_duplex": true, "_stream_passthrough": true,
	"_stream_readable": true, "_stream_transform": true, "_stream_wrap": true, "_stream_writable": true,
	"_tls_common": true, "_tls_wrap": true, "assert": true, "assert/strict": true, "async_hooks": true,
	"buffer": true, "child_process": true, "cluster": true, "console": true, "constants": true,
	"crypto": true, "dgram": true, "diagnostics_channel": true, "dns": true, "dns/promises": true,
	"domain": true, "events": true, "fs": true, "fs/promises": true, "http": true, "http2": true,
	"https": true, "inspector": true, "inspector/promises": true, "module": true, "net": true,
	"os": true, "path": true, "path/posix": true, "path/win32": true, "perf_hooks": true,
	"process": true, "punycode": true, "querystring": true, "readline": true, "readline/promises": true,
	"repl": true, "stream": true, "stream/consumers": true, "stream/promises": true, "stream/web": true,
	"string_decoder": true, "sys": true, "test": true, "timers": true, "timers/promises": true,
	"tls": true, "trace_events": true, "tty": true, "url": true, "util": true, "util/types": true,
	"v8": true, "vm": true, "wasi": true, "worker_threads": true, "zlib": true,
}
