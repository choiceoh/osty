package selfhost

import "github.com/osty/osty/internal/selfhost/api"

// InspectFromSource parses, type-checks, and runs the self-hosted
// inspect pass (toolchain/inspect.osty) on src, returning one record
// per observed AST node in source order.
//
// Records keep the self-host's raw form (byte offsets, rendered type
// strings) rather than the structured form used by
// internal/check.InspectRecord. See api.InspectRecord for the shape.
func InspectFromSource(src []byte) []api.InspectRecord {
	if len(src) == 0 {
		return nil
	}
	return adaptInspectRecords(inspectSource(string(src)))
}

func adaptInspectRecords(recs []*InspectRecord) []api.InspectRecord {
	if len(recs) == 0 {
		return nil
	}
	out := make([]api.InspectRecord, 0, len(recs))
	for _, r := range recs {
		if r == nil {
			continue
		}
		out = append(out, api.InspectRecord{
			Start:    r.start,
			End:      r.end,
			NodeKind: r.nodeKind,
			Rule:     r.rule,
			Type:     parseTypeRepr(r.typeName),
			HintName: r.hintName,
			Notes:    append([]string(nil), r.notes...),
		})
	}
	return out
}
