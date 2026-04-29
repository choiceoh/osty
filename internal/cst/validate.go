package cst

import "fmt"

// ValidationError describes one CST invariant violation.
type ValidationError struct {
	Message string
	Kind    GreenKind
	Offset  int
	End     int
}

func (e ValidationError) Error() string {
	if e.Kind == GkNone {
		return e.Message
	}
	return fmt.Sprintf("%s at %s[%d,%d)", e.Message, e.Kind, e.Offset, e.End)
}

// ValidationReport is the complete result of ValidateTree.
type ValidationReport struct {
	Errors []ValidationError
}

// OK reports whether the tree satisfied every checked invariant.
func (r ValidationReport) OK() bool { return len(r.Errors) == 0 }

// Error returns a compact human-readable summary.
func (r ValidationReport) Error() string {
	if len(r.Errors) == 0 {
		return ""
	}
	if len(r.Errors) == 1 {
		return r.Errors[0].Error()
	}
	return fmt.Sprintf("%s; and %d more", r.Errors[0].Error(), len(r.Errors)-1)
}

// ValidateTree checks the core Red/Green CST contract:
//
//   - root is a File node covering the full source buffer
//   - every node's width equals the sum of its children
//   - child ranges are ordered, non-overlapping, and inside their parent
//   - token widths match token text and attached trivia widths
//   - trivia ranges stay inside the source buffer
//   - reconstructing the tree yields the original source byte-for-byte
//
// It is intentionally strict enough for tests and debug assertions, while
// still cheap enough to run over corpus files in CI.
func ValidateTree(tree *Tree) ValidationReport {
	var report ValidationReport
	add := func(kind GreenKind, lo, hi int, msg string) {
		report.Errors = append(report.Errors, ValidationError{
			Message: msg,
			Kind:    kind,
			Offset:  lo,
			End:     hi,
		})
	}

	if tree == nil {
		add(GkNone, 0, 0, "tree is nil")
		return report
	}
	if tree.Arena == nil {
		add(GkNone, 0, 0, "tree arena is nil")
		return report
	}
	if tree.Root_ < 0 || tree.Root_ >= len(tree.Arena.Nodes) {
		add(GkNone, 0, 0, "root id is out of range")
		return report
	}

	root := tree.Root()
	if root.Kind() != GkFile {
		add(root.Kind(), root.Offset(), root.End(), "root is not File")
	}
	if tree.Source != nil && root.Width() != len(tree.Source) {
		add(root.Kind(), root.Offset(), root.End(), "root width does not match source length")
	}

	for id, tr := range tree.Arena.Trivias {
		if tr.Length < 0 {
			add(GkTrivia, tr.Offset, tr.Offset+tr.Length, fmt.Sprintf("trivia %d has negative length", id))
		}
		if tree.Source != nil && (tr.Offset < 0 || tr.Offset+tr.Length > len(tree.Source)) {
			add(GkTrivia, tr.Offset, tr.Offset+tr.Length, fmt.Sprintf("trivia %d escapes source", id))
		}
	}

	root.Walk(func(r Red) bool {
		lo, hi := r.TextRange()
		if tree.Source != nil && (lo < 0 || hi < lo || hi > len(tree.Source)) {
			add(r.Kind(), lo, hi, "red range escapes source")
			return true
		}
		if r.IsToken() {
			validateTokenRed(tree, r, add)
			return true
		}
		validateNodeRed(tree, r, add)
		return true
	})

	if tree.Source != nil {
		rebuilt := tree.Reconstruct()
		if string(rebuilt) != string(tree.Source) {
			add(GkFile, 0, root.End(), "tree reconstruction does not match source")
		}
	}
	return report
}

func validateTokenRed(tree *Tree, r Red, add func(GreenKind, int, int, string)) {
	tok := r.Token()
	if tok.Width != len(tok.Text) {
		add(r.Kind(), r.Offset(), r.End(), "token width does not match text length")
	}
	if tok.LeadingWidth != sumTriviaWidths(tree.Arena, tok.LeadingTrivia) {
		add(r.Kind(), r.Offset(), r.End(), "token leading width does not match trivia")
	}
	if tok.TrailingWidth != sumTriviaWidths(tree.Arena, tok.TrailingTrivia) {
		add(r.Kind(), r.Offset(), r.End(), "token trailing width does not match trivia")
	}
	if got, want := r.Width(), tok.TotalWidth(); got != want {
		add(r.Kind(), r.Offset(), r.End(), fmt.Sprintf("red token width = %d, want %d", got, want))
	}
	start, end, ok := r.TokenTextRange()
	if !ok || end-start != tok.Width {
		add(r.Kind(), r.Offset(), r.End(), "token text range is invalid")
	}
	if tree.Source != nil && ok && (start < 0 || end < start || end > len(tree.Source)) {
		add(r.Kind(), start, end, "token text range escapes source")
	}
}

func validateNodeRed(tree *Tree, r Red, add func(GreenKind, int, int, string)) {
	node := r.Node()
	if node.Width != r.Width() {
		add(r.Kind(), r.Offset(), r.End(), "node width and red width disagree")
	}
	sum := 0
	prevHi := r.Offset()
	for i := 0; i < r.ChildCount(); i++ {
		child := r.ChildAt(i)
		lo, hi := child.TextRange()
		if lo < prevHi {
			add(r.Kind(), r.Offset(), r.End(), fmt.Sprintf("child %d overlaps previous sibling", i))
		}
		if lo < r.Offset() || hi > r.End() {
			add(r.Kind(), r.Offset(), r.End(), fmt.Sprintf("child %d escapes parent", i))
		}
		prevHi = hi
		sum += child.Width()
	}
	if sum != node.Width {
		add(r.Kind(), r.Offset(), r.End(), fmt.Sprintf("node child widths sum to %d, want %d", sum, node.Width))
	}
	if r.Kind() != GkFile && !r.Kind().IsError() && r.Width() <= 0 {
		add(r.Kind(), r.Offset(), r.End(), "structural node has non-positive width")
	}
}

// Reconstruct rebuilds the exact source bytes represented by tree, using token
// text plus attached trivia. It returns nil for a nil tree.
func (t *Tree) Reconstruct() []byte {
	if t == nil {
		return nil
	}
	src := t.Source
	out := make([]byte, 0, len(src))
	emitTrivia := func(indices []int) {
		for _, id := range indices {
			if id < 0 || t.Arena == nil || id >= len(t.Arena.Trivias) {
				continue
			}
			tr := t.Arena.TriviaAt(id)
			lo, hi := tr.Offset, tr.Offset+tr.Length
			if lo < 0 {
				lo = 0
			}
			if hi > len(src) {
				hi = len(src)
			}
			if lo < hi {
				out = append(out, src[lo:hi]...)
			}
		}
	}
	t.Root().Walk(func(r Red) bool {
		if !r.IsToken() {
			return true
		}
		tok := r.Token()
		emitTrivia(tok.LeadingTrivia)
		out = append(out, tok.Text...)
		emitTrivia(tok.TrailingTrivia)
		return true
	})
	return out
}
