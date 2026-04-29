package parser

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
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

func (p *parsePipeline) result(run *selfhost.FrontendRun, file *ast.File, diags []*diag.Diagnostic) Result {
	return Result{
		File:        file,
		Run:         run,
		Diagnostics: diags,
		Provenance:  p.provenancePtr(),
	}
}

func (p *parsePipeline) provenancePtr() *Provenance {
	if len(p.provenance.Aliases) == 0 {
		return nil
	}
	prov := p.provenance
	return &prov
}
