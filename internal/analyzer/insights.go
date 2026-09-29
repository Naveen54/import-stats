package analyzer

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	insightDeepImport       = "deep-import"
	insightBarrel           = "barrel"
	insightDuplicatePurpose = "duplicate-purpose"
)

type duplicateGroup struct {
	Name    string
	Members []string
}

var duplicatePurposeGroups = []duplicateGroup{
	{Name: "css-in-js", Members: []string{"@emotion/react", "emotion", "jss", "styled-components"}},
	{Name: "date/time", Members: []string{"date-fns", "dayjs", "js-joda", "luxon", "moment"}},
	{Name: "forms", Members: []string{"formik", "react-hook-form", "redux-form"}},
	{Name: "http", Members: []string{"axios", "got", "ky", "node-fetch", "superagent"}},
	{Name: "i18n", Members: []string{"i18next", "polyglot", "react-intl"}},
	{Name: "immutability", Members: []string{"immer", "immutable", "seamless-immutable"}},
	{Name: "state", Members: []string{"jotai", "mobx", "recoil", "redux", "zustand"}},
	{Name: "utility", Members: []string{"lodash", "lodash-es", "ramda", "underscore"}},
	{Name: "uuid", Members: []string{"cuid", "nanoid", "shortid", "uuid"}},
}

// BuildInsights derives advisory insights from a finished report.
func BuildInsights(rep *Report) []Insight {
	if rep == nil {
		return nil
	}

	var out []Insight
	out = append(out, buildDeepImportInsights(rep.Packages)...)
	out = append(out, buildBarrelInsights(rep.InternalFiles)...)
	out = append(out, buildDuplicatePurposeInsights(rep.Packages)...)

	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func buildDeepImportInsights(packages []PackageStat) []Insight {
	var out []Insight
	for _, ps := range packages {
		if ps.DepType == "builtin" || len(ps.Subpaths) == 0 {
			continue
		}

		var evidence []string
		var symbols []string
		for _, site := range ps.Sites {
			if site.Spec != ps.Name {
				continue
			}
			named := namedSymbols(site.Symbols)
			if len(named) == 0 {
				continue
			}
			symbols = append(symbols, named...)
			evidence = append(evidence, fmt.Sprintf("%s:%d", site.File, site.Line))
		}
		evidence = sortedUniqueStrings(evidence)
		if len(evidence) == 0 {
			continue
		}
		if len(evidence) > 10 {
			evidence = evidence[:10]
		}

		symbols = sortedUniqueStrings(symbols)
		subpaths := append([]string(nil), ps.Subpaths...)
		sort.Strings(subpaths)
		out = append(out, Insight{
			ID:       insightDeepImport,
			Severity: "info",
			Subject:  ps.Name,
			Message: fmt.Sprintf("import { %s } from '%s' pulls the whole package; `%s/%s` is already used elsewhere in this project",
				symbols[0], ps.Name, ps.Name, subpaths[0]),
			Evidence: evidence,
		})
	}
	return out
}

func namedSymbols(symbols []string) []string {
	var out []string
	for _, sym := range symbols {
		if strings.HasPrefix(sym, "default:") || strings.HasPrefix(sym, "*:") {
			continue
		}
		out = append(out, sym)
	}
	sort.Strings(out)
	return out
}

func buildBarrelInsights(files []FileStat) []Insight {
	var out []Insight
	for _, fs := range files {
		// Report records where a file is imported from, not the imports made by
		// that file, so export-from dominance is unavailable here. Fan-in/fan-out
		// on index files is the stable proxy for a likely barrel.
		if !isIndexFile(fs.File) || fs.FanOut < 5 || fs.FanIn < 3 {
			continue
		}
		out = append(out, Insight{
			ID:       insightBarrel,
			Severity: "info",
			Subject:  fs.File,
			Message:  fmt.Sprintf("%s looks like a barrel file with fan-out %d and fan-in %d; importing through it can pull in all re-exported modules", fs.File, fs.FanOut, fs.FanIn),
		})
	}
	return out
}

func isIndexFile(file string) bool {
	base := path.Base(strings.ReplaceAll(file, "\\", "/"))
	ext := path.Ext(base)
	return ext != "" && strings.TrimSuffix(base, ext) == "index"
}

func buildDuplicatePurposeInsights(packages []PackageStat) []Insight {
	byName := make(map[string]PackageStat, len(packages))
	for _, ps := range packages {
		byName[ps.Name] = ps
	}

	var out []Insight
	for _, group := range duplicatePurposeGroups {
		var present []PackageStat
		for _, member := range group.Members {
			if ps, ok := byName[member]; ok {
				present = append(present, ps)
			}
		}
		if len(present) < 2 {
			continue
		}

		sort.Slice(present, func(i, j int) bool {
			if present[i].Imports != present[j].Imports {
				return present[i].Imports > present[j].Imports
			}
			if present[i].Files != present[j].Files {
				return present[i].Files > present[j].Files
			}
			return present[i].Name < present[j].Name
		})

		names := make([]string, 0, len(present))
		evidence := make([]string, 0, len(present))
		for _, ps := range present {
			names = append(names, ps.Name)
			evidence = append(evidence, fmt.Sprintf("%s — %d imports across %d files", ps.Name, ps.Imports, ps.Files))
		}
		out = append(out, Insight{
			ID:       insightDuplicatePurpose,
			Severity: "warn",
			Subject:  group.Name,
			Message:  fmt.Sprintf("Multiple %s packages are in use: %s", group.Name, strings.Join(names, ", ")),
			Evidence: evidence,
		})
	}
	return out
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:0]
	var prev string
	for i, value := range values {
		if i == 0 || value != prev {
			out = append(out, value)
			prev = value
		}
	}
	return out
}
