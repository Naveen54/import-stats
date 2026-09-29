// Package scanner extracts import/require records from JS, JSX, TS and TSX
// sources using a tolerant hand-written lexer (no external dependencies).
package scanner

import (
	"os"
	"strings"
)

// Kind describes how a module was referenced.
type Kind string

const (
	KindImport     Kind = "import"         // import x from 'y'
	KindSideEffect Kind = "side-effect"    // import 'y'
	KindExportFrom Kind = "export-from"    // export { x } from 'y'
	KindDynamic    Kind = "dynamic-import" // import('y')
	KindRequire    Kind = "require"        // require('y')
)

// Record is a single module reference found in a file.
type Record struct {
	Specifier string   `json:"specifier"`
	Kind      Kind     `json:"kind"`
	Symbols   []string `json:"symbols,omitempty"`
	Line      int      `json:"line"`
	Dynamic   bool     `json:"dynamic,omitempty"` // non-literal import()/require()
}

// Export is a binding this file exports under its own name.
type Export struct {
	Name string `json:"name"` // exported name; "default" for a default export
	Line int    `json:"line"`
	Kind string `json:"kind"` // const | let | var | function | class | default | named | type | interface | enum | re-export | commonjs
}

// Result is the full scanner output for a source file.
type Result struct {
	Records []Record `json:"records"`
	Exports []Export `json:"exports,omitempty"`
	// HasJSX reports whether any JSX syntax was found in the source. It is one
	// of two signals (the other being a PascalCase exported name) used to
	// heuristically flag a file as a React component; see analyzer.FileStat.IsComponent.
	HasJSX bool `json:"hasJSX,omitempty"`
}

// ScanFile reads and scans a file from disk.
func ScanFile(path string) ([]Record, error) {
	res, err := ScanFileResult(path)
	if err != nil {
		return nil, err
	}
	return res.Records, nil
}

// ScanFileResult reads and scans a file from disk, including local exports.
func ScanFileResult(path string) (Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	return ScanResult(string(b)), nil
}

type lexer struct {
	src  string
	pos  int
	line int
	// code holds the source with comments, string bodies, regexes, JSX syntax
	// and template text blanked out, preserving byte offsets and newlines.
	code []byte
	// strs maps the start offset of a string literal to its decoded value.
	strs map[int]string
	// hasJSX records whether any JSX syntax was successfully sanitized.
	hasJSX bool
}

// Scan returns every module reference found in src.
func Scan(src string) []Record {
	return ScanResult(src).Records
}

// ScanResult returns module references and exported bindings found in src.
func ScanResult(src string) Result {
	lx := &lexer{src: src, line: 1, code: make([]byte, len(src)), strs: map[int]string{}}
	lx.sanitize()
	return lx.extract()
}

func (l *lexer) sanitize() {
	for i := range l.code {
		l.code[i] = ' '
	}
	l.sanitizeJS(0, len(l.src))
}

type tokenKind int

const (
	tokNone tokenKind = iota
	tokExprStart
	tokIdent
	tokLiteral
	tokCloseParenExpr
	tokCloseParenControl
	tokCloseBraceBlock
	tokCloseBraceExpr
)

type jsState struct {
	last                tokenKind
	lastKeyword         string
	pendingControlParen bool
	parens              []bool
	braces              []bool // true means block, false means object/expression
}

func (s *jsState) expressionAllowed() bool {
	switch s.last {
	case tokNone, tokExprStart, tokCloseParenControl, tokCloseBraceBlock:
		return true
	}
	return expressionKeyword(s.lastKeyword)
}

func (s *jsState) regexAllowed() bool {
	return s.expressionAllowed()
}

func (s *jsState) markWord(word string) {
	if expressionKeyword(word) {
		s.last = tokExprStart
		s.lastKeyword = word
		return
	}
	if word == "if" || word == "while" || word == "for" || word == "switch" || word == "catch" || word == "with" {
		s.pendingControlParen = true
	}
	s.last = tokIdent
	s.lastKeyword = word
}

func (s *jsState) markPunct(c byte) {
	s.lastKeyword = ""
	switch c {
	case '(':
		s.parens = append(s.parens, s.pendingControlParen)
		s.pendingControlParen = false
		s.last = tokExprStart
	case ')':
		control := false
		if n := len(s.parens); n > 0 {
			control = s.parens[n-1]
			s.parens = s.parens[:n-1]
		}
		if control {
			s.last = tokCloseParenControl
		} else {
			s.last = tokCloseParenExpr
		}
	case '{':
		block := s.last == tokCloseParenControl || s.last == tokIdent || s.last == tokCloseBraceBlock || s.last == tokCloseBraceExpr
		s.braces = append(s.braces, block)
		s.last = tokExprStart
	case '}':
		block := true
		if n := len(s.braces); n > 0 {
			block = s.braces[n-1]
			s.braces = s.braces[:n-1]
		}
		if block {
			s.last = tokCloseBraceBlock
		} else {
			s.last = tokCloseBraceExpr
		}
	case '[', ',', '=', ':', ';', '?', '!', '&', '|', '+', '-', '*', '%', '<', '>', '~', '^':
		s.last = tokExprStart
	case ']':
		s.last = tokLiteral
	default:
		s.last = tokExprStart
	}
}

func expressionKeyword(word string) bool {
	switch word {
	case "return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "throw", "case", "do", "else", "yield", "await":
		return true
	}
	return false
}

func (l *lexer) sanitizeJS(start, end int) {
	src := l.src
	st := jsState{}
	i := start
	for i < end {
		c := src[i]
		switch {
		case c == '\n':
			l.code[i] = '\n'
			i++
			continue
		case c == '/' && i+1 < end && src[i+1] == '/':
			for i < end && src[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < end && src[i+1] == '*':
			i += 2
			for i < end && !(src[i] == '*' && i+1 < end && src[i+1] == '/') {
				if src[i] == '\n' {
					l.code[i] = '\n'
				}
				i++
			}
			if i < end {
				i += 2
			}
			continue
		case c == '\'' || c == '"':
			start := i
			val, end := readQuoted(src, i)
			l.strs[start] = val
			l.code[start] = c
			for j := start + 1; j < end && j < len(src); j++ {
				if src[j] == '\n' {
					l.code[j] = '\n'
				}
			}
			if end-1 < len(src) && end-1 > start && src[end-1] == c {
				l.code[end-1] = c
			}
			i = end
			st.last = tokLiteral
			st.lastKeyword = ""
			continue
		case c == '`':
			i = l.sanitizeTemplate(i, end)
			st.last = tokLiteral
			st.lastKeyword = ""
			continue
		case c == '<' && st.expressionAllowed() && plausibleJSXStart(src, i):
			if next, ok := l.sanitizeJSX(i, end); ok {
				l.hasJSX = true
				i = next
				st.last = tokLiteral
				st.lastKeyword = ""
				continue
			}
			l.code[i] = c
			st.markPunct(c)
			i++
			continue
		case c == '/' && st.regexAllowed():
			end, ok := readRegex(src, i)
			if ok {
				for j := i; j < end; j++ {
					if src[j] == '\n' {
						l.code[j] = '\n'
					}
				}
				i = end
				st.last = tokLiteral
				st.lastKeyword = ""
				continue
			}
			l.code[i] = c
			st.markPunct(c)
			i++
			continue
		case isIdentStart(c):
			j := i + 1
			for j < end && isIdentChar(src[j]) {
				j++
			}
			copy(l.code[i:j], src[i:j])
			st.markWord(src[i:j])
			i = j
			continue
		case c >= '0' && c <= '9':
			l.code[i] = c
			i++
			for i < end && (isIdentChar(src[i]) || src[i] == '.') {
				l.code[i] = src[i]
				i++
			}
			st.last = tokLiteral
			st.lastKeyword = ""
			continue
		default:
			l.code[i] = c
			if !isSpace(c) {
				st.markPunct(c)
			}
			i++
		}
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func readQuoted(src string, i int) (string, int) {
	quote := src[i]
	var sb strings.Builder
	j := i + 1
	for j < len(src) {
		c := src[j]
		if c == '\\' && j+1 < len(src) {
			sb.WriteByte(src[j+1])
			j += 2
			continue
		}
		if c == quote {
			return sb.String(), j + 1
		}
		if c == '\n' { // unterminated literal; bail out tolerantly
			return sb.String(), j
		}
		sb.WriteByte(c)
		j++
	}
	return sb.String(), j
}

func (l *lexer) sanitizeTemplate(i, limit int) int {
	src := l.src
	start := i
	var sb strings.Builder
	simple := true
	j := start + 1
	for j < limit {
		c := src[j]
		if c == '\\' && j+1 < len(src) {
			sb.WriteByte(src[j+1])
			if src[j] == '\n' {
				l.code[j] = '\n'
			}
			if src[j+1] == '\n' {
				l.code[j+1] = '\n'
			}
			j += 2
			continue
		}
		if c == '`' {
			if simple {
				l.code[start] = '`'
				l.code[j] = '`'
				l.strs[start] = sb.String()
			}
			return j + 1
		}
		if c == '$' && j+1 < len(src) && src[j+1] == '{' {
			simple = false
			exprStart := j + 2
			if exprEnd, ok := findJSBraceEnd(src, exprStart, limit); ok {
				l.sanitizeJS(exprStart, exprEnd)
				j = exprEnd + 1
			} else {
				l.sanitizeJS(exprStart, limit)
				return limit
			}
			continue
		}
		if c == '\n' {
			l.code[j] = '\n'
		}
		sb.WriteByte(c)
		j++
	}
	return j
}

func plausibleJSXStart(src string, i int) bool {
	if i+1 >= len(src) {
		return false
	}
	n := src[i+1]
	return n == '>' || n == '/' || isIdentStart(n)
}

func (l *lexer) sanitizeJSX(i, limit int) (int, bool) {
	backup := append([]byte(nil), l.code[i:limit]...)
	src := l.src
	depth := 0
	started := false
	j := i
	for j < limit {
		switch src[j] {
		case '<':
			if j+1 >= limit {
				copy(l.code[i:limit], backup)
				return i, false
			}
			next := src[j+1]
			if next == '/' {
				end, _, ok := l.sanitizeJSXTag(j, limit)
				if !ok {
					copy(l.code[i:limit], backup)
					return i, false
				}
				depth--
				j = end
				if started && depth <= 0 {
					return j, true
				}
				continue
			}
			if next == '>' || isIdentStart(next) {
				end, selfClosing, ok := l.sanitizeJSXTag(j, limit)
				if !ok {
					copy(l.code[i:limit], backup)
					return i, false
				}
				started = true
				if !selfClosing {
					depth++
				}
				j = end
				if selfClosing && depth == 0 {
					return j, true
				}
				continue
			}
			copy(l.code[i:limit], backup)
			return i, false
		case '{':
			if exprEnd, ok := findJSBraceEnd(src, j+1, limit); ok {
				l.sanitizeJS(j+1, exprEnd)
				j = exprEnd + 1
				continue
			}
			copy(l.code[i:limit], backup)
			return i, false
		case '\n':
			l.code[j] = '\n'
		}
		j++
	}
	copy(l.code[i:limit], backup)
	return i, false
}

func (l *lexer) sanitizeJSXTag(i, limit int) (int, bool, bool) {
	src := l.src
	j := i
	selfClosing := false
	for j < limit {
		c := src[j]
		switch c {
		case '\'', '"':
			_, end := readQuoted(src, j)
			for k := j; k < end && k < limit; k++ {
				if src[k] == '\n' {
					l.code[k] = '\n'
				}
			}
			j = end
			continue
		case '{':
			if exprEnd, ok := findJSBraceEnd(src, j+1, limit); ok {
				l.sanitizeJS(j+1, exprEnd)
				j = exprEnd + 1
				continue
			}
			return i, false, false
		case '>':
			k := j - 1
			for k > i && isSpace(src[k]) {
				k--
			}
			selfClosing = k > i && src[k] == '/'
			return j + 1, selfClosing, true
		case '\n':
			l.code[j] = '\n'
		}
		j++
	}
	return i, false, false
}

func findJSBraceEnd(src string, start, limit int) (int, bool) {
	st := jsState{last: tokExprStart}
	depth := 1
	for i := start; i < limit; {
		c := src[i]
		switch {
		case c == '/' && i+1 < limit && src[i+1] == '/':
			i += 2
			for i < limit && src[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < limit && src[i+1] == '*':
			i += 2
			for i < limit && !(src[i] == '*' && i+1 < limit && src[i+1] == '/') {
				i++
			}
			if i < limit {
				i += 2
			}
			continue
		case c == '\'' || c == '"':
			_, end := readQuoted(src, i)
			i = end
			st.last = tokLiteral
			st.lastKeyword = ""
			continue
		case c == '`':
			i = skipTemplateRaw(src, i, limit)
			st.last = tokLiteral
			st.lastKeyword = ""
			continue
		case c == '/' && st.regexAllowed():
			if end, ok := readRegex(src, i); ok {
				i = end
				st.last = tokLiteral
				st.lastKeyword = ""
				continue
			}
			st.markPunct(c)
			i++
			continue
		case isIdentStart(c):
			j := i + 1
			for j < limit && isIdentChar(src[j]) {
				j++
			}
			st.markWord(src[i:j])
			i = j
			continue
		case c >= '0' && c <= '9':
			i++
			for i < limit && (isIdentChar(src[i]) || src[i] == '.') {
				i++
			}
			st.last = tokLiteral
			st.lastKeyword = ""
			continue
		case c == '{':
			depth++
			st.markPunct(c)
		case c == '}':
			depth--
			if depth == 0 {
				return i, true
			}
			st.markPunct(c)
		default:
			if !isSpace(c) {
				st.markPunct(c)
			}
		}
		i++
	}
	return limit, false
}

func skipTemplateRaw(src string, i, limit int) int {
	j := i + 1
	for j < limit {
		switch src[j] {
		case '\\':
			j += 2
			continue
		case '`':
			return j + 1
		case '$':
			if j+1 < limit && src[j+1] == '{' {
				if end, ok := findJSBraceEnd(src, j+2, limit); ok {
					j = end + 1
					continue
				}
				return limit
			}
		}
		j++
	}
	return limit
}

func readRegex(src string, i int) (int, bool) {
	j := i + 1
	inClass := false
	for j < len(src) {
		c := src[j]
		switch {
		case c == '\\':
			j += 2
			continue
		case c == '\n':
			return i, false
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			j++
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			return j, true
		}
		j++
	}
	return i, false
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// lineIndex builds a prefix table so line lookup is O(log n) per call.
type lineIndex []int

func buildLineIndex(src string) lineIndex {
	idx := lineIndex{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

func (li lineIndex) at(off int) int {
	lo, hi := 0, len(li)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if li[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

func (l *lexer) extract() Result {
	code := string(l.code)
	li := buildLineIndex(l.src)
	var out Result
	n := len(code)

	for i := 0; i < n; i++ {
		c := code[i]
		if !(c == 'i' || c == 'e' || c == 'r' || c == 'm') {
			continue
		}
		if !atWordStart(code, i) {
			continue
		}
		switch {
		case strings.HasPrefix(code[i:], "import"):
			if after := i + len("import"); after < n && isIdentChar(code[after]) {
				continue
			}
			rec, next, ok := l.parseImport(code, i, li)
			if ok {
				out.Records = append(out.Records, rec...)
				i = next - 1
			}
		case strings.HasPrefix(code[i:], "exports"):
			if after := i + len("exports"); after < n && isIdentChar(code[after]) {
				continue
			}
			exports, next, ok := l.parseCommonJS(code, i, li)
			if ok {
				out.Exports = append(out.Exports, exports...)
				i = next - 1
			}
		case strings.HasPrefix(code[i:], "export"):
			if after := i + len("export"); after < n && isIdentChar(code[after]) {
				continue
			}
			recs, exports, next, ok := l.parseExport(code, i, li)
			if ok {
				out.Records = append(out.Records, recs...)
				out.Exports = append(out.Exports, exports...)
				i = next - 1
			}
		case strings.HasPrefix(code[i:], "require"):
			if after := i + len("require"); after < n && isIdentChar(code[after]) {
				continue
			}
			rec, next, ok := l.parseRequire(code, i, li)
			if ok {
				out.Records = append(out.Records, rec)
				i = next - 1
			}
		case strings.HasPrefix(code[i:], "module"):
			if after := i + len("module"); after < n && isIdentChar(code[after]) {
				continue
			}
			exports, next, ok := l.parseCommonJS(code, i, li)
			if ok {
				out.Exports = append(out.Exports, exports...)
				i = next - 1
			}
		}
	}
	out.HasJSX = l.hasJSX
	return out
}

func (l *lexer) parseExport(code string, start int, li lineIndex) ([]Record, []Export, int, bool) {
	i := skipSpace(code, start+len("export"))
	if i >= len(code) {
		return nil, nil, start, false
	}
	line := li.at(start)
	if wordAt(code, i, "default") {
		return nil, []Export{{Name: "default", Line: line, Kind: "default"}}, start + len("export"), true
	}

	if wordAt(code, i, "declare") {
		i = skipSpace(code, i+len("declare"))
	}
	if wordAt(code, i, "async") {
		j := skipSpace(code, i+len("async"))
		if wordAt(code, j, "function") {
			nameOff, name := functionName(code, j+len("function"))
			if name != "" {
				return nil, []Export{{Name: name, Line: li.at(nameOff), Kind: "function"}}, start + len("export"), true
			}
		}
	}
	if wordAt(code, i, "abstract") {
		i = skipSpace(code, i+len("abstract"))
	}

	switch {
	case code[i] == '*':
		rec, end, ok := l.parseExportFrom(code, start, li)
		if !ok {
			return nil, nil, start, false
		}
		var exports []Export
		j := skipSpace(code, i+1)
		if wordAt(code, j, "as") {
			nameOff, name := readIdentifier(code, skipSpace(code, j+len("as")))
			if name != "" {
				exports = append(exports, Export{Name: name, Line: li.at(nameOff), Kind: "re-export"})
			}
		}
		return []Record{rec}, exports, end, true
	case code[i] == '{':
		close, ok := matchingBrace(code, i)
		if !ok {
			return nil, nil, start, false
		}
		stmtEnd := findStatementEnd(code, close+1)
		fromIdx := findFrom(code, close+1)
		kind := "named"
		var recs []Record
		next := start + len("export")
		if fromIdx >= 0 && fromIdx < stmtEnd {
			if rec, end, ok := l.parseExportFrom(code, start, li); ok {
				recs = append(recs, rec)
				kind = "re-export"
				next = end
			}
		}
		return recs, parseNamedExportList(code, i, close, kind, li), next, true
	case wordAt(code, i, "type"):
		j := skipSpace(code, i+len("type"))
		if j < len(code) && code[j] == '{' {
			close, ok := matchingBrace(code, j)
			if !ok {
				return nil, nil, start, false
			}
			stmtEnd := findStatementEnd(code, close+1)
			fromIdx := findFrom(code, close+1)
			kind := "named"
			var recs []Record
			next := start + len("export")
			if fromIdx >= 0 && fromIdx < stmtEnd {
				if rec, end, ok := l.parseExportFrom(code, start, li); ok {
					recs = append(recs, rec)
					kind = "re-export"
					next = end
				}
			}
			return recs, parseNamedExportList(code, j, close, kind, li), next, true
		}
		nameOff, name := readIdentifier(code, j)
		if name == "" {
			return nil, nil, start, false
		}
		return nil, []Export{{Name: name, Line: li.at(nameOff), Kind: "type"}}, start + len("export"), true
	case wordAt(code, i, "interface"):
		nameOff, name := readIdentifier(code, skipSpace(code, i+len("interface")))
		if name == "" {
			return nil, nil, start, false
		}
		return nil, []Export{{Name: name, Line: li.at(nameOff), Kind: "interface"}}, start + len("export"), true
	case wordAt(code, i, "enum"):
		nameOff, name := readIdentifier(code, skipSpace(code, i+len("enum")))
		if name == "" {
			return nil, nil, start, false
		}
		return nil, []Export{{Name: name, Line: li.at(nameOff), Kind: "enum"}}, start + len("export"), true
	case wordAt(code, i, "const"), wordAt(code, i, "let"), wordAt(code, i, "var"):
		kindEnd := i
		for kindEnd < len(code) && isIdentChar(code[kindEnd]) {
			kindEnd++
		}
		kind := code[i:kindEnd]
		return nil, parseVarExportList(code, skipSpace(code, kindEnd), kind, li), start + len("export"), true
	case wordAt(code, i, "function"):
		nameOff, name := functionName(code, i+len("function"))
		if name == "" {
			return nil, nil, start, false
		}
		return nil, []Export{{Name: name, Line: li.at(nameOff), Kind: "function"}}, start + len("export"), true
	case wordAt(code, i, "class"):
		nameOff, name := readIdentifier(code, skipSpace(code, i+len("class")))
		if name == "" {
			return nil, nil, start, false
		}
		return nil, []Export{{Name: name, Line: li.at(nameOff), Kind: "class"}}, start + len("export"), true
	}
	return nil, nil, start, false
}

func wordAt(code string, i int, word string) bool {
	if i < 0 || i+len(word) > len(code) || !strings.HasPrefix(code[i:], word) {
		return false
	}
	after := i + len(word)
	return (i == 0 || !isIdentChar(code[i-1])) && (after >= len(code) || !isIdentChar(code[after]))
}

func readIdentifier(code string, i int) (int, string) {
	i = skipSpace(code, i)
	if i >= len(code) || !isIdentStart(code[i]) {
		return i, ""
	}
	j := i + 1
	for j < len(code) && isIdentChar(code[j]) {
		j++
	}
	return i, code[i:j]
}

func functionName(code string, i int) (int, string) {
	i = skipSpace(code, i)
	if i < len(code) && code[i] == '*' {
		i = skipSpace(code, i+1)
	}
	return readIdentifier(code, i)
}

func matchingBrace(code string, open int) (int, bool) {
	depth := 1
	for i := open + 1; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		case '\'', '"', '`':
			quote := code[i]
			for i++; i < len(code) && code[i] != quote; i++ {
			}
		}
	}
	return len(code), false
}

func matchingBracket(code string, open int) (int, bool) {
	close := byte(']')
	if code[open] == '{' {
		close = '}'
	}
	depth := 1
	for i := open + 1; i < len(code); i++ {
		switch code[i] {
		case code[open]:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i, true
			}
		case '\'', '"', '`':
			quote := code[i]
			for i++; i < len(code) && code[i] != quote; i++ {
			}
		}
	}
	return len(code), false
}

func findStatementEnd(code string, i int) int {
	depth := 0
	for ; i < len(code); i++ {
		switch code[i] {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				return i
			}
		case '\'', '"', '`':
			quote := code[i]
			for i++; i < len(code) && code[i] != quote; i++ {
			}
		}
	}
	return len(code)
}

func parseNamedExportList(code string, open, close int, kind string, li lineIndex) []Export {
	var out []Export
	for _, part := range topLevelParts(code, open+1, close, ',') {
		nameOff, name := exportedName(code, part.start, part.end)
		if name != "" {
			out = append(out, Export{Name: name, Line: li.at(nameOff), Kind: kind})
		}
	}
	return out
}

func exportedName(code string, start, end int) (int, string) {
	fields := identFields(code, start, end)
	if len(fields) == 0 {
		return start, ""
	}
	if fields[0].name == "type" {
		fields = fields[1:]
	}
	for i := 0; i+1 < len(fields); i++ {
		if fields[i].name == "as" {
			return fields[i+1].off, fields[i+1].name
		}
	}
	return fields[0].off, fields[0].name
}

type identField struct {
	off  int
	name string
}

func identFields(code string, start, end int) []identField {
	var out []identField
	for i := start; i < end; i++ {
		if !isIdentStart(code[i]) {
			continue
		}
		j := i + 1
		for j < end && isIdentChar(code[j]) {
			j++
		}
		out = append(out, identField{off: i, name: code[i:j]})
		i = j - 1
	}
	return out
}

func parseVarExportList(code string, start int, kind string, li lineIndex) []Export {
	end := findStatementEnd(code, start)
	var out []Export
	for i := start; i < end; {
		i = skipSpaceAndCommas(code, i, end)
		if i >= end {
			break
		}
		switch {
		case code[i] == '{' || code[i] == '[':
			close, ok := matchingBracket(code, i)
			if !ok || close > end {
				return out
			}
			// Destructuring is recursive for simple binding patterns. Computed
			// keys and defaults are ignored rather than guessed.
			for _, b := range destructuredBindings(code, i, close) {
				out = append(out, Export{Name: b.name, Line: li.at(b.off), Kind: kind})
			}
			i = skipDeclaratorInitializer(code, close+1, end)
		case isIdentStart(code[i]):
			j := i + 1
			for j < end && isIdentChar(code[j]) {
				j++
			}
			out = append(out, Export{Name: code[i:j], Line: li.at(i), Kind: kind})
			i = skipDeclaratorInitializer(code, j, end)
		default:
			i++
		}
	}
	return out
}

func skipSpaceAndCommas(code string, i, end int) int {
	for i < end && (isSpace(code[i]) || code[i] == ',') {
		i++
	}
	return i
}

func skipDeclaratorInitializer(code string, i, end int) int {
	depth := 0
	for ; i < end; i++ {
		switch code[i] {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				return i + 1
			}
		case '\'', '"', '`':
			quote := code[i]
			for i++; i < end && code[i] != quote; i++ {
			}
		}
	}
	return end
}

type span struct {
	start int
	end   int
}

func topLevelParts(code string, start, end int, sep byte) []span {
	var out []span
	partStart := start
	depth := 0
	for i := start; i < end; i++ {
		switch code[i] {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				out = append(out, span{start: partStart, end: i})
				partStart = i + 1
			}
		case '\'', '"', '`':
			quote := code[i]
			for i++; i < end && code[i] != quote; i++ {
			}
		}
	}
	out = append(out, span{start: partStart, end: end})
	return out
}

type binding struct {
	off  int
	name string
}

func destructuredBindings(code string, open, close int) []binding {
	if code[open] == '{' {
		return objectPatternBindings(code, open+1, close)
	}
	return arrayPatternBindings(code, open+1, close)
}

func objectPatternBindings(code string, start, end int) []binding {
	var out []binding
	for _, part := range topLevelParts(code, start, end, ',') {
		s := skipSpace(code, part.start)
		if s >= part.end {
			continue
		}
		if strings.HasPrefix(code[s:part.end], "...") {
			if off, name := readIdentifier(code, s+3); name != "" && off < part.end {
				out = append(out, binding{off: off, name: name})
			}
			continue
		}
		colon := topLevelByte(code, s, part.end, ':')
		targetStart, targetEnd := s, part.end
		if colon >= 0 {
			targetStart = skipSpace(code, colon+1)
		}
		if eq := topLevelByte(code, targetStart, targetEnd, '='); eq >= 0 {
			targetEnd = eq
		}
		if targetStart < targetEnd && (code[targetStart] == '{' || code[targetStart] == '[') {
			if close, ok := matchingBracket(code, targetStart); ok && close < targetEnd {
				out = append(out, destructuredBindings(code, targetStart, close)...)
			}
			continue
		}
		if off, name := readIdentifier(code, targetStart); name != "" && off < targetEnd {
			out = append(out, binding{off: off, name: name})
		}
	}
	return out
}

func arrayPatternBindings(code string, start, end int) []binding {
	var out []binding
	for _, part := range topLevelParts(code, start, end, ',') {
		s := skipSpace(code, part.start)
		if s >= part.end {
			continue
		}
		if strings.HasPrefix(code[s:part.end], "...") {
			s = skipSpace(code, s+3)
		}
		if eq := topLevelByte(code, s, part.end, '='); eq >= 0 {
			part.end = eq
		}
		if s < part.end && (code[s] == '{' || code[s] == '[') {
			if close, ok := matchingBracket(code, s); ok && close < part.end {
				out = append(out, destructuredBindings(code, s, close)...)
			}
			continue
		}
		if off, name := readIdentifier(code, s); name != "" && off < part.end {
			out = append(out, binding{off: off, name: name})
		}
	}
	return out
}

func topLevelByte(code string, start, end int, b byte) int {
	depth := 0
	for i := start; i < end; i++ {
		switch code[i] {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case b:
			if depth == 0 {
				return i
			}
		case '\'', '"', '`':
			quote := code[i]
			for i++; i < end && code[i] != quote; i++ {
			}
		}
	}
	return -1
}

func (l *lexer) parseCommonJS(code string, start int, li lineIndex) ([]Export, int, bool) {
	if strings.HasPrefix(code[start:], "exports") {
		return l.parseCommonJSAfterExports(code, start, start+len("exports"), li)
	}
	i := skipSpace(code, start+len("module"))
	if i >= len(code) || code[i] != '.' {
		return nil, start, false
	}
	i = skipSpace(code, i+1)
	if !wordAt(code, i, "exports") {
		return nil, start, false
	}
	return l.parseCommonJSAfterExports(code, start, i+len("exports"), li)
}

func (l *lexer) parseCommonJSAfterExports(code string, start, i int, li lineIndex) ([]Export, int, bool) {
	i = skipSpace(code, i)
	if i < len(code) && (code[i] == '.' || code[i] == '[') {
		nameOff, name, after, ok := l.propertyName(code, i)
		if !ok {
			return nil, start, false
		}
		j := skipSpace(code, after)
		if j >= len(code) || code[j] != '=' || (j+1 < len(code) && code[j+1] == '=') {
			return nil, start, false
		}
		return []Export{{Name: name, Line: li.at(nameOff), Kind: "commonjs"}}, start + 1, true
	}
	if i >= len(code) || code[i] != '=' || (i+1 < len(code) && code[i+1] == '=') {
		return nil, start, false
	}
	j := skipSpace(code, i+1)
	if j < len(code) && code[j] == '{' {
		if close, ok := matchingBrace(code, j); ok {
			exports, complete := l.objectLiteralExports(code, j, close, li)
			if len(exports) == 0 || !complete {
				// Dynamic/computed wholesale CommonJS objects cannot be named
				// accurately; the sentinel lets callers know the file exports.
				exports = append(exports, Export{Name: "module.exports", Line: li.at(start), Kind: "commonjs"})
			}
			return exports, start + 1, true
		}
	}
	return []Export{{Name: "module.exports", Line: li.at(start), Kind: "commonjs"}}, start + 1, true
}

func (l *lexer) propertyName(code string, i int) (int, string, int, bool) {
	if code[i] == '.' {
		off, name := readIdentifier(code, i+1)
		return off, name, off + len(name), name != ""
	}
	j := skipSpace(code, i+1)
	if spec, end, ok := l.literalAt(code, j); ok {
		k := skipSpace(code, end)
		if k < len(code) && code[k] == ']' && spec != "" {
			return j, spec, k + 1, true
		}
	}
	return i, "", i, false
}

func (l *lexer) objectLiteralExports(code string, open, close int, li lineIndex) ([]Export, bool) {
	var out []Export
	complete := true
	for _, part := range topLevelParts(code, open+1, close, ',') {
		s := skipSpace(code, part.start)
		if s >= part.end {
			continue
		}
		if strings.HasPrefix(code[s:part.end], "...") || code[s] == '[' {
			complete = false
			continue
		}
		if spec, end, ok := l.literalAt(code, s); ok {
			k := skipSpace(code, end)
			if k < part.end && code[k] == ':' && spec != "" {
				out = append(out, Export{Name: spec, Line: li.at(s), Kind: "commonjs"})
				continue
			}
			complete = false
			continue
		}
		off, name := readIdentifier(code, s)
		if name == "" || off >= part.end {
			continue
		}
		out = append(out, Export{Name: name, Line: li.at(off), Kind: "commonjs"})
	}
	return out, complete
}

func atWordStart(code string, i int) bool {
	return i == 0 || !isIdentChar(code[i-1]) && code[i-1] != '.'
}

func skipSpace(code string, i int) int {
	for i < len(code) && isSpace(code[i]) {
		i++
	}
	return i
}

// parseImport handles `import ... from 'x'`, `import 'x'` and `import('x')`.
func (l *lexer) parseImport(code string, start int, li lineIndex) ([]Record, int, bool) {
	i := skipSpace(code, start+len("import"))
	if i >= len(code) {
		return nil, start, false
	}

	// Dynamic import: import( ... )
	if code[i] == '(' {
		if !calleeExpressionAllowed(code, start) || callLooksLikeMethodDefinition(code, i) {
			return nil, start, false
		}
		j := skipSpace(code, i+1)
		spec, end, ok := l.literalAt(code, j)
		if ok {
			return []Record{{Specifier: spec, Kind: KindDynamic, Line: li.at(start)}}, end, true
		}
		return []Record{{Specifier: "", Kind: KindDynamic, Line: li.at(start), Dynamic: true}}, i + 1, true
	}

	// import.meta
	if code[i] == '.' {
		return nil, start, false
	}

	// Side-effect import: import 'x'
	if spec, end, ok := l.literalAt(code, i); ok {
		return []Record{{Specifier: spec, Kind: KindSideEffect, Line: li.at(start)}}, end, true
	}

	// import type? clause from 'x'
	clauseStart := i
	fromIdx := findFrom(code, i)
	if fromIdx < 0 {
		return nil, start, false
	}
	j := skipSpace(code, fromIdx+len("from"))
	spec, end, ok := l.literalAt(code, j)
	if !ok {
		return nil, start, false
	}
	symbols := parseClause(code[clauseStart:fromIdx])
	return []Record{{Specifier: spec, Kind: KindImport, Symbols: symbols, Line: li.at(start)}}, end, true
}

func (l *lexer) parseExportFrom(code string, start int, li lineIndex) (Record, int, bool) {
	i := skipSpace(code, start+len("export"))
	if i >= len(code) {
		return Record{}, start, false
	}
	// Only `export * from` / `export { ... } from` reference a module.
	if code[i] != '*' && code[i] != '{' {
		if !strings.HasPrefix(code[i:], "type") {
			return Record{}, start, false
		}
	}
	fromIdx := findFrom(code, i)
	if fromIdx < 0 {
		return Record{}, start, false
	}
	// Guard against matching a `from` far beyond this statement.
	if strings.Contains(code[i:fromIdx], ";") {
		return Record{}, start, false
	}
	j := skipSpace(code, fromIdx+len("from"))
	spec, end, ok := l.literalAt(code, j)
	if !ok {
		return Record{}, start, false
	}
	return Record{Specifier: spec, Kind: KindExportFrom, Symbols: parseClause(code[i:fromIdx]), Line: li.at(start)}, end, true
}

func (l *lexer) parseRequire(code string, start int, li lineIndex) (Record, int, bool) {
	i := skipSpace(code, start+len("require"))
	if i < len(code) && code[i] == '.' {
		i = skipSpace(code, i+1)
		if !strings.HasPrefix(code[i:], "resolve") {
			return Record{}, start, false
		}
		after := i + len("resolve")
		if after < len(code) && isIdentChar(code[after]) {
			return Record{}, start, false
		}
		i = skipSpace(code, after)
	}
	if i >= len(code) || code[i] != '(' || callLooksLikeMethodDefinition(code, i) {
		return Record{}, start, false
	}
	j := skipSpace(code, i+1)
	spec, end, ok := l.literalAt(code, j)
	if !ok {
		return Record{Specifier: "", Kind: KindRequire, Line: li.at(start), Dynamic: true}, i + 1, true
	}
	symbols := requireSymbols(code, start)
	return Record{Specifier: spec, Kind: KindRequire, Symbols: symbols, Line: li.at(start)}, end, true
}

func calleeExpressionAllowed(code string, start int) bool {
	i := start - 1
	for i >= 0 && isSpace(code[i]) {
		i--
	}
	if i < 0 {
		return true
	}
	if isIdentChar(code[i]) {
		end := i + 1
		for i >= 0 && isIdentChar(code[i]) {
			i--
		}
		return expressionKeyword(code[i+1 : end])
	}
	if code[i] == '\'' || code[i] == '"' || code[i] == '`' || code[i] == ')' || code[i] == ']' {
		return false
	}
	return code[i] != '.'
}

func callLooksLikeMethodDefinition(code string, open int) bool {
	close, ok := matchingParen(code, open)
	if !ok {
		return false
	}
	i := skipSpace(code, close+1)
	return i < len(code) && code[i] == '{'
}

func matchingParen(code string, open int) (int, bool) {
	depth := 1
	for i := open + 1; i < len(code); i++ {
		switch code[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		case '\'', '"', '`':
			quote := code[i]
			for i++; i < len(code) && code[i] != quote; i++ {
			}
		}
	}
	return len(code), false
}

// requireSymbols extracts the binding of `const { a, b } = require(...)`.
func requireSymbols(code string, requireStart int) []string {
	lineStart := strings.LastIndexByte(code[:requireStart], '\n') + 1
	head := code[lineStart:requireStart]
	eq := strings.LastIndexByte(head, '=')
	if eq < 0 {
		return nil
	}
	decl := strings.TrimSpace(head[:eq])
	for _, kw := range []string{"const ", "let ", "var "} {
		decl = strings.TrimPrefix(strings.TrimSpace(decl), kw)
	}
	decl = strings.TrimSpace(decl)
	if strings.HasPrefix(decl, "{") {
		return parseNamed(decl)
	}
	if decl != "" && isIdentifier(decl) {
		return []string{"default:" + decl}
	}
	return nil
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentChar(s[i]) {
			return false
		}
	}
	return true
}

// literalAt returns the string literal starting at i (using the decoded values
// captured during sanitisation) plus the offset just past it.
func (l *lexer) literalAt(code string, i int) (string, int, bool) {
	if i >= len(code) {
		return "", i, false
	}
	c := code[i]
	if c != '\'' && c != '"' && c != '`' {
		return "", i, false
	}
	val, ok := l.strs[i]
	if !ok {
		return "", i, false
	}
	// Find the closing marker written during sanitisation.
	for j := i + 1; j < len(code); j++ {
		if code[j] == c {
			return val, j + 1, true
		}
	}
	return val, len(code), true
}

// findFrom locates the `from` keyword that terminates an import clause.
func findFrom(code string, i int) int {
	depth := 0
	for ; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ';':
			if depth <= 0 {
				return -1
			}
		case 'f':
			if depth <= 0 && atWordStart(code, i) && strings.HasPrefix(code[i:], "from") {
				after := i + len("from")
				if after >= len(code) || !isIdentChar(code[after]) {
					return i
				}
			}
		}
	}
	return -1
}

// parseClause turns an import clause into a symbol list. Named bindings are
// reported as `name` or `name as alias`; default/namespace bindings are
// prefixed so consumers can distinguish them.
func parseClause(clause string) []string {
	clause = strings.TrimSpace(clause)
	clause = strings.TrimPrefix(clause, "type ")
	var symbols []string
	if brace := strings.IndexByte(clause, '{'); brace >= 0 {
		head := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(clause[:brace]), ","))
		symbols = append(symbols, defaultOrNamespace(head)...)
		symbols = append(symbols, parseNamed(clause[brace:])...)
		return symbols
	}
	return defaultOrNamespace(clause)
}

func defaultOrNamespace(head string) []string {
	head = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(head), ","))
	if head == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(head, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "*") {
			alias := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(part, "*")), "as"))
			out = append(out, "*:"+alias)
			continue
		}
		out = append(out, "default:"+part)
	}
	return out
}

func parseNamed(seg string) []string {
	open := strings.IndexByte(seg, '{')
	if open < 0 {
		return nil
	}
	close := strings.IndexByte(seg[open:], '}')
	if close < 0 {
		close = len(seg) - open
	}
	inner := seg[open+1 : open+close]
	var out []string
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "type ")
		part = strings.Join(strings.Fields(part), " ")
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
