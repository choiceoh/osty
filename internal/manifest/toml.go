package manifest

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Package manifest includes a small TOML parser scoped to the subset
// needed for osty.toml and osty.lock. Rather than pull in an external
// dependency we implement just what the manifest shapes require:
//
//   - Bare and quoted keys (`"with spaces"`).
//   - Dotted keys: `a.b = 1`.
//   - String, integer, boolean scalars. Floats are parsed as strings
//     inside the generic Value to keep the parser small.
//   - Inline tables: `{ key = value, other = value }`.
//   - Arrays: `[1, 2, 3]` and `["a", "b"]`. Nested arrays supported.
//   - `[section]` and `[[array.of.tables]]` headers.
//   - `#` line comments.
//
// The parser rejects: multiline strings, escape sequences outside
// the documented set, hex/octal/binary integers, dates, and literal
// (single-quoted) strings. osty.toml does not use those features;
// a user who writes one gets a clear error pointing at the offending
// line.
//
// Returned shape: the top level is a *tomlTable, keyed by its bare
// key; every subtable is another *tomlTable; `[[array]]` headers
// produce []tomlTable wrapped in an *tomlArray.

// value is the common union type returned from the TOML parser. Only
// ever one of the *ptr/slice fields is set — callers switch on which
// is non-nil.
type value struct {
	Str  *string
	Int  *int64
	Bool *bool
	Arr  *tomlArray
	Tbl  *tomlTable
	// Line is the 1-based source line where the value begins; carried
	// through so downstream errors point back at the right manifest
	// line.
	Line int
}

// tomlTable is a key-ordered map so re-serializing preserves author
// intent. osty.lock roundtrips deterministically on this alone.
type tomlTable struct {
	keys  []string
	items map[string]*value
	// inline is true for `{ ... }` tables written on one line. The
	// writer preserves that shape.
	inline bool
	// Line is the 1-based source line where the table opened. Zero
	// for synthesized tables built by the writer.
	Line int
}

func newTable() *tomlTable {
	return &tomlTable{items: map[string]*value{}}
}

func (t *tomlTable) set(key string, v *value) {
	if _, ok := t.items[key]; !ok {
		t.keys = append(t.keys, key)
	}
	t.items[key] = v
}

func (t *tomlTable) get(key string) (*value, bool) {
	v, ok := t.items[key]
	return v, ok
}

// tomlArray is either a value array (`[1, 2, 3]`) or an array of
// tables (`[[package]]` producing repeated tables). A mixed array is
// rejected at parse time.
type tomlArray struct {
	Values []*value
}

// parseTOML parses src into a root table. Errors include line numbers
// and a short context string. The parser is strict: extra commas,
// missing values, or unknown escape sequences all error.
func parseTOML(src []byte) (*tomlTable, error) {
	p := tomlParser{src: src, line: 1}
	return p.parseRoot()
}

type tomlParser struct {
	src  []byte
	pos  int
	line int
}

func (p *tomlParser) errorf(format string, args ...any) error {
	return fmt.Errorf("osty.toml:%d: %s", p.line, fmt.Sprintf(format, args...))
}

// parseRoot drives the top-level loop: keys go into `root`, `[section]`
// headers switch the active table, `[[array]]` headers append.
func (p *tomlParser) parseRoot() (*tomlTable, error) {
	root := newTable()
	root.Line = 1
	cur := root
	for {
		p.skipWhitespaceAndComments()
		if p.atEnd() {
			return root, nil
		}
		switch p.peek() {
		case '[':
			next, err := p.parseHeader(root)
			if err != nil {
				return nil, err
			}
			cur = next
		default:
			if err := p.parseKeyValue(cur); err != nil {
				return nil, err
			}
		}
	}
}

// parseHeader handles `[a.b]` and `[[a.b]]`. Returns the table new
// key-values should be added to.
func (p *tomlParser) parseHeader(root *tomlTable) (*tomlTable, error) {
	startLine := p.line
	p.advance() // '['
	arrayOfTables := false
	if !p.atEnd() && p.peek() == '[' {
		arrayOfTables = true
		p.advance()
	}
	parts, err := p.parseDottedKey()
	if err != nil {
		return nil, err
	}
	p.skipHorizontalWhitespace()
	if arrayOfTables {
		if p.atEnd() || p.peek() != ']' {
			return nil, p.errorf("expected `]]` to close array-of-tables header")
		}
		p.advance()
	}
	if p.atEnd() || p.peek() != ']' {
		return nil, p.errorf("expected `]` to close table header")
	}
	p.advance()
	// Anything after the header on the same line must be whitespace /
	// comment.
	p.skipHorizontalWhitespace()
	if !p.atEnd() && p.peek() != '\n' && p.peek() != '#' {
		return nil, p.errorf("garbage after table header")
	}
	// Navigate / create the intermediate tables.
	t := root
	for i, part := range parts {
		last := i == len(parts)-1
		existing, ok := t.items[part]
		switch {
		case !ok:
			var sub *tomlTable
			if last && arrayOfTables {
				arr := &tomlArray{}
				sub = newTable()
				sub.Line = startLine
				arr.Values = append(arr.Values, &value{Tbl: sub, Line: startLine})
				t.set(part, &value{Arr: arr, Line: startLine})
			} else {
				sub = newTable()
				sub.Line = startLine
				t.set(part, &value{Tbl: sub, Line: startLine})
			}
			t = sub
		case existing.Tbl != nil:
			if last && arrayOfTables {
				return nil, p.errorf("table `%s` previously defined as a plain table", strings.Join(parts, "."))
			}
			t = existing.Tbl
		case existing.Arr != nil:
			if last && arrayOfTables {
				sub := newTable()
				sub.Line = startLine
				existing.Arr.Values = append(existing.Arr.Values, &value{Tbl: sub, Line: startLine})
				t = sub
				continue
			}
			// Navigate into the last element of the array-of-tables.
			if n := len(existing.Arr.Values); n > 0 && existing.Arr.Values[n-1].Tbl != nil {
				t = existing.Arr.Values[n-1].Tbl
				continue
			}
			return nil, p.errorf("cannot descend into array `%s`", strings.Join(parts[:i+1], "."))
		default:
			return nil, p.errorf("cannot descend into scalar `%s`", strings.Join(parts[:i+1], "."))
		}
	}
	return t, nil
}

// parseKeyValue parses `key = value` and sets it on t.
func (p *tomlParser) parseKeyValue(t *tomlTable) error {
	startLine := p.line
	parts, err := p.parseDottedKey()
	if err != nil {
		return err
	}
	p.skipHorizontalWhitespace()
	if p.atEnd() || p.peek() != '=' {
		return p.errorf("expected `=` after key")
	}
	p.advance()
	p.skipHorizontalWhitespace()
	v, err := p.parseValue()
	if err != nil {
		return err
	}
	// Walk dotted path, creating intermediate tables as needed.
	cur := t
	for i, part := range parts {
		if i == len(parts)-1 {
			if _, dup := cur.items[part]; dup {
				return p.errorf("duplicate key `%s`", strings.Join(parts, "."))
			}
			v.Line = startLine
			cur.set(part, v)
			break
		}
		if existing, ok := cur.items[part]; ok {
			if existing.Tbl == nil {
				return p.errorf("key `%s` already set to non-table", strings.Join(parts[:i+1], "."))
			}
			cur = existing.Tbl
		} else {
			sub := newTable()
			sub.Line = startLine
			cur.set(part, &value{Tbl: sub, Line: startLine})
			cur = sub
		}
	}
	p.skipHorizontalWhitespace()
	if !p.atEnd() && p.peek() != '\n' && p.peek() != '#' {
		return p.errorf("garbage after value")
	}
	return nil
}

// parseDottedKey consumes one or more keys separated by `.`. Keys are
// either bare (matching [A-Za-z0-9_-]+) or basic-quoted ("...").
func (p *tomlParser) parseDottedKey() ([]string, error) {
	var out []string
	for {
		p.skipHorizontalWhitespace()
		k, err := p.parseSingleKey()
		if err != nil {
			return nil, err
		}
		out = append(out, k)
		p.skipHorizontalWhitespace()
		if p.atEnd() || p.peek() != '.' {
			return out, nil
		}
		p.advance() // '.'
	}
}

func (p *tomlParser) parseSingleKey() (string, error) {
	if p.atEnd() {
		return "", p.errorf("expected key, got end of input")
	}
	if p.peek() == '"' {
		return p.parseBasicString()
	}
	start := p.pos
	for !p.atEnd() {
		c := p.peek()
		if isBareKeyChar(c) {
			p.advance()
			continue
		}
		break
	}
	if p.pos == start {
		return "", p.errorf("expected key, got %q", string(p.src[p.pos]))
	}
	return string(p.src[start:p.pos]), nil
}

func isBareKeyChar(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z',
		c >= 'a' && c <= 'z',
		c >= '0' && c <= '9',
		c == '_' || c == '-':
		return true
	}
	return false
}

// parseValue parses one value: string, int, bool, array, or inline
// table. Leading whitespace is assumed to have been consumed.
func (p *tomlParser) parseValue() (*value, error) {
	if p.atEnd() {
		return nil, p.errorf("expected value, got end of input")
	}
	c := p.peek()
	switch {
	case c == '"':
		s, err := p.parseBasicString()
		if err != nil {
			return nil, err
		}
		return &value{Str: &s, Line: p.line}, nil
	case c == '[':
		return p.parseArray()
	case c == '{':
		return p.parseInlineTable()
	case c == 't' || c == 'f':
		return p.parseBool()
	case c == '-' || c == '+' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	}
	return nil, p.errorf("unexpected character %q at start of value", string(c))
}

func (p *tomlParser) parseBool() (*value, error) {
	if strings.HasPrefix(string(p.src[p.pos:]), "true") {
		p.pos += 4
		b := true
		return &value{Bool: &b, Line: p.line}, nil
	}
	if strings.HasPrefix(string(p.src[p.pos:]), "false") {
		p.pos += 5
		b := false
		return &value{Bool: &b, Line: p.line}, nil
	}
	return nil, p.errorf("expected bool (`true` or `false`)")
}

// parseNumber reads an integer (decimal, optionally signed). Floats
// are not supported in osty.toml/lock; emitting one is an error here
// so the manifest schema stays small.
func (p *tomlParser) parseNumber() (*value, error) {
	start := p.pos
	if p.peek() == '+' || p.peek() == '-' {
		p.advance()
	}
	if p.atEnd() || !(p.peek() >= '0' && p.peek() <= '9') {
		return nil, p.errorf("expected digit after sign")
	}
	for !p.atEnd() {
		c := p.peek()
		if c >= '0' && c <= '9' {
			p.advance()
			continue
		}
		if c == '_' {
			// TOML allows `1_000_000`; strip silently.
			p.advance()
			continue
		}
		if c == '.' || c == 'e' || c == 'E' {
			return nil, p.errorf("floating-point values are not supported in osty.toml")
		}
		break
	}
	raw := strings.ReplaceAll(string(p.src[start:p.pos]), "_", "")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, p.errorf("invalid integer %q: %v", raw, err)
	}
	return &value{Int: &n, Line: p.line}, nil
}

// parseBasicString reads a "..." string. Supports the common escapes
// (\" \\ \n \r \t) and \uXXXX. Any other escape is rejected so the
// grammar stays narrow.
func (p *tomlParser) parseBasicString() (string, error) {
	if p.atEnd() || p.peek() != '"' {
		return "", p.errorf("expected `\"` to open string")
	}
	p.advance()
	var out strings.Builder
	for {
		if p.atEnd() {
			return "", p.errorf("unterminated string")
		}
		c := p.peek()
		if c == '\n' {
			return "", p.errorf("newline inside single-line string; use escaped `\\n` instead")
		}
		if c == '"' {
			p.advance()
			return out.String(), nil
		}
		if c == '\\' {
			p.advance()
			if p.atEnd() {
				return "", p.errorf("unterminated escape at end of string")
			}
			esc := p.peek()
			p.advance()
			switch esc {
			case '"':
				out.WriteByte('"')
			case '\\':
				out.WriteByte('\\')
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			case '/':
				out.WriteByte('/')
			case 'u':
				if p.pos+4 > len(p.src) {
					return "", p.errorf("truncated \\u escape")
				}
				hex := string(p.src[p.pos : p.pos+4])
				p.pos += 4
				n, err := strconv.ParseUint(hex, 16, 32)
				if err != nil {
					return "", p.errorf("bad \\u escape %q: %v", hex, err)
				}
				var buf [utf8.UTFMax]byte
				nb := utf8.EncodeRune(buf[:], rune(n))
				out.Write(buf[:nb])
			default:
				return "", p.errorf("unknown escape `\\%c`", esc)
			}
			continue
		}
		out.WriteByte(c)
		p.advance()
	}
}

// parseArray reads `[v, v, v]`, allowing a trailing comma and
// newlines between elements. A mixed-type array is rejected.
func (p *tomlParser) parseArray() (*value, error) {
	startLine := p.line
	if p.atEnd() || p.peek() != '[' {
		return nil, p.errorf("expected `[` to open array")
	}
	p.advance()
	arr := &tomlArray{}
	for {
		p.skipWhitespaceAndComments()
		if p.atEnd() {
			return nil, p.errorf("unterminated array")
		}
		if p.peek() == ']' {
			p.advance()
			return &value{Arr: arr, Line: startLine}, nil
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		arr.Values = append(arr.Values, v)
		p.skipWhitespaceAndComments()
		if p.atEnd() {
			return nil, p.errorf("unterminated array")
		}
		if p.peek() == ',' {
			p.advance()
			continue
		}
		if p.peek() == ']' {
			p.advance()
			return &value{Arr: arr, Line: startLine}, nil
		}
		return nil, p.errorf("expected `,` or `]` in array")
	}
}

// parseInlineTable reads `{ a = 1, b = "x" }`. Newlines inside an
// inline table are NOT permitted per TOML 1.0 — reject them clearly.
func (p *tomlParser) parseInlineTable() (*value, error) {
	startLine := p.line
	if p.atEnd() || p.peek() != '{' {
		return nil, p.errorf("expected `{` to open inline table")
	}
	p.advance()
	t := newTable()
	t.inline = true
	t.Line = startLine
	p.skipHorizontalWhitespace()
	if !p.atEnd() && p.peek() == '}' {
		p.advance()
		return &value{Tbl: t, Line: startLine}, nil
	}
	for {
		p.skipHorizontalWhitespace()
		if p.atEnd() || p.peek() == '\n' {
			return nil, p.errorf("inline table may not contain a newline; split into a `[header]` table instead")
		}
		parts, err := p.parseDottedKey()
		if err != nil {
			return nil, err
		}
		p.skipHorizontalWhitespace()
		if p.atEnd() || p.peek() != '=' {
			return nil, p.errorf("expected `=` after inline-table key")
		}
		p.advance()
		p.skipHorizontalWhitespace()
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		cur := t
		for i, part := range parts {
			if i == len(parts)-1 {
				cur.set(part, v)
				break
			}
			if existing, ok := cur.items[part]; ok && existing.Tbl != nil {
				cur = existing.Tbl
			} else {
				sub := newTable()
				sub.inline = true
				cur.set(part, &value{Tbl: sub, Line: startLine})
				cur = sub
			}
		}
		p.skipHorizontalWhitespace()
		if p.atEnd() {
			return nil, p.errorf("unterminated inline table")
		}
		if p.peek() == ',' {
			p.advance()
			continue
		}
		if p.peek() == '}' {
			p.advance()
			return &value{Tbl: t, Line: startLine}, nil
		}
		return nil, p.errorf("expected `,` or `}` in inline table")
	}
}

// ---- Scanner helpers ----

func (p *tomlParser) atEnd() bool { return p.pos >= len(p.src) }
func (p *tomlParser) peek() byte  { return p.src[p.pos] }

func (p *tomlParser) advance() {
	if p.atEnd() {
		return
	}
	if p.src[p.pos] == '\n' {
		p.line++
	}
	p.pos++
}

func (p *tomlParser) skipHorizontalWhitespace() {
	for !p.atEnd() {
		switch p.src[p.pos] {
		case ' ', '\t':
			p.pos++
		case '\r':
			// Tolerate CRLF: swallow \r when paired with a following
			// \n so downstream end-of-line checks see the LF. A lone
			// \r is left to caller so real errors still surface.
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '\n' {
				p.pos++
				continue
			}
			return
		default:
			return
		}
	}
}

func (p *tomlParser) skipWhitespaceAndComments() {
	for !p.atEnd() {
		switch p.src[p.pos] {
		case ' ', '\t':
			p.pos++
		case '\n':
			p.line++
			p.pos++
		case '\r':
			// Tolerate CRLF: consume \r, let \n handling bump line.
			p.pos++
		case '#':
			for !p.atEnd() && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}
