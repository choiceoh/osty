package format

import (
	"strings"

	"github.com/osty/osty/internal/token"
)

// triviaBefore emits any comments and blank lines that appeared in the
// source before target, and have not yet been emitted. It is the main
// hook for preserving authoring intent: every top-level decl and most
// sub-nodes call this before their own content is written.
//
// Rules:
//   - Line/block comments strictly before target.Line are emitted here.
//   - Doc comments are skipped without updating lastSrcLine — they will
//     be re-emitted from the AST by the declaration printer, and
//     callers that seeded lastSrcLine to the decl's own position rely
//     on us not clobbering it.
//   - A source gap of 2+ lines between lastSrcLine and target produces
//     one blank line of separation.
func (p *printer) triviaBefore(target token.Pos) {
	if target.Line == 0 {
		return
	}
	for p.commentIdx < len(p.comments) {
		c := p.comments[p.commentIdx]
		if c.Pos.Line >= target.Line {
			break
		}
		if c.Kind == token.CommentDoc {
			p.commentIdx++
			continue
		}
		if p.lastSrcLine > 0 && c.Pos.Line-p.lastSrcLine >= 2 {
			p.blank()
		}
		p.emitComment(c, false)
		p.commentIdx++
		p.lastSrcLine = c.EndLine
	}
	if p.lastSrcLine > 0 && target.Line-p.lastSrcLine >= 2 {
		p.blank()
	}
}

// trailingCommentAfter emits a same-line comment at endLine inline,
// turning `foo()  // note` in the source into a printed line with the
// note preserved. It consumes at most one comment; if the next queued
// comment is on a different line the call is a no-op.
func (p *printer) trailingCommentAfter(endLine int) {
	if endLine == 0 || p.commentIdx >= len(p.comments) {
		return
	}
	c := p.comments[p.commentIdx]
	if c.Pos.Line != endLine {
		return
	}
	// Cancel the pending newline so the comment goes on the current
	// line, not the next one.
	p.pendingNL = false
	p.rawByte(' ')
	p.emitComment(c, true)
	p.commentIdx++
	p.lastSrcLine = c.EndLine
	p.rawByte('\n')
	p.atLineStart = true
}

// emitRemainingComments flushes any comments that live beyond the last
// AST node (typical for a trailing footer comment).
func (p *printer) emitRemainingComments() {
	if p.commentIdx >= len(p.comments) {
		return
	}
	if p.pendingNL {
		p.flushNL()
	}
	for ; p.commentIdx < len(p.comments); p.commentIdx++ {
		c := p.comments[p.commentIdx]
		if c.Kind == token.CommentDoc {
			// Orphan doc comment at EOF — render as a regular line
			// comment since there is no declaration to attach it to.
			p.emitComment(c, false)
			continue
		}
		if p.lastSrcLine > 0 && c.Pos.Line-p.lastSrcLine >= 2 {
			p.blank()
		}
		p.emitComment(c, false)
		p.lastSrcLine = c.EndLine
	}
}

// emitComment writes a single comment. When inline, the caller has
// already placed the cursor and the leading space; otherwise the
// comment is emitted on its own line at the current indent.
func (p *printer) emitComment(c token.Comment, inline bool) {
	if !inline {
		if p.pendingNL {
			p.flushNL()
		}
		if !p.atLineStart {
			p.rawByte('\n')
			p.atLineStart = true
		}
		p.emitIndent()
	}
	switch c.Kind {
	case token.CommentLine, token.CommentDoc:
		prefix := "//"
		if c.Kind == token.CommentDoc {
			prefix = "///"
		}
		// Normalize surrounding whitespace: the canonical form is
		// `// text` — one space after the slashes, nothing before or
		// trailing. Multiple leading spaces on a source `//   foo`
		// collapse to one, matching gofmt convention. Internal runs
		// of whitespace are preserved since authors sometimes use
		// them for alignment or ASCII art.
		txt := strings.TrimSpace(c.Text)
		p.rawString(prefix)
		if txt != "" {
			p.rawByte(' ')
			p.rawString(txt)
		}
	case token.CommentBlock:
		// Multi-line block comments keep their internal layout verbatim
		// since authors often draw ASCII art inside.
		p.rawString("/*")
		p.rawString(c.Text)
		p.rawString("*/")
	}
	if !inline {
		p.rawByte('\n')
		p.atLineStart = true
	}
}

// markSrcLine advances lastSrcLine monotonically.
func (p *printer) markSrcLine(line int) {
	if line > p.lastSrcLine {
		p.lastSrcLine = line
	}
}
