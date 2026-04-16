package airepair

import (
	"bytes"

	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/repair"
)

func parserCanonicalSource(src []byte) repair.Result {
	parsed := parser.ParseDetailed(src)
	if parsed.File == nil || hasParseErrors(parsed.Diagnostics) || parsed.Provenance == nil || parsed.Provenance.Empty() {
		return repair.Result{Source: src}
	}
	canonicalSrc := canonical.Source(src, parsed.File)
	if len(canonicalSrc) == 0 || bytes.Equal(canonicalSrc, src) {
		return repair.Result{Source: src}
	}
	return repair.Result{
		Source:  canonicalSrc,
		Changes: provenanceChanges(parsed.Provenance),
	}
}

func hasParseErrors(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func provenanceChanges(prov *parser.Provenance) []repair.Change {
	if prov == nil {
		return nil
	}
	steps := make([]parser.ProvenanceStep, 0, len(prov.Aliases)+len(prov.Lowerings))
	steps = append(steps, prov.Aliases...)
	steps = append(steps, prov.Lowerings...)
	out := make([]repair.Change, 0, len(steps))
	for _, step := range steps {
		out = append(out, repair.Change{
			Kind:    provenanceChangeKind(step.Kind),
			Message: step.Detail,
			Pos:     step.Span.Start,
		})
	}
	return out
}

func provenanceChangeKind(kind string) string {
	switch kind {
	case "stable_function_keyword":
		return "function_keyword"
	case "stable_use_keyword":
		return "import_keyword"
	case "stable_while_keyword":
		return "while_keyword"
	default:
		return kind
	}
}
