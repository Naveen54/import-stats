package weight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"importstats/internal/analyzer"
)

type shakeResult struct {
	name string
	info *analyzer.ShakeInfo
	ok   bool
}

// Shake inspects node_modules/<name>/package.json for each name and reports
// how tree-shakeable each package is. Missing packages are simply absent
// from the result.
func Shake(root string, names []string, workers int) map[string]*analyzer.ShakeInfo {
	out := map[string]*analyzer.ShakeInfo{}
	nodeModules := filepath.Join(root, "node_modules")
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

	results := shakePackages(nodeModules, uniq, workers)
	for _, r := range results {
		if r.ok {
			out[r.name] = r.info
		}
	}
	return out
}

func shakePackages(nodeModules string, names []string, workers int) []shakeResult {
	results := make([]shakeResult, len(names))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				name := names[i]
				info, ok := shakePackage(packagePath(nodeModules, name))
				results[i] = shakeResult{name: name, info: info, ok: ok}
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

func shakePackage(path string) (*analyzer.ShakeInfo, bool) {
	if path == "" {
		return nil, false
	}
	if !packagePresent(path) {
		return nil, false
	}

	raw, err := os.ReadFile(filepath.Join(path, "package.json"))
	if err != nil {
		return unknownShakeInfo(), true
	}
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return unknownShakeInfo(), true
	}
	return classifyShake(pkg), true
}

func packagePresent(path string) bool {
	info, err := os.Stat(path)
	if err == nil {
		return info.IsDir()
	}
	if _, lstatErr := os.Lstat(path); lstatErr == nil {
		return true
	}
	return false
}

func unknownShakeInfo() *analyzer.ShakeInfo {
	return &analyzer.ShakeInfo{
		Format:    "unknown",
		Shakeable: false,
		Reason:    "package metadata could not be read — tree-shaking support is unknown",
	}
}

func classifyShake(pkg map[string]any) *analyzer.ShakeInfo {
	_, hasModule := pkg["module"]
	exports, hasExports := pkg["exports"]
	hasImport, hasRequire := false, false
	if hasExports {
		hasImport, hasRequire = scanExportConditions(exports)
	}

	typeModule := stringField(pkg, "type") == "module"
	hasMain := stringField(pkg, "main") != ""
	sideEffectsFalse := sideEffectsIsFalse(pkg["sideEffects"])

	shipsESM := hasModule || hasImport || typeModule
	shipsCJS := hasMain || hasRequire

	format := "unknown"
	switch {
	case shipsESM && shipsCJS:
		format = "dual"
	case shipsESM:
		format = "esm"
	case shipsCJS:
		format = "cjs"
	}

	return &analyzer.ShakeInfo{
		Format:           format,
		HasModule:        hasModule,
		HasExports:       hasExports,
		SideEffectsFalse: sideEffectsFalse,
		Shakeable:        shipsESM,
		Reason:           shakeReason(format, hasModule, hasImport, typeModule, sideEffectsFalse),
	}
}

func stringField(pkg map[string]any, key string) string {
	v, ok := pkg[key].(string)
	if !ok {
		return ""
	}
	return v
}

func sideEffectsIsFalse(v any) bool {
	switch x := v.(type) {
	case bool:
		return !x
	case string:
		return x == "false"
	default:
		return false
	}
}

func scanExportConditions(v any) (bool, bool) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		hasImport, hasRequire := false, false
		for _, k := range keys {
			if k == "import" {
				hasImport = true
			}
			if k == "require" {
				hasRequire = true
			}
			childImport, childRequire := scanExportConditions(x[k])
			hasImport = hasImport || childImport
			hasRequire = hasRequire || childRequire
		}
		return hasImport, hasRequire
	case []any:
		hasImport, hasRequire := false, false
		for _, item := range x {
			childImport, childRequire := scanExportConditions(item)
			hasImport = hasImport || childImport
			hasRequire = hasRequire || childRequire
		}
		return hasImport, hasRequire
	default:
		return false, false
	}
}

func shakeReason(format string, hasModule, hasImport, typeModule, sideEffectsFalse bool) string {
	switch format {
	case "dual":
		if sideEffectsFalse {
			return "ships both ESM and CommonJS and declares no side effects — unused exports can be dropped"
		}
		return "ships both ESM and CommonJS — bundlers can prefer the ESM entry for tree-shaking"
	case "esm":
		source := esmSource(hasModule, hasImport, typeModule)
		if sideEffectsFalse {
			return "ships ESM via " + source + " and declares no side effects — unused exports can be dropped"
		}
		return "ships ESM via " + source + " — bundlers can drop unused exports"
	case "cjs":
		return "CommonJS only — bundlers cannot drop unused exports"
	default:
		return "no ESM or CommonJS entry point was declared — tree-shaking support is unknown"
	}
}

func esmSource(hasModule, hasImport, typeModule bool) string {
	var sources []string
	if hasModule {
		sources = append(sources, "\"module\"")
	}
	if hasImport {
		sources = append(sources, "\"exports.import\"")
	}
	if typeModule {
		sources = append(sources, "\"type: module\"")
	}
	sort.Strings(sources)
	if len(sources) == 0 {
		return "package metadata"
	}
	if len(sources) == 1 {
		return sources[0]
	}
	return joinSentence(sources)
}

func joinSentence(parts []string) string {
	if len(parts) == 2 {
		return parts[0] + " and " + parts[1]
	}
	out := ""
	for i, part := range parts {
		if i > 0 {
			if i == len(parts)-1 {
				out += ", and "
			} else {
				out += ", "
			}
		}
		out += part
	}
	return out
}
