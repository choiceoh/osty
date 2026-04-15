// Package tomlparse is the minimal TOML parser shared by
// internal/manifest (osty.toml) and internal/lockfile (osty.lock).
//
// Factored out because both consumers want the same subset:
//
//   - Bare and quoted keys; dotted keys (`a.b = 1`).
//   - String, integer, boolean scalars. No floats or dates.
//   - Inline tables (`{ k = v }`).
//   - Arrays of scalars and arrays of tables (`[[section]]`).
//   - Standard `[section]` headers.
//   - Comments (`#`) and whitespace (horizontal + newlines).
//
// Returned shape: the top level is a *Table whose iteration order
// matches insertion order, so marshaling can preserve the author's
// intent for manifest files. osty.lock is always emitted in a
// canonical sorted order by its caller, so key order on the way out
// is a caller concern.
//
// Error handling: every error carries a 1-based line number so the
// caller can prepend a filename and render it as "path:line: msg".
package tomlparse

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Value is the union of TOML values this parser produces. Callers
// branch on which of Str/Int/Bool/Arr/Tbl is non-nil.
type Value struct {
	Str  *string
	Int  *int64
	Bool *bool
	Arr  *Array
	Tbl  *Table
	// Line is the 1-based source line where the value begins. Zero
	// for values constructed by callers rather than parsed.
	Line int
}

// Table is an insertion-ordered map. The Keys slice mirrors the
// iteration order the callers want.
type Table struct {
	Keys   []string
	Items  map[string]*Value
	Inline bool // true for `{ ... }` tables; writers may honor this
	Line   int
}

// NewTable constructs an empty Table ready to be populated.
func NewTable() *Table {
	return &Table{Items: map[string]*Value{}}
}

// Set inserts or overwrites a key, updating Keys to preserve order.
func (t *Table) Set(key string, v *Value) {
	if _, ok := t.Items[key]; !ok {
		t.Keys = append(t.Keys, key)
	}
	t.Items[key] = v
}

// Get returns the value for key plus whether it was present.
func (t *Table) Get(key string) (*Value, bool) {
	v, ok := t.Items[key]
	return v, ok
}

// Array is either a value array or an array-of-tables. A mixed array
// is rejected at parse time.
type Array struct {
	Values []*Value
}

// Parse parses src into a root Table. Errors include line numbers.
func Parse(src []byte) (*Table, error) {
	p := parser{src: src, line: 1}
	return p.parseRoot()
}

type parser struct {
	src  []byte
	pos  int
	line int
}

func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("%d: %s", p.line, fmt.Sprintf(format, args...))
}

func (p *parser) parseRoot() (*Table, error) {
	root := NewTable()
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

func (p *parser) parseHeader(root *Table) (*Table, error) {
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
	p.skipHorizontalWhitespace()
	if !p.atEnd() && p.peek() != '\n' && p.peek() != '#' {
		return nil, p.errorf("garbage after table header")
	}
	t := root
	for i, part := range parts {
		last := i == len(parts)-1
		existing, ok := t.Items[part]
		switch {
		case !ok:
			var sub *Table
			if last && arrayOfTables {
				arr := &Array{}
				sub = NewTable()
				sub.Line = startLine
				arr.Values = append(arr.Values, &Value{Tbl: sub, Line: startLine})
				t.Set(part, &Value{Arr: arr, Line: startLine})
			} else {
				sub = NewTable()
				sub.Line = startLine
				t.Set(part, &Value{Tbl: sub, Line: startLine})
			}
			t = sub
		case existing.Tbl != nil:
			if last && arrayOfTables {
				return nil, p.errorf("table `%s` previously defined as a plain table", strings.Join(parts, "."))
			}
			t = existing.Tbl
		case existing.Arr != nil:
			if last && arrayOfTables {
				sub := NewTable()
				sub.Line = startLine
				existing.Arr.Values = append(existing.Arr.Values, &Value{Tbl: sub, Line: startLine})
				t = sub
				continue
			}
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

func (p *parser) parseKeyValue(t *Table) error {
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
	cur := t
	for i, part := range parts {
		if i == len(parts)-1 {
			if _, dup := cur.Items[part]; dup {
				return p.errorf("duplicate key `%s`", strings.Join(parts, "."))
			}
			v.Line = startLine
			cur.Set(part, v)
			break
		}
		if existing, ok := cur.Items[part]; ok {
			if existing.Tbl == nil {
				return p.errorf("key `%s` already set to non-table", strings.Join(parts[:i+1], "."))
			}
			cur = existing.Tbl
		} else {
			sub := NewTable()
			sub.Line = startLine
			cur.Set(part, &Value{Tbl: sub, Line: startLine})
			cur = sub
		}
	}
	p.skipHorizontalWhitespace()
	if !p.atEnd() && p.peek() != '\n' && p.peek() != '#' {
		return p.errorf("garbage after value")
	}
	return nil
}

func (p *parser) parseDottedKey() ([]string, error) {
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
		p.advance()
	}
}

func (p *parser) parseSingleKey() (string, error) {
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

func (p *parser) parseValue() (*Value, error) {
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
		return &Value{Str: &s, Line: p.line}, nil
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

func (p *parser) parseBool() (*Value, error) {
	if strings.HasPrefix(string(p.src[p.pos:]), "true") {
		p.pos += 4
		b := true
		return &Value{Bool: &b, Line: p.line}, nil
	}
	if strings.HasPrefix(string(p.src[p.pos:]), "false") {
		p.pos += 5
		b := false
		return &Value{Bool: &b, Line: p.line}, nil
	}
	return nil, p.errorf("expected bool (`true` or `false`)")
}

func (p *parser) parseNumber() (*Value, error) {
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
			p.advance()
			continue
		}
		if c == '.' || c == 'e' || c == 'E' {
			return nil, p.errorf("floating-point values are not supported")
		}
		break
	}
	raw := strings.ReplaceAll(string(p.src[start:p.pos]), "_", "")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, p.errorf("invalid integer %q: %v", raw, err)
	}
	return &Value{Int: &n, Line: p.line}, nil
}

func (p *parser) parseBasicString() (string, error) {
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

func (p *parser) parseArray() (*Value, error) {
	startLine := p.line
	if p.atEnd() || p.peek() != '[' {
		return nil, p.errorf("expected `[` to open array")
	}
	p.advance()
	arr := &Array{}
	for {
		p.skipWhitespaceAndComments()
		if p.atEnd() {
			return nil, p.errorf("unterminated array")
		}
		if p.peek() == ']' {
			p.advance()
			return &Value{Arr: arr, Line: startLine}, nil
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
			return &Value{Arr: arr, Line: startLine}, nil
		}
		return nil, p.errorf("expected `,` or `]` in array")
	}
}

func (p *parser) parseInlineTable() (*Value, error) {
	startLine := p.line
	if p.atEnd() || p.peek() != '{' {
		return nil, p.errorf("expected `{` to open inline table")
	}
	p.advance()
	t := NewTable()
	t.Inline = true
	t.Line = startLine
	p.skipHorizontalWhitespace()
	if !p.atEnd() && p.peek() == '}' {
		p.advance()
		return &Value{Tbl: t, Line: startLine}, nil
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
				cur.Set(part, v)
				break
			}
			if existing, ok := cur.Items[part]; ok && existing.Tbl != nil {
				cur = existing.Tbl
			} else {
				sub := NewTable()
				sub.Inline = true
				cur.Set(part, &Value{Tbl: sub, Line: startLine})
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
			return &Value{Tbl: t, Line: startLine}, nil
		}
		return nil, p.errorf("expected `,` or `}` in inline table")
	}
}

func (p *parser) atEnd() bool { return p.pos >= len(p.src) }
func (p *parser) peek() byte  { return p.src[p.pos] }

func (p *parser) advance() {
	if p.atEnd() {
		return
	}
	if p.src[p.pos] == '\n' {
		p.line++
	}
	p.pos++
}

func (p *parser) skipHorizontalWhitespace() {
	for !p.atEnd() {
		switch p.src[p.pos] {
		case ' ', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) skipWhitespaceAndComments() {
	for !p.atEnd() {
		switch p.src[p.pos] {
		case ' ', '\t':
			p.pos++
		case '\n':
			p.line++
			p.pos++
		case '\r':
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
