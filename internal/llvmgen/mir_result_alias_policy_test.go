package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

func TestMirResultTypeTextParsesNestedArgs(t *testing.T) {
	typ := "Result<List<(Int, String)>, Error?>"
	if got := mirResultOkTypeText(typ); got != "List<(Int, String)>" {
		t.Fatalf("ok arg = %q", got)
	}
	if got := mirResultErrTypeText(typ); got != "Error?" {
		t.Fatalf("err arg = %q", got)
	}
}

func TestMirResultUnitTupleAliasPolicy(t *testing.T) {
	unitResult := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TUnit, ir.TString}, Builtin: true}
	tupleResult := &ir.NamedType{Name: "Result", Args: []ir.Type{&ir.TupleType{}, ir.TString}, Builtin: true}
	intResult := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TInt, ir.TString}, Builtin: true}

	if !compatibleResultUnitTupleAlias(unitResult, tupleResult) {
		t.Fatalf("Result<(), String> should alias empty-tuple Ok spelling")
	}
	if compatibleResultUnitTupleAlias(unitResult, intResult) {
		t.Fatalf("Result<(), String> must not alias Result<Int, String>")
	}
	if !resultUnitTupleLLVMTypeAlias("%Result.unit.ptr", "%Result.Tuple..ptr") {
		t.Fatalf("LLVM Result unit/Tuple type name alias not recognized")
	}
}

func TestMirCompatibleEnumAggregateHintPolicy(t *testing.T) {
	if !mirCompatibleEnumAggregateHintText("Result<Unit, String>", "Result<(), String>", "Ok") {
		t.Fatalf("Ok unit aggregate text should adopt tuple-context hint")
	}

	rv := &mir.AggregateRV{
		Kind:       mir.AggEnumVariant,
		T:          &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TInt, ir.TString}, Builtin: true},
		VariantTag: "Err",
	}
	hint := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TUnit, ir.TString}, Builtin: true}
	if got := compatibleEnumAggregateHint(rv, hint); got != hint {
		t.Fatalf("Err aggregate should adopt matching-Err Result hint")
	}

	badHint := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TUnit, ir.TInt}, Builtin: true}
	if got := compatibleEnumAggregateHint(rv, badHint); got != nil {
		t.Fatalf("mismatched Err type should not be hinted: %#v", got)
	}
}

func TestMirRetypeI64PairAggregateLines(t *testing.T) {
	got := mirRetypeI64PairAggregateLines("%disc", "%payload", "%tmp", "%out", "%Result.unit.ptr", "%Result.Tuple..ptr", "%value")
	for _, want := range []string{
		"  %disc = extractvalue %Result.unit.ptr %value, 0\n",
		"  %payload = extractvalue %Result.unit.ptr %value, 1\n",
		"  %tmp = insertvalue %Result.Tuple..ptr undef, i64 %disc, 0\n",
		"  %out = insertvalue %Result.Tuple..ptr %tmp, i64 %payload, 1\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestMirMonomorphMangledSourceName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "_ZTSN3std4List", want: "List"},
		{name: "_ZTSN4core6Result", want: "Result"},
		{name: "_ZTSN3std3MapI64", want: "Map"},
		{name: "List", want: ""},
		{name: "_ZTSN3std0", want: ""},
	} {
		if got := mirMonomorphMangledSourceName(tc.name); got != tc.want {
			t.Fatalf("mirMonomorphMangledSourceName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	if !mangledBuiltinSourceNameIs("_ZTSN3std4List", "List") {
		t.Fatalf("mangled builtin List predicate failed")
	}
	if mangledBuiltinSourceNameIs("_ZTSN3std4List", "Map") {
		t.Fatalf("mangled builtin predicate matched wrong source name")
	}
}
