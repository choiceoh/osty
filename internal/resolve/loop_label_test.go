package resolve

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

func TestResolveUndefinedLoopLabel(t *testing.T) {
	src := []byte(`fn main() {
    for {
        break 'missing
    }
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) > 0 {
		t.Fatalf("parse diagnostics = %v", parseDiags)
	}
	res := File(file, NewPrelude())
	if !hasResolveCode(res.Diags, "E0763") {
		t.Fatalf("expected E0763, got %#v", res.Diags)
	}
}

func TestResolveLoopLabelShadow(t *testing.T) {
	src := []byte(`fn main() {
    'outer: for {
        'outer: for {
            break 'outer
        }
    }
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) > 0 {
		t.Fatalf("parse diagnostics = %v", parseDiags)
	}
	res := File(file, NewPrelude())
	if !hasResolveCode(res.Diags, "E0764") {
		t.Fatalf("expected E0764, got %#v", res.Diags)
	}
}

func TestResolveBreakValueExpression(t *testing.T) {
	src := []byte(`fn main() {
    let value = 1
    let result = loop {
        break value
    }
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) > 0 {
		t.Fatalf("parse diagnostics = %v", parseDiags)
	}
	res := File(file, NewPrelude())
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected resolve diagnostics: %#v", res.Diags)
	}
	for id, sym := range res.Refs {
		if id.Name == "value" && sym != nil && sym.Name == "value" {
			return
		}
	}
	t.Fatalf("break value reference to `value` was not resolved: %#v", res.Refs)
}

func hasResolveCode(diags []*diag.Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}
