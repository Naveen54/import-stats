// Package rules checks import graph edges against architecture constraints.
package rules

import (
	"fmt"
	"sort"
	"strings"
)

// Rule is one parsed layering constraint.
type Rule struct {
	Raw  string // original text, echoed in violations
	From string // glob for the importing file
	To   string // glob for the imported file
	Deny bool   // true for "!->", false for "-->" (allowed-only)
}

// Edge is one local import to be checked.
type Edge struct {
	From string // project-relative path of the importing file
	To   string // project-relative path of the imported file
	Line int
}

// Violation is an edge that breaks a rule.
type Violation struct {
	Rule string
	From string
	To   string
	Line int
}

// Parse turns raw --rule strings into rules, reporting the first syntax error.
func Parse(raw []string) ([]Rule, error) {
	rules := make([]Rule, 0, len(raw))
	for _, text := range raw {
		rule, err := parseOne(text)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func parseOne(raw string) (Rule, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return Rule{}, fmt.Errorf("invalid rule %q: missing operator !-> or -->", raw)
	}

	idx, op, deny := strings.Index(text, "!->"), "!->", true
	if idx < 0 {
		idx, op, deny = strings.Index(text, "-->"), "-->", false
	}
	if idx < 0 {
		if hasUnknownOperator(text) {
			return Rule{}, fmt.Errorf("invalid rule %q: unknown operator; use !-> or -->", raw)
		}
		return Rule{}, fmt.Errorf("invalid rule %q: missing operator !-> or -->", raw)
	}

	from := strings.TrimSpace(text[:idx])
	to := strings.TrimSpace(text[idx+len(op):])
	if from == "" || to == "" {
		return Rule{}, fmt.Errorf("invalid rule %q: empty pattern on one side of %s", raw, op)
	}
	if strings.Contains(to, "!->") || strings.Contains(to, "-->") {
		return Rule{}, fmt.Errorf("invalid rule %q: multiple operators", raw)
	}

	return Rule{Raw: raw, From: from, To: to, Deny: deny}, nil
}

// Check reports every edge that violates a rule, in deterministic order.
func Check(rs []Rule, edges []Edge) []Violation {
	var out []Violation
	for _, edge := range edges {
		if edge.From == edge.To {
			continue
		}

		for _, rule := range rs {
			if rule.Deny && Match(rule.From, edge.From) && Match(rule.To, edge.To) {
				out = append(out, violation(rule, edge))
			}
		}

		seenAllow := false
		allowed := false
		for _, rule := range rs {
			if rule.Deny || !Match(rule.From, edge.From) {
				continue
			}
			seenAllow = true
			if Match(rule.To, edge.To) {
				allowed = true
			}
		}
		if seenAllow && !allowed {
			for _, rule := range rs {
				if !rule.Deny && Match(rule.From, edge.From) {
					out = append(out, violation(rule, edge))
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		if out[i].To != out[j].To {
			return out[i].To < out[j].To
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

func hasUnknownOperator(text string) bool {
	if strings.Contains(text, "->") || strings.Contains(text, "=>") || strings.Contains(text, "<-") {
		return true
	}
	fields := strings.Fields(text)
	if len(fields) != 3 {
		return false
	}
	return strings.ContainsAny(fields[1], "-<>=")
}

func violation(rule Rule, edge Edge) Violation {
	return Violation{Rule: rule.Raw, From: edge.From, To: edge.To, Line: edge.Line}
}

// Match reports whether a project-relative path matches a glob pattern.
//
// Patterns use slash-separated paths. '*' and '?' match within one segment;
// '**' may cross segment boundaries. A pattern with no wildcard is a segment
// boundary prefix, so 'src/components' matches 'src/components/Header.js' but
// not 'src/components-old/Header.js'.
func Match(pattern, path string) bool {
	pattern = cleanPath(pattern)
	path = cleanPath(path)
	if pattern == "" || path == "" {
		return pattern == path
	}
	if !hasWildcard(pattern) {
		return path == pattern || strings.HasPrefix(path, pattern+"/")
	}
	return matchSegments(splitPath(pattern), splitPath(path))
}

func cleanPath(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\\", "/"))
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	s = strings.TrimPrefix(s, "./")
	return strings.Trim(s, "/")
}

func hasWildcard(s string) bool {
	return strings.ContainsAny(s, "*?")
}

func splitPath(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegments(pattern, path []string) bool {
	dp := make([][]bool, len(pattern)+1)
	for i := range dp {
		dp[i] = make([]bool, len(path)+1)
	}
	dp[0][0] = true
	for i := 1; i <= len(pattern); i++ {
		if pattern[i-1] == "**" {
			dp[i][0] = dp[i-1][0]
		}
	}
	for i := 1; i <= len(pattern); i++ {
		for j := 1; j <= len(path); j++ {
			if pattern[i-1] == "**" {
				dp[i][j] = dp[i-1][j] || dp[i][j-1]
				continue
			}
			dp[i][j] = dp[i-1][j-1] && matchSegment(pattern[i-1], path[j-1])
		}
	}
	return dp[len(pattern)][len(path)]
}

func matchSegment(pattern, text string) bool {
	dp := make([][]bool, len(pattern)+1)
	for i := range dp {
		dp[i] = make([]bool, len(text)+1)
	}
	dp[0][0] = true
	for i := 1; i <= len(pattern); i++ {
		if pattern[i-1] == '*' {
			dp[i][0] = dp[i-1][0]
		}
	}
	for i := 1; i <= len(pattern); i++ {
		for j := 1; j <= len(text); j++ {
			switch pattern[i-1] {
			case '*':
				dp[i][j] = dp[i-1][j] || dp[i][j-1]
			case '?':
				dp[i][j] = dp[i-1][j-1]
			default:
				dp[i][j] = dp[i-1][j-1] && pattern[i-1] == text[j-1]
			}
		}
	}
	return dp[len(pattern)][len(text)]
}
