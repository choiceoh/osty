package selfhost

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost/api"
)

func TestInspectFromSourceEmpty(t *testing.T) {
	if got := InspectFromSource(nil); got != nil {
		t.Fatalf("nil src: want nil, got %d records", len(got))
	}
	if got := InspectFromSource([]byte{}); got != nil {
		t.Fatalf("empty src: want nil, got %d records", len(got))
	}
}

func TestInspectFromSourceEmitsRecords(t *testing.T) {
	src := []byte(`fn add(a: Int, b: Int) -> Int {
    let s = a + b
    s
}
`)
	recs := InspectFromSource(src)
	if len(recs) == 0 {
		t.Fatal("expected records, got 0")
	}
	for _, r := range recs {
		if r.Start < 0 || r.End < r.Start {
			t.Fatalf("invalid span on record %+v", r)
		}
		if r.NodeKind == "" {
			t.Fatalf("empty NodeKind on record %+v", r)
		}
	}
	var typed bool
	for _, r := range recs {
		if r.Type != nil && r.Type.String() != "" && r.Type.String() != "Invalid" {
			typed = true
			break
		}
	}
	if !typed {
		t.Fatalf("expected at least one structured inspect type, got %#v", recs)
	}
	if summary := kindSummary(recs); summary == "" {
		t.Fatal("empty kind summary")
	}
}

func kindSummary(recs []api.InspectRecord) string {
	var b strings.Builder
	for i, r := range recs {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(r.NodeKind)
	}
	return b.String()
}

func TestInspectFromSourceRecordsSorted(t *testing.T) {
	src := []byte(`fn id(x: Int) -> Int { x }
fn pair(a: Int, b: Int) -> (Int, Int) { (a, b) }
`)
	recs := InspectFromSource(src)
	if len(recs) < 2 {
		t.Skipf("too few records for ordering check: %d", len(recs))
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].Start < recs[i-1].Start {
			var buf strings.Builder
			for j, r := range recs {
				buf.WriteString(r.NodeKind)
				if j < len(recs)-1 {
					buf.WriteByte(' ')
				}
			}
			t.Fatalf("records not sorted by start offset at i=%d: %s", i, buf.String())
		}
	}
}

func TestInspectPackageStructuredUsesPackageImportSurface(t *testing.T) {
	src := []byte(`use strings

fn main() {
    let value = strings.len("abc")
    value
}
`)
	recs, err := InspectPackageStructured(PackageCheckInput{
		Files: []PackageCheckFile{{Source: src, Base: 0, Name: "main.osty"}},
		Imports: []PackageCheckImport{{
			Alias: "strings",
			Functions: []PackageCheckFn{{
				Name:           "len",
				ReturnTypeRepr: &api.TypeRepr{Kind: "primitive", Name: "Int"},
				ParamTypeReprs: []api.TypeRepr{{Kind: "primitive", Name: "String"}},
				ParamNames:     []string{"s"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("InspectPackageStructured: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("expected inspect records")
	}
	foundBinding := false
	for _, rec := range recs {
		if rec.Rule == "BIND" && rec.Type != nil {
			foundBinding = true
			break
		}
	}
	if !foundBinding {
		t.Fatalf("records missing typed BIND from package inspect: %#v", recs)
	}
}
