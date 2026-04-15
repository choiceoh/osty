package format

import (
	"bytes"

	"github.com/osty/osty/internal/diag"
)

// OstySource formats src through the same AST-backed printer as Source.
//
// The first token-stream implementation of the "osty" engine deliberately
// tried to mirror the self-hosting formatter. That kept the implementation
// small, but it also meant every ambiguous token shape (`{}` as block vs
// pattern, `<` as generic vs comparison, keyword expressions, prefix ops)
// needed another heuristic. The highest-quality design is to make the
// formatter role-aware by construction: parse once, print declarations,
// statements, expressions, types, and patterns from the AST, and keep comments
// as trivia.
//
// This function is intentionally a thin named entry point over Source so the
// CLI can still expose --engine=osty while sharing one canonical formatting
// contract. The pure Osty formatter in examples/selfhost-core is tested
// separately as a self-hosting exercise; the CLI engine should not diverge
// from the production AST printer.
func OstySource(src []byte) ([]byte, []*diag.Diagnostic, error) {
	return Source(normalizeOstyFormatterInput(src))
}

func normalizeOstyFormatterInput(src []byte) []byte {
	src = bytes.TrimPrefix(src, []byte{0xEF, 0xBB, 0xBF})
	if !bytes.Contains(src, []byte{'\r'}) {
		return src
	}
	out := make([]byte, 0, len(src))
	for idx := 0; idx < len(src); idx++ {
		if src[idx] != '\r' {
			out = append(out, src[idx])
			continue
		}
		if idx+1 < len(src) && src[idx+1] == '\n' {
			continue
		}
		out = append(out, '\n')
	}
	return out
}
