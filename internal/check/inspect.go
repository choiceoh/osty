package check

// Inspect adapts selfhost-owned inspector observations for the legacy Go
// formatting surface. The typechecker and inference-rule policy live in
// toolchain/check.osty and toolchain/inspect.osty; this file only converts
// byte spans to token.Pos and TypeRepr to display-friendly values.

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// InspectRecord is one observation of the inference algorithm at a single
// source range.
type InspectRecord struct {
	// Pos and End delimit the source range the record describes.
	Pos token.Pos
	End token.Pos
	// NodeKind is the syntactic category, e.g. "IntLit", "CallExpr", "If".
	NodeKind string
	// Rule is the label from LANG_SPEC_v0.5/02a-type-inference.md.
	Rule string
	// Type is retained for compatibility with callers that already consume
	// types.Type values. New formatting code prefers TypeName so it never has
	// to re-derive semantic type identity in Go.
	Type     types.Type
	TypeName string
	// Hint is the legacy structured hint slot. HintName is the selfhost-owned
	// rendered hint; formatting prefers HintName when present.
	Hint     types.Type
	HintName string
	Notes    []string
}

// Inspect preserves the historical signature for tests and package-internal
// callers. It can only produce selfhost-owned records when the Result came
// from a single-file check that remembered its source; otherwise callers
// should use InspectSource or InspectRecordsFromSelfhost directly.
func Inspect(_ *ast.File, chk *Result) []InspectRecord {
	if chk == nil || len(chk.inspectSource) == 0 {
		return nil
	}
	return InspectSource(chk.inspectSource, chk)
}

// InspectSource parses, checks, and inspects src through the selfhost path,
// then adapts the returned records for the legacy formatter.
func InspectSource(src []byte, _ *Result) []InspectRecord {
	return InspectRecordsFromSelfhost(src, selfhost.InspectFromSource(src))
}

// InspectRecordsFromSelfhost adapts already-produced selfhost inspect records.
// It is used by native package/workspace CLI paths that need to bucket records
// by file before rendering.
func InspectRecordsFromSelfhost(src []byte, recs []api.InspectRecord) []InspectRecord {
	if len(src) == 0 || len(recs) == 0 {
		return nil
	}
	out := make([]InspectRecord, 0, len(recs))
	for _, rec := range recs {
		span := byteRangeSpan(src, rec.Start, rec.End)
		typeName := ""
		var typ types.Type
		if rec.Type != nil {
			typeName = rec.Type.String()
			typ = typeReprToType(rec.Type)
		}
		out = append(out, InspectRecord{
			Pos:      span.Start,
			End:      span.End,
			NodeKind: rec.NodeKind,
			Rule:     rec.Rule,
			Type:     typ,
			TypeName: typeName,
			HintName: rec.HintName,
			Notes:    append([]string(nil), rec.Notes...),
		})
	}
	return out
}
