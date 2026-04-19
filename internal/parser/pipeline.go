package parser

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

// parsePipeline keeps parser-owned source rewrites and AST repairs in one
// place so the various entrypoints do not drift on which stages they run.
type parsePipeline struct {
	parsedSrc  []byte
	provenance Provenance
}

func newParsePipeline(src []byte) *parsePipeline {
	return &parsePipeline{parsedSrc: src}
}

func (p *parsePipeline) applySourceCompat() {
	normalized, aliases := normalizeStableAliases(p.parsedSrc)
	p.parsedSrc = normalized
	if len(aliases) > 0 {
		p.provenance.Aliases = append(p.provenance.Aliases, aliases...)
	}
}

func (p *parsePipeline) parse() (*ast.File, []*diag.Diagnostic) {
	run := selfhost.Run(p.parsedSrc)
	return run.File(), run.Diagnostics()
}

func (p *parsePipeline) applyASTFixups(file *ast.File) {
	if file == nil {
		return
	}
	if lowerings := lowerStableAST(file); len(lowerings) > 0 {
		p.provenance.Lowerings = append(p.provenance.Lowerings, lowerings...)
	}
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
