package scaffold

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/osty/osty/internal/diag"
)

// FFIOptions configures the C-header → Osty wrapper-stub generator.
//
// The generator is intentionally narrow: it scans for top-level C
// function declarations of the form `<retType> <name>(<args>);` and
// emits one Osty wrapper per match. It does not understand typedefs,
// macros, struct definitions, or function pointers — anything it
// can't parse is summarised as an `// unparsed:` comment so the user
// can refine the output by hand. The result is a starting point for a
// binding, not a finished one.
//
// Osty has no surface syntax for `extern fn` yet, so wrappers are
// emitted as `pub fn` with a `todo()` body and a doc comment that
// records the original C signature. When real FFI lands, the
// scaffold can switch to whatever syntax the spec adopts without the
// user having to retype the function names.
type FFIOptions struct {
	// Module is the Osty type/module label that namespaces the
	// generated wrappers. Used as the filename and as a `// {Module}
	// FFI bindings` header comment.
	Module string
	// Header is the raw C header bytes. Whitespace, multi-line
	// declarations, and `//` / `/* */` comments are tolerated; the
	// parser strips comments before scanning.
	Header []byte
}

// RenderFFI parses opts.Header and returns the generated Osty source.
// The output is always a syntactically valid Osty file, even when no
// declarations could be parsed — in that case it contains only the
// header comment plus a single `unparsed` note so the user gets a
// usable file to edit rather than an empty one.
func RenderFFI(opts FFIOptions) (string, *diag.Diagnostic) {
	if d := ValidateName(opts.Module); d != nil {
		return "", d
	}
	decls, unparsed := parseCDecls(opts.Header)

	module := identForName(opts.Module)
	var b strings.Builder
	fmt.Fprintf(&b, "// %s.osty — FFI binding stubs for the %q C header.\n", strings.ToLower(module), opts.Module)
	b.WriteString("//\n")
	b.WriteString("// Each wrapper records the original C signature in its doc\n")
	b.WriteString("// comment. The bodies are `todo()` placeholders — replace them\n")
	b.WriteString("// with real calls once the project picks an FFI mechanism\n")
	b.WriteString("// (cgo shim, std.ffi, or a user-supplied bridge).\n\n")
	b.WriteString("use std.process\n\n")

	if len(decls) == 0 {
		b.WriteString("// No C function declarations were recognised in the input.\n")
		b.WriteString("// The generator only handles top-level `<retType> <name>(<args>);`\n")
		b.WriteString("// forms — typedefs, macros, and struct definitions are skipped.\n")
	}
	for i, d := range decls {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "/// C: `%s`\n", d.original)
		fmt.Fprintf(&b, "pub fn %s(%s) -> %s {\n", camelCase(d.name), formatOstyParams(d.params), d.osRetType())
		fmt.Fprintf(&b, "    process.todo(\"%s: not yet wired through FFI\")\n", d.name)
		b.WriteString("}\n")
	}
	if len(unparsed) > 0 {
		b.WriteString("\n// unparsed declarations (the generator could not map these to Osty):\n")
		for _, line := range unparsed {
			fmt.Fprintf(&b, "//   %s\n", line)
		}
	}
	return b.String(), nil
}

// WriteFFI writes the rendered binding to `<dir>/<module>.osty`.
func WriteFFI(dir string, opts FFIOptions) (string, *diag.Diagnostic) {
	src, d := RenderFFI(opts)
	if d != nil {
		return "", d
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", ioErr(dir, err)
	}
	base := strings.ToLower(identForName(opts.Module))
	path := filepath.Join(abs, base+".osty")
	if _, err := os.Stat(path); err == nil {
		return "", existsDiag(path)
	} else if !os.IsNotExist(err) {
		return "", ioErr(path, err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return "", ioErr(path, err)
	}
	return path, nil
}

// cDecl captures one parsed C function declaration. `original` is
// the cleaned single-line form rendered into the doc comment;
// `params` may be empty for a no-arg function.
type cDecl struct {
	original string
	retType  string
	name     string
	params   []cParam
}

type cParam struct {
	name string
	typ  string
}

// osRetType maps the C return type to the closest Osty type. Void
// becomes `()` (Osty's unit type); pointer-to-`char` becomes
// `String`; the integer family collapses to `Int` or its sized
// variants. Anything we can't classify is rendered as `Json` so the
// stub still parses — the user is expected to refine the signature.
func (d cDecl) osRetType() string {
	return mapCType(d.retType)
}

// parseCDecls strips C/C++ comments and scans the result for
// top-level function declarations. It returns the parsed entries in
// declaration order plus a list of lines it considered but couldn't
// classify (so the user gets a checklist of what to clean up by hand).
func parseCDecls(src []byte) (parsed []cDecl, unparsed []string) {
	clean := stripCComments(src)
	// Glue declarations split across lines back together: real
	// headers wrap long argument lists. We cut on `;` after
	// normalising whitespace.
	var current strings.Builder
	flush := func() {
		stmt := strings.TrimSpace(collapseWS(current.String()))
		current.Reset()
		if stmt == "" {
			return
		}
		if d, ok := matchCDecl(stmt); ok {
			parsed = append(parsed, d)
		} else if isPotentialDecl(stmt) {
			unparsed = append(unparsed, stmt)
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(clean))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip preprocessor directives entirely — they're not
		// declarations and accumulating them into `current` would
		// glue them onto whatever real declaration appears next.
		if preprocessorRE.MatchString(line) {
			continue
		}
		current.WriteString(line)
		current.WriteString(" ")
		// A `;` terminates the statement. Some headers chain
		// declarations on one line; split on every `;`.
		if strings.Contains(line, ";") {
			parts := strings.Split(current.String(), ";")
			current.Reset()
			for i, p := range parts {
				if i == len(parts)-1 {
					// trailing fragment after the last `;` — keep
					// for the next iteration
					current.WriteString(p)
					continue
				}
				current.WriteString(p)
				current.WriteString(";")
				flush()
			}
		}
	}
	flush()
	return parsed, unparsed
}

// stripCComments removes both `//` line comments and `/* */` block
// comments. Strings are not stripped — headers don't generally
// contain `*/` inside string literals, and the alternative is a
// proper tokenizer which is overkill for a scaffolding tool.
func stripCComments(src []byte) []byte {
	var out bytes.Buffer
	in := src
	for len(in) > 0 {
		if len(in) >= 2 && in[0] == '/' && in[1] == '/' {
			i := bytes.IndexByte(in, '\n')
			if i < 0 {
				return out.Bytes()
			}
			out.WriteByte('\n')
			in = in[i+1:]
			continue
		}
		if len(in) >= 2 && in[0] == '/' && in[1] == '*' {
			end := bytes.Index(in[2:], []byte("*/"))
			if end < 0 {
				return out.Bytes()
			}
			out.WriteByte(' ')
			in = in[2+end+2:]
			continue
		}
		out.WriteByte(in[0])
		in = in[1:]
	}
	return out.Bytes()
}

func collapseWS(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' || r == ' ' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

// declRE captures `[storage] retType name(args);` where `args` is
// arbitrarily complex and may itself contain commas. We let the
// regex grab everything up to the closing `)` and parse the args
// list in a second pass.
var declRE = regexp.MustCompile(`^(?:extern\s+|static\s+)*([A-Za-z_][\w\s\*]*?)\s+\*?\s*([A-Za-z_]\w*)\s*\(([^)]*)\)\s*;?$`)

// preprocessorRE catches `#include`, `#define`, `#ifdef`, etc. Those
// lines are skipped silently — they're not declarations and we don't
// want them in the unparsed list.
var preprocessorRE = regexp.MustCompile(`^\s*#`)

func matchCDecl(stmt string) (cDecl, bool) {
	if preprocessorRE.MatchString(stmt) {
		return cDecl{}, false
	}
	m := declRE.FindStringSubmatch(stmt)
	if m == nil {
		return cDecl{}, false
	}
	retType := strings.TrimSpace(m[1])
	name := strings.TrimSpace(m[2])
	rawArgs := strings.TrimSpace(m[3])
	// Detect pointer-return: if the regex consumed the `*` into the
	// type, it's already there; if it sits between type and name,
	// our regex captures it as part of name with a leading `*`. Be
	// tolerant.
	if strings.HasPrefix(name, "*") {
		retType = retType + " *"
		name = strings.TrimPrefix(name, "*")
	}
	params, ok := parseCParams(rawArgs)
	if !ok {
		return cDecl{}, false
	}
	return cDecl{
		original: stmt,
		retType:  retType,
		name:     name,
		params:   params,
	}, true
}

// parseCParams splits a comma-separated parameter list. `void` (with
// no name) is treated as "no parameters". Anonymous parameters
// (`int`, `const char *`) get synthesised `arg0`-style names so the
// Osty signature is well-formed.
func parseCParams(raw string) ([]cParam, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "void" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	out := make([]cParam, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, false
		}
		// Variadic / function pointer / array param — out of scope
		// for the scaffold. Refuse the whole declaration so it
		// shows up in `unparsed` rather than producing a
		// half-correct Osty signature.
		if strings.Contains(p, "...") || strings.Contains(p, "(") || strings.Contains(p, "[") {
			return nil, false
		}
		fields := strings.Fields(p)
		var name, typ string
		if len(fields) == 1 {
			// Type-only parameter (e.g. `int`). Synthesise a name.
			name = fmt.Sprintf("arg%d", i)
			typ = fields[0]
		} else {
			last := fields[len(fields)-1]
			stars := ""
			for strings.HasPrefix(last, "*") {
				stars += "*"
				last = last[1:]
			}
			if last == "" {
				// `const char *` — the trailing `*` was alone; the
				// previous tokens are the full type and the param
				// has no name in the source.
				name = fmt.Sprintf("arg%d", i)
				typ = strings.TrimSpace(strings.Join(fields[:len(fields)-1], " ") + " " + stars)
			} else {
				// `const char *path` — `last` is the param name,
				// the stars belong on the type.
				name = last
				typ = strings.TrimSpace(strings.Join(fields[:len(fields)-1], " ") + " " + stars)
			}
		}
		out = append(out, cParam{name: camelCase(name), typ: typ})
	}
	return out, true
}

func formatOstyParams(ps []cParam) string {
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s: %s", p.name, mapCType(p.typ))
	}
	return b.String()
}

// mapCType maps a C type spelling to an Osty type. The mapping is
// deliberately small — anything we can't classify becomes `Json`,
// which is the project's opaque-value placeholder, so the generated
// file always parses.
func mapCType(c string) string {
	c = strings.TrimSpace(c)
	c = strings.ReplaceAll(c, "const ", "")
	c = collapseWS(c)
	c = strings.TrimSpace(c)
	if c == "" {
		return "()"
	}
	switch c {
	case "void":
		return "()"
	case "char *", "char*", "const char *", "char *const":
		return "String"
	case "int", "signed int":
		return "Int32"
	case "unsigned int", "uint":
		return "UInt32"
	case "long", "long int", "signed long":
		return "Int64"
	case "unsigned long", "unsigned long int":
		return "UInt64"
	case "short", "short int":
		return "Int16"
	case "unsigned short":
		return "UInt16"
	case "char", "signed char":
		return "Int8"
	case "unsigned char", "uint8_t":
		return "UInt8"
	case "uint16_t":
		return "UInt16"
	case "uint32_t":
		return "UInt32"
	case "uint64_t", "size_t":
		return "UInt64"
	case "int8_t":
		return "Int8"
	case "int16_t":
		return "Int16"
	case "int32_t":
		return "Int32"
	case "int64_t", "ssize_t":
		return "Int64"
	case "float":
		return "Float32"
	case "double":
		return "Float64"
	case "bool", "_Bool":
		return "Bool"
	}
	if strings.HasSuffix(c, "*") {
		// Pointer to something we don't recognise — Osty has no
		// raw-pointer surface, so the conservative scaffold maps it
		// to the opaque `Json` placeholder.
		return "Json"
	}
	return "Json"
}

// camelCase converts a snake_case C identifier to camelCase Osty
// style (spec §1.4). Leading underscores are preserved.
func camelCase(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	upper := false
	leading := true
	for _, r := range s {
		if r == '_' {
			if leading {
				b.WriteByte('_')
				continue
			}
			upper = true
			continue
		}
		leading = false
		if upper {
			b.WriteRune(toUpper(r))
			upper = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isPotentialDecl filters which not-recognised statements deserve to
// be flagged in `unparsed`. We only complain about lines that look
// vaguely like declarations — bare `}` from a struct close, blank
// remnants, and preprocessor lines are uninteresting.
func isPotentialDecl(stmt string) bool {
	if stmt == "" || stmt == "}" {
		return false
	}
	if preprocessorRE.MatchString(stmt) {
		return false
	}
	// Need at least one identifier and a `(` to be a function-ish
	// declaration; otherwise it's likely a struct field, typedef,
	// or extern variable — none of which the FFI scaffold handles.
	return strings.Contains(stmt, "(") && strings.Contains(stmt, ")")
}

