package llvmgen

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestCollectDeclarationsPreservesContainerMetadata(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Holder {
    names: List<String>
    scores: Map<String, Int>
    seen: Set<Int>
}

fn collect(names: List<String>, scores: Map<String, Int>) -> Set<Int> {}
`)

	decls, err := collectDeclarations(file)
	if err != nil {
		t.Fatalf("collectDeclarations returned error: %v", err)
	}

	holder := decls.structsByName["Holder"]
	if holder == nil {
		t.Fatal("missing Holder struct metadata")
	}
	if got := holder.byName["names"].listElemTyp; got != "ptr" {
		t.Fatalf("Holder.names listElemTyp = %q, want ptr", got)
	}
	if !holder.byName["names"].listElemString {
		t.Fatal("Holder.names listElemString = false, want true")
	}
	if got := holder.byName["scores"].mapKeyTyp; got != "ptr" {
		t.Fatalf("Holder.scores mapKeyTyp = %q, want ptr", got)
	}
	if got := holder.byName["scores"].mapValueTyp; got != "i64" {
		t.Fatalf("Holder.scores mapValueTyp = %q, want i64", got)
	}
	if !holder.byName["scores"].mapKeyString {
		t.Fatal("Holder.scores mapKeyString = false, want true")
	}
	if got := holder.byName["seen"].setElemTyp; got != "i64" {
		t.Fatalf("Holder.seen setElemTyp = %q, want i64", got)
	}

	sig := decls.functionsByName["collect"]
	if sig == nil {
		t.Fatal("missing collect signature")
	}
	if len(sig.params) != 2 {
		t.Fatalf("len(sig.params) = %d, want 2", len(sig.params))
	}
	if got := sig.params[0].listElemTyp; got != "ptr" {
		t.Fatalf("collect param0 listElemTyp = %q, want ptr", got)
	}
	if !sig.params[0].listElemString {
		t.Fatal("collect param0 listElemString = false, want true")
	}
	if got := sig.params[1].mapKeyTyp; got != "ptr" {
		t.Fatalf("collect param1 mapKeyTyp = %q, want ptr", got)
	}
	if got := sig.params[1].mapValueTyp; got != "i64" {
		t.Fatalf("collect param1 mapValueTyp = %q, want i64", got)
	}
	if !sig.params[1].mapKeyString {
		t.Fatal("collect param1 mapKeyString = false, want true")
	}
	if got := sig.retSetElemTyp; got != "i64" {
		t.Fatalf("collect return setElemTyp = %q, want i64", got)
	}
}

func TestSynthFnSigFromFnTypePreservesContainerMetadata(t *testing.T) {
	ft := &ast.FnType{
		Params: []ast.Type{
			&ast.NamedType{Path: []string{"List"}, Args: []ast.Type{
				&ast.NamedType{Path: []string{"String"}},
			}},
			&ast.NamedType{Path: []string{"Map"}, Args: []ast.Type{
				&ast.NamedType{Path: []string{"String"}},
				&ast.NamedType{Path: []string{"Int"}},
			}},
		},
		ReturnType: &ast.NamedType{Path: []string{"Set"}, Args: []ast.Type{
			&ast.NamedType{Path: []string{"Int"}},
		}},
	}

	sig, err := synthFnSigFromFnType(ft, typeEnv{})
	if err != nil {
		t.Fatalf("synthFnSigFromFnType returned error: %v", err)
	}

	if len(sig.params) != 2 {
		t.Fatalf("len(sig.params) = %d, want 2", len(sig.params))
	}
	if got := sig.params[0].listElemTyp; got != "ptr" {
		t.Fatalf("param0 listElemTyp = %q, want ptr", got)
	}
	if !sig.params[0].listElemString {
		t.Fatal("param0 listElemString = false, want true")
	}
	if got := sig.params[1].mapKeyTyp; got != "ptr" {
		t.Fatalf("param1 mapKeyTyp = %q, want ptr", got)
	}
	if got := sig.params[1].mapValueTyp; got != "i64" {
		t.Fatalf("param1 mapValueTyp = %q, want i64", got)
	}
	if !sig.params[1].mapKeyString {
		t.Fatal("param1 mapKeyString = false, want true")
	}
	if got := sig.retSetElemTyp; got != "i64" {
		t.Fatalf("return setElemTyp = %q, want i64", got)
	}
	if sig.returnSourceType != ft.ReturnType {
		t.Fatal("returnSourceType not preserved on synthesized fn sig")
	}
}

func TestSynthFnSigFromSourceTypeRecognizesFnTypeOnly(t *testing.T) {
	listType := &ast.NamedType{Path: []string{"List"}, Args: []ast.Type{
		&ast.NamedType{Path: []string{"Int"}},
	}}
	if sig, ok, err := synthFnSigFromSourceType(listType, typeEnv{}); err != nil {
		t.Fatalf("synthFnSigFromSourceType(non-fn) returned error: %v", err)
	} else if ok || sig != nil {
		t.Fatalf("synthFnSigFromSourceType(non-fn) = (%v, %v), want (nil, false)", sig, ok)
	}

	ft := &ast.FnType{
		Params: []ast.Type{
			&ast.NamedType{Path: []string{"Int"}},
		},
		ReturnType: &ast.NamedType{Path: []string{"Bool"}},
	}
	sig, ok, err := synthFnSigFromSourceType(ft, typeEnv{})
	if err != nil {
		t.Fatalf("synthFnSigFromSourceType(fn) returned error: %v", err)
	}
	if !ok {
		t.Fatal("synthFnSigFromSourceType(fn) = false, want true")
	}
	if sig == nil {
		t.Fatal("synthFnSigFromSourceType(fn) returned nil sig")
	}
	if got := sig.ret; got != "i1" {
		t.Fatalf("sig.ret = %q, want i1", got)
	}
	if len(sig.params) != 1 || sig.params[0].typ != "i64" {
		t.Fatalf("sig.params = %#v, want one i64 param", sig.params)
	}
}
