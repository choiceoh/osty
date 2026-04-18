package parser

// v0.5 (G30) §5 — `pub use` re-export.
//
// The self-hosted parser silently drops the `pub` keyword that
// precedes a `use`, producing a UseDecl with no record of the
// intended visibility. Regenerating the transpiled parser would be
// the clean fix, but `internal/bootstrap/gen` has pre-existing
// regressions that miscompile other toolchain-side Osty constructs,
// so we cannot run the regen pipeline today.
//
// The workaround: re-lex the source after parsing, locate every
// `pub` IDENT immediately followed by the `use` keyword, and flip
// `IsPub = true` on the matching UseDecl. Matching is done by
// statement-start offset — the position recorded on the UseDecl
// points at the `use` keyword, so we compare against the byte
// offset of the `use` token that follows our detected `pub`.
//
// When the bootstrap-gen regen is restored, this file can be
// replaced with the in-parser encoding (flags bit 1 on the AST
// node) by simply deleting markPubUseDecls and wiring the bit in
// `internal/selfhost/ast_lower.osty` instead.

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// markPubUseDecls walks the source token stream, finds every
// `pub use ...` pattern, and flips `IsPub = true` on the
// corresponding UseDecl in file. No-op when file is nil or has no
// use declarations.
func markPubUseDecls(src []byte, file *ast.File) {
	if file == nil || len(file.Uses) == 0 {
		return
	}
	toks, _, _ := selfhost.Lex(src)
	if len(toks) == 0 {
		return
	}

	// Index UseDecls by the byte offset of their `use` keyword (which
	// is also their Pos). Duplicate offsets are possible only on
	// malformed input; last one wins.
	byUseOffset := make(map[int]*ast.UseDecl, len(file.Uses))
	for _, u := range file.Uses {
		if u == nil {
			continue
		}
		byUseOffset[u.PosV.Offset] = u
	}

	for i := 0; i+1 < len(toks); i++ {
		// Match `pub` IDENT followed by `use` keyword on the same or
		// adjacent position — newlines between `pub` and `use` in
		// Osty would be a lex error anyway, so direct adjacency is
		// enough.
		if toks[i].Kind != token.PUB {
			continue
		}
		if toks[i+1].Kind != token.USE {
			continue
		}
		useOff := toks[i+1].Pos.Offset
		if u, ok := byUseOffset[useOff]; ok {
			u.IsPub = true
		}
	}
}
