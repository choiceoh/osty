package parser

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// parsePipeline keeps parser-owned provenance collection in one place so the
// various entrypoints do not drift on which stages they report.
type parsePipeline struct {
	parsedSrc  []byte
	provenance Provenance
}

func newParsePipeline(src []byte) *parsePipeline {
	return &parsePipeline{parsedSrc: src}
}

func (p *parsePipeline) applySourceCompat(run *selfhost.FrontendRun) {
	if run == nil {
		return
	}
	aliases := run.StableAliases()
	if len(aliases) > 0 {
		for _, alias := range aliases {
			p.provenance.Aliases = append(p.provenance.Aliases, ProvenanceStep{
				Kind:        alias.Kind,
				SourceHabit: alias.SourceHabit,
				Span:        alias.Span,
				Detail:      alias.Detail,
			})
		}
	}
}

func (p *parsePipeline) parseRun() *selfhost.FrontendRun {
	return selfhost.Run(p.parsedSrc)
}

func (p *parsePipeline) applyArenaCompatProvenance(run *selfhost.FrontendRun) {
	if run == nil {
		return
	}
	lowerings := run.StableLowerings()
	if len(lowerings) == 0 {
		return
	}
	toks := run.Tokens()
	for _, lowering := range lowerings {
		p.provenance.Lowerings = append(p.provenance.Lowerings, ProvenanceStep{
			Kind:        lowering.Kind,
			SourceHabit: lowering.SourceHabit,
			Detail:      lowering.Detail,
			Span:        loweringTokenSpan(toks, lowering.Start, lowering.End),
		})
	}
}

func loweringTokenSpan(toks []token.Token, start, end int) diag.Span {
	pos := token.Pos{Line: 1, Column: 1}
	if len(toks) == 0 {
		return diag.Span{Start: pos, End: pos}
	}
	if start < 0 {
		start = 0
	}
	if start >= len(toks) {
		start = len(toks) - 1
	}
	startPos := toks[start].Pos
	endPos := toks[start].End
	endIdx := end - 1
	if endIdx < start {
		endIdx = start
	}
	if endIdx >= len(toks) {
		endIdx = len(toks) - 1
	}
	if endIdx >= 0 {
		endPos = toks[endIdx].End
	}
	return diag.Span{Start: startPos, End: endPos}
}

func (p *parsePipeline) result(file *ast.File, diags []*diag.Diagnostic) Result {
	return Result{
		File:        file,
		Diagnostics: diags,
		Provenance:  p.provenancePtr(),
	}
}

func (p *parsePipeline) provenancePtr() *Provenance {
	if len(p.provenance.Aliases) == 0 && len(p.provenance.Lowerings) == 0 {
		return nil
	}
	prov := p.provenance
	return &prov
}
