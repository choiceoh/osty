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
	if parsed.File == nil || hasParseErrors(parsed.Diagnostics) {
		return repair.Result{Source: src}
	}
	if parsed.Provenance != nil && !parsed.Provenance.Empty() {
		canonicalSrc := canonical.Source(src, parsed.File)
		if len(canonicalSrc) == 0 || bytes.Equal(canonicalSrc, src) {
			return repair.Result{Source: src}
		}
		return repair.Result{
			Source:  canonicalSrc,
			Changes: provenanceChanges(parsed.Provenance),
		}
	}

	current := append([]byte(nil), src...)
	var changes []repair.Change
	if out, rewritten, ok := rewriteSemanticAppendLines(current); ok {
		current = out
		changes = append(changes, rewritten...)
	}
	if out, rewritten, ok := rewriteBuiltinLenCalls(current); ok {
		current = out
		changes = append(changes, rewritten...)
	}
	if len(changes) == 0 || bytes.Equal(current, src) {
		return repair.Result{Source: src}
	}
	return repair.Result{Source: current, Changes: changes}
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
	steps := make([]parser.ProvenanceStep, 0, len(prov.Aliases))
	steps = append(steps, prov.Aliases...)
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
