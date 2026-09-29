package analyzer

import (
	"reflect"
	"testing"
)

func TestBuildInsightsDeepImport(t *testing.T) {
	tests := []struct {
		name string
		rep  *Report
		want []Insight
	}{
		{
			name: "bare named import and subpath usage fires",
			rep: &Report{Packages: []PackageStat{
				{
					Name:     "lodash",
					DepType:  "dependency",
					Subpaths: []string{"throttle", "debounce"},
					Sites: []ImportSite{
						{File: "src/b.js", Line: 5, Spec: "lodash", Symbols: []string{"isEmpty", "debounce"}},
						{File: "src/a.js", Line: 2, Spec: "lodash/debounce", Symbols: []string{"default:debounce"}},
						{File: "src/a.js", Line: 1, Spec: "lodash", Symbols: []string{"default:_"}},
					},
				},
			}},
			want: []Insight{{
				ID:       "deep-import",
				Severity: "info",
				Subject:  "lodash",
				Message:  "import { debounce } from 'lodash' pulls the whole package; `lodash/debounce` is already used elsewhere in this project",
				Evidence: []string{"src/b.js:5"},
			}},
		},
		{
			name: "builtin excluded",
			rep: &Report{Packages: []PackageStat{
				{
					Name:     "fs",
					DepType:  "builtin",
					Subpaths: []string{"promises"},
					Sites:    []ImportSite{{File: "src/a.js", Line: 1, Spec: "fs", Symbols: []string{"readFile"}}},
				},
			}},
			want: nil,
		},
		{
			name: "default-only bare import does not fire",
			rep: &Report{Packages: []PackageStat{
				{
					Name:     "lodash",
					DepType:  "dependency",
					Subpaths: []string{"debounce"},
					Sites:    []ImportSite{{File: "src/a.js", Line: 1, Spec: "lodash", Symbols: []string{"default:_"}}},
				},
			}},
			want: nil,
		},
		{
			name: "no subpaths does not fire",
			rep: &Report{Packages: []PackageStat{
				{
					Name:    "lodash",
					DepType: "dependency",
					Sites:   []ImportSite{{File: "src/a.js", Line: 1, Spec: "lodash", Symbols: []string{"debounce"}}},
				},
			}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildInsights(tt.rep); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildInsights() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildInsightsDeepImportEvidenceCapAndSort(t *testing.T) {
	rep := &Report{Packages: []PackageStat{{
		Name:     "lodash",
		DepType:  "dependency",
		Subpaths: []string{"debounce"},
		Sites: []ImportSite{
			{File: "src/z.js", Line: 12, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/a.js", Line: 1, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/c.js", Line: 3, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/b.js", Line: 2, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/e.js", Line: 5, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/d.js", Line: 4, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/g.js", Line: 7, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/f.js", Line: 6, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/i.js", Line: 9, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/h.js", Line: 8, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/k.js", Line: 11, Spec: "lodash", Symbols: []string{"debounce"}},
			{File: "src/j.js", Line: 10, Spec: "lodash", Symbols: []string{"debounce"}},
		},
	}}}

	got := BuildInsights(rep)
	if len(got) != 1 {
		t.Fatalf("insights = %#v, want 1", got)
	}
	wantEvidence := []string{
		"src/a.js:1",
		"src/b.js:2",
		"src/c.js:3",
		"src/d.js:4",
		"src/e.js:5",
		"src/f.js:6",
		"src/g.js:7",
		"src/h.js:8",
		"src/i.js:9",
		"src/j.js:10",
	}
	if !reflect.DeepEqual(got[0].Evidence, wantEvidence) {
		t.Fatalf("evidence = %#v, want %#v", got[0].Evidence, wantEvidence)
	}
}

func TestBuildInsightsBarrel(t *testing.T) {
	tests := []struct {
		name string
		file FileStat
		want []Insight
	}{
		{
			name: "index file with high fan-in and fan-out fires",
			file: FileStat{File: "src/components/index.ts", FanIn: 3, FanOut: 5},
			want: []Insight{{
				ID:       "barrel",
				Severity: "info",
				Subject:  "src/components/index.ts",
				Message:  "src/components/index.ts looks like a barrel file with fan-out 5 and fan-in 3; importing through it can pull in all re-exported modules",
			}},
		},
		{
			name: "non-index file does not fire",
			file: FileStat{File: "src/components/all.ts", FanIn: 10, FanOut: 10},
			want: nil,
		},
		{
			name: "low fan-in does not fire",
			file: FileStat{File: "src/components/index.ts", FanIn: 2, FanOut: 5},
			want: nil,
		},
		{
			name: "low fan-out does not fire",
			file: FileStat{File: "src/components/index.ts", FanIn: 3, FanOut: 4},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &Report{InternalFiles: []FileStat{tt.file}}
			if got := BuildInsights(rep); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildInsights() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildInsightsDuplicatePurpose(t *testing.T) {
	tests := []struct {
		name string
		rep  *Report
		want []Insight
	}{
		{
			name: "date time packages fire",
			rep: &Report{Packages: []PackageStat{
				{Name: "dayjs", Imports: 7, Files: 4},
				{Name: "moment", Imports: 42, Files: 18},
				{Name: "react", Imports: 100, Files: 50},
			}},
			want: []Insight{{
				ID:       "duplicate-purpose",
				Severity: "warn",
				Subject:  "date/time",
				Message:  "Multiple date/time packages are in use: moment, dayjs",
				Evidence: []string{
					"moment — 42 imports across 18 files",
					"dayjs — 7 imports across 4 files",
				},
			}},
		},
		{
			name: "variants fire",
			rep: &Report{Packages: []PackageStat{
				{Name: "lodash-es", Imports: 9, Files: 3},
				{Name: "lodash", Imports: 9, Files: 8},
				{Name: "@emotion/react", Imports: 3, Files: 3},
				{Name: "emotion", Imports: 3, Files: 2},
			}},
			want: []Insight{
				{
					ID:       "duplicate-purpose",
					Severity: "warn",
					Subject:  "css-in-js",
					Message:  "Multiple css-in-js packages are in use: @emotion/react, emotion",
					Evidence: []string{
						"@emotion/react — 3 imports across 3 files",
						"emotion — 3 imports across 2 files",
					},
				},
				{
					ID:       "duplicate-purpose",
					Severity: "warn",
					Subject:  "utility",
					Message:  "Multiple utility packages are in use: lodash, lodash-es",
					Evidence: []string{
						"lodash — 9 imports across 8 files",
						"lodash-es — 9 imports across 3 files",
					},
				},
			},
		},
		{
			name: "single date library does not fire",
			rep: &Report{Packages: []PackageStat{
				{Name: "date-fns", Imports: 5, Files: 2},
				{Name: "react", Imports: 10, Files: 5},
			}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildInsights(tt.rep); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildInsights() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildInsightsDeterminismAndOrder(t *testing.T) {
	rep := &Report{
		Packages: []PackageStat{
			{
				Name:     "lodash",
				Imports:  10,
				Files:    4,
				DepType:  "dependency",
				Subpaths: []string{"debounce"},
				Sites:    []ImportSite{{File: "src/use-lodash.js", Line: 4, Spec: "lodash", Symbols: []string{"debounce"}}},
			},
			{Name: "underscore", Imports: 2, Files: 1, DepType: "dependency"},
		},
		InternalFiles: []FileStat{{File: "src/shared/index.js", FanIn: 3, FanOut: 5}},
	}

	first := BuildInsights(rep)
	second := BuildInsights(rep)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("BuildInsights not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}

	wantOrder := []string{"barrel:src/shared/index.js", "deep-import:lodash", "duplicate-purpose:utility"}
	gotOrder := make([]string, len(first))
	for i, in := range first {
		gotOrder[i] = in.ID + ":" + in.Subject
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("order = %#v, want %#v", gotOrder, wantOrder)
	}
}

func TestBuildInsightsNilAndEmptyReport(t *testing.T) {
	for _, rep := range []*Report{nil, {}} {
		if got := BuildInsights(rep); len(got) != 0 {
			t.Fatalf("BuildInsights(%#v) = %#v, want none", rep, got)
		}
	}
}
