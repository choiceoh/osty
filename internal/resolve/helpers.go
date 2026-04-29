package resolve

import (
	"strings"

	"github.com/osty/osty/internal/ast"
)

// lastSeg returns the substring after the last occurrence of sep, or the
// whole string if sep does not appear. Used by selfhost projection to
// derive package-qualified names ("std/fs" → "fs").
func lastSeg(s string, sep byte) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == sep {
			return s[i+1:]
		}
	}
	return s
}

// stringArg extracts a literal string from an annotation argument
// expression. Returns ok=false for interpolated or non-string forms —
// annotations require literal arguments per the v0.5 spec.
func stringArg(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, p := range lit.Parts {
		if !p.IsLit {
			return "", false
		}
		b.WriteString(p.Lit)
	}
	return b.String(), true
}

// levenshteinBounded reports the Levenshtein distance between a and b,
// short-circuiting at limit. Used by did-you-mean diagnostics; the
// pbuf{1,2} arguments are scratch buffers the caller reuses across many
// candidate comparisons to avoid per-call allocation.
func levenshteinBounded(a, b string, limit int, pbuf1, pbuf2 *[]int) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la-lb >= limit || lb-la >= limit {
		return limit
	}
	prev := growInts(pbuf1, lb+1)
	cur := growInts(pbuf2, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := prev[j] + 1
			if x := cur[j-1] + 1; x < d {
				d = x
			}
			if x := prev[j-1] + cost; x < d {
				d = x
			}
			cur[j] = d
			if d < rowMin {
				rowMin = d
			}
		}
		if rowMin >= limit {
			return limit
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

func growInts(p *[]int, n int) []int {
	if cap(*p) < n {
		*p = make([]int, n)
		return *p
	}
	s := (*p)[:n]
	for i := range s {
		s[i] = 0
	}
	return s
}
