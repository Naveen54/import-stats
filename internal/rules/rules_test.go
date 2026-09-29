package rules

import (
	"reflect"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{name: "exact", pattern: "src/App.js", path: "src/App.js", want: true},
		{name: "exact differs", pattern: "src/App.js", path: "src/App.test.js", want: false},
		{name: "star within segment", pattern: "src/components/*.js", path: "src/components/Header.js", want: true},
		{name: "star does not cross slash", pattern: "src/components/*.js", path: "src/components/nav/Header.js", want: false},
		{name: "double star crosses segments", pattern: "src/**/*.js", path: "src/components/nav/Header.js", want: true},
		{name: "double star crosses zero segments", pattern: "src/**/Header.js", path: "src/Header.js", want: true},
		{name: "question single char", pattern: "src/pages/Page?.tsx", path: "src/pages/Page1.tsx", want: true},
		{name: "question only one char", pattern: "src/pages/Page?.tsx", path: "src/pages/Page10.tsx", want: false},
		{name: "bare prefix matches child", pattern: "src/components", path: "src/components/Header.js", want: true},
		{name: "bare prefix matches nested child", pattern: "src/components", path: "src/components/nav/Header.js", want: true},
		{name: "bare prefix matches exact", pattern: "src/components", path: "src/components", want: true},
		{name: "bare prefix segment boundary", pattern: "src/components", path: "src/components-old/Header.js", want: false},
		{name: "bare prefix sibling", pattern: "src/components", path: "src/component/Header.js", want: false},
		{name: "leading and trailing slashes", pattern: "/src/components/", path: "src/components/Header.js", want: true},
		{name: "leading dot", pattern: "./src/components", path: "src/components/Header.js", want: true},
		{name: "duplicate slashes", pattern: "src//components", path: "src/components/Header.js", want: true},
		{name: "empty pattern empty path", pattern: "", path: "", want: true},
		{name: "empty pattern non-empty path", pattern: "", path: "src/App.js", want: false},
		{name: "non-empty pattern empty path", pattern: "src/**", path: "", want: false},
		{name: "star can match empty segment text", pattern: "src/*.js", path: "src/.js", want: true},
		{name: "double star all descendants", pattern: "src/**", path: "src/components/Header.js", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.pattern, tt.path); got != tt.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	rules, err := Parse([]string{
		"src/components !-> src/pages",
		" src/pages   -->   src/components ",
		"src/components-old !-> src/pages-old",
	})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	want := []Rule{
		{Raw: "src/components !-> src/pages", From: "src/components", To: "src/pages", Deny: true},
		{Raw: " src/pages   -->   src/components ", From: "src/pages", To: "src/components", Deny: false},
		{Raw: "src/components-old !-> src/pages-old", From: "src/components-old", To: "src/pages-old", Deny: true},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("Parse() = %#v, want %#v", rules, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing operator", raw: "src/components src/pages", want: "missing operator"},
		{name: "empty left", raw: " !-> src/pages", want: "empty pattern"},
		{name: "empty right", raw: "src/components --> ", want: "empty pattern"},
		{name: "unknown operator", raw: "src/components -> src/pages", want: "unknown operator"},
		{name: "empty text", raw: "   ", want: "missing operator"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]string{tt.raw})
			if err == nil {
				t.Fatalf("Parse(%q) returned nil error", tt.raw)
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.raw) || !strings.Contains(msg, tt.want) {
				t.Fatalf("error = %q, want it to contain %q and %q", msg, tt.raw, tt.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	t.Run("deny violation", func(t *testing.T) {
		rules := []Rule{{Raw: "src/components !-> src/pages", From: "src/components", To: "src/pages", Deny: true}}
		edges := []Edge{{From: "src/components/Header.js", To: "src/pages/Home.js", Line: 3}}
		want := []Violation{{Rule: "src/components !-> src/pages", From: "src/components/Header.js", To: "src/pages/Home.js", Line: 3}}
		if got := Check(rules, edges); !reflect.DeepEqual(got, want) {
			t.Fatalf("Check() = %#v, want %#v", got, want)
		}
	})

	t.Run("deny non violation", func(t *testing.T) {
		rules := []Rule{{Raw: "src/components !-> src/pages", From: "src/components", To: "src/pages", Deny: true}}
		edges := []Edge{{From: "src/components/Header.js", To: "src/utils/format.js", Line: 3}}
		if got := Check(rules, edges); len(got) != 0 {
			t.Fatalf("Check() = %#v, want no violations", got)
		}
	})

	t.Run("allow only violation", func(t *testing.T) {
		rules := []Rule{{Raw: "src/pages --> src/components", From: "src/pages", To: "src/components"}}
		edges := []Edge{{From: "src/pages/Home.js", To: "src/services/api.js", Line: 7}}
		want := []Violation{{Rule: "src/pages --> src/components", From: "src/pages/Home.js", To: "src/services/api.js", Line: 7}}
		if got := Check(rules, edges); !reflect.DeepEqual(got, want) {
			t.Fatalf("Check() = %#v, want %#v", got, want)
		}
	})

	t.Run("allow only satisfied", func(t *testing.T) {
		rules := []Rule{{Raw: "src/pages --> src/components", From: "src/pages", To: "src/components"}}
		edges := []Edge{{From: "src/pages/Home.js", To: "src/components/Button.js", Line: 7}}
		if got := Check(rules, edges); len(got) != 0 {
			t.Fatalf("Check() = %#v, want no violations", got)
		}
	})

	t.Run("allow only union", func(t *testing.T) {
		rules := []Rule{
			{Raw: "src/pages --> src/components", From: "src/pages", To: "src/components"},
			{Raw: "src/pages --> src/utils", From: "src/pages", To: "src/utils"},
		}
		edges := []Edge{{From: "src/pages/Home.js", To: "src/utils/format.js", Line: 11}}
		if got := Check(rules, edges); len(got) != 0 {
			t.Fatalf("Check() = %#v, want no violations", got)
		}
	})

	t.Run("self import ignored", func(t *testing.T) {
		rules := []Rule{{Raw: "src/pages --> src/components", From: "src/pages", To: "src/components"}}
		edges := []Edge{{From: "src/pages/Home.js", To: "src/pages/Home.js", Line: 1}}
		if got := Check(rules, edges); len(got) != 0 {
			t.Fatalf("Check() = %#v, want no violations", got)
		}
	})

	t.Run("empty rules", func(t *testing.T) {
		edges := []Edge{{From: "src/pages/Home.js", To: "src/services/api.js", Line: 2}}
		if got := Check(nil, edges); len(got) != 0 {
			t.Fatalf("Check() = %#v, want no violations", got)
		}
	})
}

func TestCheckDeterminism(t *testing.T) {
	rules := []Rule{
		{Raw: "src/pages --> src/components", From: "src/pages", To: "src/components"},
		{Raw: "src/components !-> src/pages", From: "src/components", To: "src/pages", Deny: true},
		{Raw: "src/pages --> src/utils", From: "src/pages", To: "src/utils"},
	}
	edges := []Edge{
		{From: "src/pages/Home.js", To: "src/services/api.js", Line: 20},
		{From: "src/components/Nav.js", To: "src/pages/About.js", Line: 4},
		{From: "src/pages/Home.js", To: "src/models/user.js", Line: 10},
	}

	first := Check(rules, edges)
	second := Check(rules, edges)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Check not deterministic: first %#v, second %#v", first, second)
	}
	for i := 1; i < len(first); i++ {
		prev, cur := first[i-1], first[i]
		if prev.From > cur.From ||
			(prev.From == cur.From && prev.Line > cur.Line) ||
			(prev.From == cur.From && prev.Line == cur.Line && prev.To > cur.To) ||
			(prev.From == cur.From && prev.Line == cur.Line && prev.To == cur.To && prev.Rule > cur.Rule) {
			t.Fatalf("violations not sorted: %#v", first)
		}
	}
}

func TestCheckReactScenario(t *testing.T) {
	rules := []Rule{{Raw: "src/components !-> src/pages", From: "src/components", To: "src/pages", Deny: true}}
	edges := []Edge{
		{From: "src/components/Header.js", To: "src/components/Logo.js", Line: 1},
		{From: "src/components/Header.js", To: "src/utils/routes.js", Line: 2},
		{From: "src/pages/Home.js", To: "src/components/Header.js", Line: 3},
		{From: "src/components/Nav/Menu.js", To: "src/pages/Admin.js", Line: 4},
		{From: "src/components-old/Legacy.js", To: "src/pages/Home.js", Line: 5},
	}
	want := []Violation{{Rule: "src/components !-> src/pages", From: "src/components/Nav/Menu.js", To: "src/pages/Admin.js", Line: 4}}
	if got := Check(rules, edges); !reflect.DeepEqual(got, want) {
		t.Fatalf("Check() = %#v, want %#v", got, want)
	}
}
