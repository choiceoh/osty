//go:build selfhostgen

package gen

import (
	"bytes"
	"fmt"
)

// writer is an indent-aware byte buffer. Writes starting a new line are
// automatically prefixed with the current indent level (as tabs, so the
// output is gofmt-ready). Callers use indent()/dedent() to control nesting.
type writer struct {
	buf       bytes.Buffer
	level     int
	lineStart bool
}

func newWriter() *writer { return &writer{lineStart: true} }

// write appends s, inserting the current indent at any internal line starts.
func (w *writer) write(s string) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if w.lineStart && c != '\n' {
			for j := 0; j < w.level; j++ {
				w.buf.WriteByte('\t')
			}
			w.lineStart = false
		}
		w.buf.WriteByte(c)
		if c == '\n' {
			w.lineStart = true
		}
	}
}

// writef is write + fmt.Sprintf.
func (w *writer) writef(format string, args ...any) {
	w.write(fmt.Sprintf(format, args...))
}

// writeln writes s followed by a newline.
func (w *writer) writeln(s string) {
	w.write(s)
	w.write("\n")
}

// nl writes a bare newline.
func (w *writer) nl() { w.write("\n") }

// indent / dedent adjust the current level.
func (w *writer) indent() { w.level++ }
func (w *writer) dedent() {
	if w.level > 0 {
		w.level--
	}
}

// bytes returns the accumulated output.
func (w *writer) bytes() []byte { return w.buf.Bytes() }
