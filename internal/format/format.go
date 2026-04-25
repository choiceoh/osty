package format

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/token"
)

// Indent is the canonical per-level indent. `osty fmt` accepts no
// configuration (§13.3) — four spaces is fixed, matching the style used
// throughout the spec's examples.
const Indent = "    "

// MaxLineWidth is the soft width budget. Comma-separated constructs
// (call args, list/map/tuple literals, struct literals, fn params)
// try to render flat; if the flat form exceeds this column they
// re-render in multi-line form with a trailing comma. The formatter
// never breaks inside atomic tokens, so lines wider than the budget
// can still appear when no inner comma list would help.
const MaxLineWidth = 100

// indentCache holds a pre-built run of indent strings so write() can
// emit N levels of indent with one buffer append instead of N. Deeper
// nesting than cacheLevels falls back to the explicit loop.
const cacheLevels = 16

var indentCache = strings.Repeat(Indent, cacheLevels)

// Source formats the given Osty source bytes through the self-hosted
// formatter (toolchain/formatter_ast.osty). It returns the formatted
// output, any parse diagnostics produced along the way, and an error
// only when the input cannot be parsed (diagnostics contain at least
// one Error-severity entry) or the self-host formatter rejects the
// shape.
//
// Warnings do not block formatting — the formatter works on a
// best-effort AST when possible. Callers that want "format only clean
// files" should inspect the returned diagnostics themselves.
func Source(src []byte) ([]byte, []*diag.Diagnostic, error) {
	_, diags := parser.ParseDiagnostics(src)
	for _, d := range diags {
		if d.Severity == diag.Error {
			return nil, diags, fmt.Errorf("cannot format file with parse errors")
		}
	}
	out, _, err := selfhost.FormatSource(src)
	if err != nil {
		return nil, diags, err
	}
	return out, diags, nil
}

// File prints an already-parsed AST using the canonical Osty printer without
// reparsing or preserving source comments/trivia.
func File(file *ast.File) []byte {
	out, _ := FileWithMap(file)
	return out
}

// FileWithMap prints an already-parsed AST and returns a coarse source map
// from canonical output spans back to the original source spans carried on the
// AST.
func FileWithMap(file *ast.File) ([]byte, *sourcemap.Map) {
	if file == nil {
		return nil, nil
	}
	builder := sourcemap.NewBuilder()
	p := newPrinter(nil, builder)
	p.printFile(file)
	out := p.bytes()
	return out, builder.Build(out)
}

// printer is the mutable state carried through one formatting pass.
type printer struct {
	buf   bytes.Buffer
	level int
	// pendingNL and atLineStart together implement a deferred newline:
	// pendingNL is true when a newline has been requested but not yet
	// written to buf; atLineStart is true when the cursor already sits at
	// column 0 (either because flushNL just ran or no output exists yet).
	// The trailing-comment path needs both — "a newline is coming" vs
	// "we are already past it" mean different things for inlining.
	pendingNL   bool
	atLineStart bool

	// inlineOnly suppresses every multi-line rendering decision made
	// inside a single-quoted string interpolation. A `"..."` literal
	// cannot hold a newline (§1.6.3 — only `"""..."""` can), so any
	// `{expr}` nested in it must serialize flat regardless of width.
	// Set by printStringLit around each interpolated-expression emit;
	// printBracketedList and shouldBreakChain check it to force flat.
	inlineOnly bool

	// comments is the queue of yet-to-be-emitted comments in source
	// order. commentIdx is the sliding front pointer.
	comments   []token.Comment
	commentIdx int

	// lastSrcLine is the highest original-source line already accounted
	// for. Blank-line preservation uses a 2-line gap against this.
	lastSrcLine int

	// maps records coarse node-level output spans so canonical output can be
	// projected back onto original source spans.
	maps *sourcemap.Builder
}

func newPrinter(comments []token.Comment, maps *sourcemap.Builder) *printer {
	return &printer{
		comments:    comments,
		atLineStart: true,
		maps:        maps,
	}
}

// bytes finalizes the output and returns the formatted buffer. A
// formatted file ends with exactly one newline, trailing spaces are
// stripped, and consecutive blank lines collapse to one.
func (p *printer) bytes() []byte {
	p.emitRemainingComments()
	return finalize(p.buf.Bytes())
}

// snapshot captures every printer field that write/nl/blank and their
// downstream helpers can mutate, so a failed flat render attempt can
// be rolled back and retried in multi-line form. Importantly the
// buffer contents themselves are truncated on restore — any bytes
// appended during the speculative render vanish.
type snapshot struct {
	bufLen      int
	level       int
	pendingNL   bool
	atLineStart bool
	inlineOnly  bool
	commentIdx  int
	lastSrcLine int
	mapLen      int
}

func (p *printer) snapshot() snapshot {
	return snapshot{
		bufLen:      p.buf.Len(),
		level:       p.level,
		pendingNL:   p.pendingNL,
		atLineStart: p.atLineStart,
		inlineOnly:  p.inlineOnly,
		commentIdx:  p.commentIdx,
		lastSrcLine: p.lastSrcLine,
		mapLen:      p.maps.Snapshot(),
	}
}

func (p *printer) restore(s snapshot) {
	p.buf.Truncate(s.bufLen)
	p.level = s.level
	p.pendingNL = s.pendingNL
	p.atLineStart = s.atLineStart
	p.inlineOnly = s.inlineOnly
	p.commentIdx = s.commentIdx
	p.lastSrcLine = s.lastSrcLine
	p.maps.Restore(s.mapLen)
}

// currentCol returns the rune offset of the cursor within the current
// output line, counting a pending-but-unflushed newline as not yet
// started. Rune-based (not byte-based) because a multibyte character
// should count as one column — Hangul, CJK, and emoji code points
// appear in normal source and would otherwise blow the wrap decision
// by a factor of 2-4×.
func (p *printer) currentCol() int {
	if p.pendingNL || p.atLineStart {
		return 0
	}
	b := p.buf.Bytes()
	nl := bytes.LastIndexByte(b, '\n')
	start := 0
	if nl >= 0 {
		start = nl + 1
	}
	return utf8.RuneCount(b[start:])
}

// write emits a literal string at the current position, flushing any
// deferred newline and emitting the leading indent if we are sitting at
// column 0.
func (p *printer) write(s string) {
	if s == "" {
		return
	}
	if p.pendingNL {
		p.flushNL()
	}
	if p.atLineStart {
		p.emitIndent()
	}
	p.buf.WriteString(s)
}

func (p *printer) currentOffset() int {
	return p.buf.Len()
}

func (p *printer) materializeNodeStart() {
	if p.pendingNL {
		p.flushNL()
	}
	if p.atLineStart {
		p.emitIndent()
	}
}

func (p *printer) recordNode(kind string, span diag.Span, emit func()) {
	if p == nil {
		return
	}
	if p.maps == nil || (span.Start.Line == 0 && span.Start.Offset == 0 && span.End.Offset == 0) {
		emit()
		return
	}
	p.materializeNodeStart()
	start := p.currentOffset()
	emit()
	end := p.currentOffset()
	p.maps.Add(kind, start, end, span)
}

func spanOfNode(n ast.Node) diag.Span {
	if n == nil {
		return diag.Span{}
	}
	return diag.Span{Start: n.Pos(), End: n.End()}
}

// indentString returns the indent prefix for the given level, sliced
// from indentCache when level fits and falling back to strings.Repeat
// for unusually deep nesting. Shared between emitIndent (buffer write)
// and printTripleString (needs the prefix as a value to prepend after
// each content newline).
func indentString(level int) string {
	if level <= cacheLevels {
		return indentCache[:level*len(Indent)]
	}
	return strings.Repeat(Indent, level)
}

// emitIndent writes the leading indent for the current level.
func (p *printer) emitIndent() {
	p.buf.WriteString(indentString(p.level))
	p.atLineStart = false
}

// nl defers a newline until the next write. Idempotent: redundant calls
// are no-ops, so print paths can call nl() defensively without stacking
// blank lines. Explicit blanks go through p.blank().
func (p *printer) nl() {
	if p.atLineStart || p.pendingNL {
		return
	}
	p.pendingNL = true
}

// flushNL writes the pending newline to buf and marks the cursor as
// sitting at column 0.
func (p *printer) flushNL() {
	p.pendingNL = false
	p.buf.WriteByte('\n')
	p.atLineStart = true
}

// blank emits an unconditional blank line. finalize will collapse any
// accidental runs of blanks into one.
func (p *printer) blank() {
	if p.pendingNL {
		p.flushNL()
	}
	p.buf.WriteByte('\n')
	p.atLineStart = true
}

func (p *printer) indent() { p.level++ }
func (p *printer) dedent() { p.level-- }

// rawByte and rawString append directly to the output buffer without touching
// pendingNL / atLineStart or emitting indent. They exist so trivia- and
// triple-string-emission paths, which manage their own line boundaries, don't
// have to reach into p.buf directly.
func (p *printer) rawByte(b byte)     { p.buf.WriteByte(b) }
func (p *printer) rawString(s string) { p.buf.WriteString(s) }

// finalize walks the formatted buffer once, producing the final bytes:
// trailing whitespace on each line is stripped, runs of blank lines
// collapse to a single blank, leading blank lines are removed, and the
// output ends with exactly one newline. A single pass avoids the two
// full-buffer copies that a bytes.Split-based approach would require.
func finalize(in []byte) []byte {
	out := bytes.Buffer{}
	out.Grow(len(in) + 1)
	lineStart := 0
	prevBlank := false
	leading := true
	writeLine := func(line []byte) {
		// Trim trailing spaces/tabs.
		end := len(line)
		for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t') {
			end--
		}
		blank := end == 0
		if blank && (leading || prevBlank) {
			return
		}
		leading = false
		out.Write(line[:end])
		out.WriteByte('\n')
		prevBlank = blank
	}
	for i := 0; i < len(in); i++ {
		if in[i] == '\n' {
			writeLine(in[lineStart:i])
			lineStart = i + 1
		}
	}
	if lineStart < len(in) {
		writeLine(in[lineStart:])
	}
	b := out.Bytes()
	// Drop any trailing blank line(s) but keep exactly one final newline.
	for len(b) >= 2 && b[len(b)-1] == '\n' && b[len(b)-2] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return b
}
