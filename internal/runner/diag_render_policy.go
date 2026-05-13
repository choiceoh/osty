// diag_render_policy.go is the Go snapshot of
// toolchain/diag_render.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import (
	"fmt"
	"strings"
)

// LineBoundsResult mirrors toolchain/diag_render.osty's
// LineBoundsResult. Start/End are -1 when the requested line is
// out of range; callers test `Start < 0` to detect the miss.
//
// Osty: toolchain/diag_render.osty:19
type LineBoundsResult struct {
	Start int
	End   int
}

// ColToRuneIndex converts a 1-based column (counted in Unicode
// scalar values) to a 0-based index into a rune sequence of
// `runeCount` elements. Columns <= 1 map to 0; columns past the
// end clamp to runeCount.
//
// Osty: toolchain/diag_render.osty:29
func ColToRuneIndex(runeCount, col int) int {
	if col <= 1 {
		return 0
	}
	idx := col - 1
	if idx > runeCount {
		idx = runeCount
	}
	return idx
}

// DigitWidth returns the number of decimal digits needed to
// render `n`. `n < 10` returns 1 (covers 0 and any small
// negative); otherwise count by repeated division.
//
// Osty: toolchain/diag_render.osty:45
func DigitWidth(n int) int {
	if n < 10 {
		return 1
	}
	w := 0
	for n > 0 {
		w++
		n /= 10
	}
	return w
}

// PadInt renders `n` and left-pads it with spaces to `width`
// columns. When the rendered number already has at least `width`
// characters the unpadded form is returned.
//
// Osty: toolchain/diag_render.osty:62
func PadInt(n, width int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// LineBounds returns the [start, end) byte offsets of the 1-based
// `line` in `src`, exclusive of the trailing newline. Returns
// (-1, -1) when line <= 0 or the source has fewer than `line`
// lines. The final line is reported even when it has no trailing
// newline.
//
// Osty: toolchain/diag_render.osty:78
func LineBounds(src []byte, line int) LineBoundsResult {
	if line <= 0 {
		return LineBoundsResult{Start: -1, End: -1}
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if cur == line && src[i] == '\n' {
			return LineBoundsResult{Start: start, End: i}
		}
		if src[i] == '\n' {
			cur++
			start = i + 1
		}
	}
	if cur == line {
		return LineBoundsResult{Start: start, End: len(src)}
	}
	return LineBoundsResult{Start: -1, End: -1}
}
