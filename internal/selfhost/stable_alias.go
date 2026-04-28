package selfhost

import (
	"github.com/osty/osty/internal/diag"
)

// StableAliasProvenance records a stable keyword alias accepted by the
// self-hosted parser. Alias parsing lives in toolchain/parser.osty; this Go
// surface only exposes the provenance event to callers that want to report it.
type StableAliasProvenance struct {
	Alias       string
	Canonical   string
	Kind        string
	SourceHabit string
	Detail      string
	Span        diag.Span
}

// StableAliases returns stable keyword aliases accepted by this front-end run.
// Calling it does not materialize the public *ast.File.
func (r *FrontendRun) StableAliases() []StableAliasProvenance {
	if r == nil || r.parser == nil {
		return nil
	}
	aliases := r.parser.stableAliases
	if len(aliases) == 0 {
		return nil
	}
	r.ensureLexAdapted()
	steps := make([]StableAliasProvenance, 0, len(aliases))
	for _, alias := range aliases {
		if alias == nil {
			continue
		}
		steps = append(steps, StableAliasProvenance{
			Alias:       alias.alias,
			Canonical:   alias.canonical,
			Kind:        alias.kind,
			SourceHabit: alias.sourceHabit,
			Detail:      alias.detail,
			Span:        r.stableAliasSpan(alias),
		})
	}
	return steps
}

func (r *FrontendRun) stableAliasSpan(alias *AstStableAlias) diag.Span {
	if alias == nil || alias.start < 0 || alias.start >= len(r.toks) {
		return diag.Span{}
	}
	startTok := r.toks[alias.start]
	endIdx := alias.end - 1
	if endIdx < alias.start {
		endIdx = alias.start
	}
	if endIdx >= len(r.toks) {
		endIdx = len(r.toks) - 1
	}
	endTok := r.toks[endIdx]
	return diag.Span{Start: startTok.Pos, End: endTok.End}
}
