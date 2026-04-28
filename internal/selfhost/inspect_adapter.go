package selfhost

import "github.com/osty/osty/internal/selfhost/api"

// InspectFromSource parses, type-checks, and runs the self-hosted
// inspect pass (toolchain/inspect.osty) on src, returning one record
// per observed AST node in source order.
//
// Records carry byte offsets (start-inclusive, end-exclusive) — the
// self-host pass emits token-indexed spans which this adapter converts
// via the lex stream, matching the semantics adaptCheckResult applies
// to structured CheckedNode records.
func InspectFromSource(src []byte) []api.InspectRecord {
	if len(src) == 0 {
		return nil
	}
	lexed := ostyLexSource(string(src))
	if lexed == nil {
		return nil
	}
	file := astParseLexedSource(lexed)
	checked := frontendCheckAstStructured(file)
	recs := inspectFromAstAndCheck(file, checked)
	rt := newRuneTable(lexed.source)
	return adaptInspectRecords(recs, rt, lexed.stream)
}

func adaptInspectRecords(recs []*InspectRecord, rt runeTable, stream *FrontLexStream) []api.InspectRecord {
	if len(recs) == 0 {
		return nil
	}
	out := make([]api.InspectRecord, 0, len(recs))
	for _, r := range recs {
		if r == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, r.start, r.end)
		out = append(out, api.InspectRecord{
			Start:    start,
			End:      end,
			NodeKind: r.nodeKind,
			Rule:     r.rule,
			Type:     parseTypeRepr(r.typeName),
			HintName: r.hintName,
			Notes:    append([]string(nil), r.notes...),
		})
	}
	return out
}
