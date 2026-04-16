package parser

import (
	"github.com/osty/osty/internal/diag"
)

// Provenance captures parser-owned normalization and lowering steps. Unlike
// airepair, these steps happen inside the front-end and therefore remain active
// even when higher-level repair is disabled.
type Provenance struct {
	Aliases   []ProvenanceStep
	Lowerings []ProvenanceStep
}

// ProvenanceStep describes one canonicalization the parser applied.
type ProvenanceStep struct {
	Kind        string
	SourceHabit string
	Span        diag.Span
	Detail      string
}

func (p *Provenance) Empty() bool {
	return p == nil || (len(p.Aliases) == 0 && len(p.Lowerings) == 0)
}
