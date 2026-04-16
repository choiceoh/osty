package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

func assertGeneratedIRContains(t *testing.T, ir, want string) {
	t.Helper()
	if !strings.Contains(ir, want) {
		t.Fatalf("generated IR missing %q\nIR:\n%s", want, ir)
	}
}

func generateLLVMForTest(t *testing.T, src string) string {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) > 0 {
		t.Fatalf("parse returned %d diagnostics: %v", len(diags), diags[0])
	}
	ir, err := Generate(file, Options{SourcePath: "/tmp/managed.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	return string(ir)
}

func TestGeneratedRenderSkeletonInterpolatesQuotedFields(t *testing.T) {
	ir := llvmRenderSkeleton(
		"main",
		"/tmp/unsupported.osty",
		"llvm-ir",
		"x86_64-unknown-linux-gnu",
		"",
	)

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/unsupported.osty\"")
	assertGeneratedIRContains(t, ir, "target triple = \"x86_64-unknown-linux-gnu\"")
}

func TestGeneratedGcRuntimeAbiSmokeIR(t *testing.T) {
	ir := llvmSmokeGcRuntimeAbiIR("/tmp/gc_runtime_abi.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/gc_runtime_abi.osty\"")
	assertGeneratedIRContains(t, ir, "declare ptr @osty.gc.alloc_v1(i64, i64, ptr)")
	assertGeneratedIRContains(t, ir, "declare void @osty.gc.pre_write_v1(ptr, ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare void @osty.gc.post_write_v1(ptr, ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare ptr @osty.gc.load_v1(ptr)")
	assertGeneratedIRContains(t, ir, "declare void @osty.gc.root_bind_v1(ptr)")
	assertGeneratedIRContains(t, ir, "declare void @osty.gc.root_release_v1(ptr)")
	assertGeneratedIRContains(t, ir, "@.str0 = private unnamed_addr constant [15 x i8] c\"llvm.gc.object\\00\"")
	assertGeneratedIRContains(t, ir, "%t0 = call ptr @osty.gc.alloc_v1(i64 1, i64 32, ptr @.str0)")
	assertGeneratedIRContains(t, ir, "call void @osty.gc.pre_write_v1(ptr %t0, ptr %t1, i64 0)")
	assertGeneratedIRContains(t, ir, "%t2 = call ptr @osty.gc.load_v1(ptr %t1)")
	assertGeneratedIRContains(t, ir, "call void @osty.gc.post_write_v1(ptr %t0, ptr %t2, i64 0)")
	assertGeneratedIRContains(t, ir, "call void @osty.gc.root_release_v1(ptr %t0)")
}

func TestGeneratedClangLinkBinaryArgsAcceptMultipleObjects(t *testing.T) {
	args := llvmClangLinkBinaryArgs("", []string{"/tmp/main.o", "/tmp/runtime/gc_runtime.o"}, "/tmp/app")
	got := strings.Join(args, " ")

	assertGeneratedIRContains(t, got, "/tmp/main.o")
	assertGeneratedIRContains(t, got, "/tmp/runtime/gc_runtime.o")
	assertGeneratedIRContains(t, got, "-o /tmp/app")
}

func TestGeneratedComparePoliciesAreOstyOwned(t *testing.T) {
	if !llvmIsCompareOp("==") {
		t.Fatal(`llvmIsCompareOp("==") = false, want true`)
	}
	if llvmIsCompareOp("+") {
		t.Fatal(`llvmIsCompareOp("+") = true, want false`)
	}
	if got, want := llvmIntComparePredicate(">="), "sge"; got != want {
		t.Fatalf("llvmIntComparePredicate(%q) = %q, want %q", ">=", got, want)
	}
	if got, want := llvmFloatComparePredicate("<"), "olt"; got != want {
		t.Fatalf("llvmFloatComparePredicate(%q) = %q, want %q", "<", got, want)
	}
	if got, want := llvmIntBinaryInstruction("%"), "srem"; got != want {
		t.Fatalf("llvmIntBinaryInstruction(%q) = %q, want %q", "%", got, want)
	}
	if got, want := llvmIntBinaryInstruction("&"), "and"; got != want {
		t.Fatalf("llvmIntBinaryInstruction(%q) = %q, want %q", "&", got, want)
	}
	if got, want := llvmIntBinaryInstruction("|"), "or"; got != want {
		t.Fatalf("llvmIntBinaryInstruction(%q) = %q, want %q", "|", got, want)
	}
	if got, want := llvmIntBinaryInstruction("^"), "xor"; got != want {
		t.Fatalf("llvmIntBinaryInstruction(%q) = %q, want %q", "^", got, want)
	}
	if got, want := llvmIntBinaryInstruction("<<"), "shl"; got != want {
		t.Fatalf("llvmIntBinaryInstruction(%q) = %q, want %q", "<<", got, want)
	}
	if got, want := llvmIntBinaryInstruction(">>"), "ashr"; got != want {
		t.Fatalf("llvmIntBinaryInstruction(%q) = %q, want %q", ">>", got, want)
	}
	if got, want := llvmFloatBinaryInstruction("/"), "fdiv"; got != want {
		t.Fatalf("llvmFloatBinaryInstruction(%q) = %q, want %q", "/", got, want)
	}
	if got, want := llvmLogicalInstruction("&&"), "and"; got != want {
		t.Fatalf("llvmLogicalInstruction(%q) = %q, want %q", "&&", got, want)
	}
	if !llvmIsAsciiStringText("line one\n") {
		t.Fatal(`llvmIsAsciiStringText("line one\n") = false, want true`)
	}
	if llvmIsAsciiStringText("bad €") {
		t.Fatal(`llvmIsAsciiStringText("bad €") = true, want false`)
	}
	if !llvmIsIdent("value2") {
		t.Fatal(`llvmIsIdent("value2") = false, want true`)
	}
	if llvmIsIdent("2value") {
		t.Fatal(`llvmIsIdent("2value") = true, want false`)
	}
	if got, want := llvmFirstNonEmpty("", "fallback"), "fallback"; got != want {
		t.Fatalf("llvmFirstNonEmpty(%q, %q) = %q, want %q", "", "fallback", got, want)
	}
	if !llvmIsKnownRuntimeFfiPath("runtime.strings") {
		t.Fatal(`llvmIsKnownRuntimeFfiPath("runtime.strings") = false, want true`)
	}
	if llvmIsKnownRuntimeFfiPath("runtime.unknown") {
		t.Fatal(`llvmIsKnownRuntimeFfiPath("runtime.unknown") = true, want false`)
	}
	if got, want := llvmRuntimeFfiAlias("alias", "strings", "runtime.strings"), "alias"; got != want {
		t.Fatalf("llvmRuntimeFfiAlias(%q, %q, %q) = %q, want %q", "alias", "strings", "runtime.strings", got, want)
	}
	if got, want := llvmRuntimeFfiAlias("", "", "runtime.package.manifest"), "manifest"; got != want {
		t.Fatalf("llvmRuntimeFfiAlias(%q, %q, %q) = %q, want %q", "", "", "runtime.package.manifest", got, want)
	}
	if got, want := llvmRuntimeFfiSymbol("runtime.strings", "HasPrefix"), "osty_rt_strings_HasPrefix"; got != want {
		t.Fatalf("llvmRuntimeFfiSymbol(%q, %q) = %q, want %q", "runtime.strings", "HasPrefix", got, want)
	}
	if got, want := llvmBuiltinType("Int"), "i64"; got != want {
		t.Fatalf("llvmBuiltinType(%q) = %q, want %q", "Int", got, want)
	}
	if got, want := llvmBuiltinType("String"), "ptr"; got != want {
		t.Fatalf("llvmBuiltinType(%q) = %q, want %q", "String", got, want)
	}
	if got, want := llvmRuntimeAbiBuiltinType("Bool"), "i1"; got != want {
		t.Fatalf("llvmRuntimeAbiBuiltinType(%q) = %q, want %q", "Bool", got, want)
	}
	if got, want := llvmEnumPayloadBuiltinType("Float"), "double"; got != want {
		t.Fatalf("llvmEnumPayloadBuiltinType(%q) = %q, want %q", "Float", got, want)
	}
	if got, want := llvmZeroLiteral("double"), "0.0"; got != want {
		t.Fatalf("llvmZeroLiteral(%q) = %q, want %q", "double", got, want)
	}
	if got, want := llvmZeroLiteral("ptr"), "null"; got != want {
		t.Fatalf("llvmZeroLiteral(%q) = %q, want %q", "ptr", got, want)
	}
	if got, want := llvmZeroLiteral("i64"), "0"; got != want {
		t.Fatalf("llvmZeroLiteral(%q) = %q, want %q", "i64", got, want)
	}
	if got, want := llvmStructTypeName("Pair"), "%Pair"; got != want {
		t.Fatalf("llvmStructTypeName(%q) = %q, want %q", "Pair", got, want)
	}
	if got, want := llvmEnumStorageType("Choice", true), "%Choice"; got != want {
		t.Fatalf("llvmEnumStorageType(%q, true) = %q, want %q", "Choice", got, want)
	}
	if got, want := llvmEnumStorageType("Choice", false), "i64"; got != want {
		t.Fatalf("llvmEnumStorageType(%q, false) = %q, want %q", "Choice", got, want)
	}
	if got, want := llvmSignatureParamName("lhs", 0), "lhs"; got != want {
		t.Fatalf("llvmSignatureParamName(%q, %d) = %q, want %q", "lhs", 0, got, want)
	}
	if got, want := llvmSignatureParamName("", 2), "arg2"; got != want {
		t.Fatalf("llvmSignatureParamName(%q, %d) = %q, want %q", "", 2, got, want)
	}
	if !llvmAllowsMainSignature(0, false) {
		t.Fatal("llvmAllowsMainSignature(0, false) = false, want true")
	}
	if llvmAllowsMainSignature(1, false) {
		t.Fatal("llvmAllowsMainSignature(1, false) = true, want false")
	}
	if llvmAllowsMainSignature(0, true) {
		t.Fatal("llvmAllowsMainSignature(0, true) = true, want false")
	}
	if got, want := llvmNamedType("String", 1, 0, "", ""), "ptr"; got != want {
		t.Fatalf("llvmNamedType(%q, %d, %d, %q, %q) = %q, want %q", "String", 1, 0, "", "", got, want)
	}
	if got, want := llvmNamedType("Pair", 1, 0, "%Pair", ""), "%Pair"; got != want {
		t.Fatalf("llvmNamedType(%q, %d, %d, %q, %q) = %q, want %q", "Pair", 1, 0, "%Pair", "", got, want)
	}
	if got, want := llvmNamedType("Choice", 1, 0, "", "%Choice"), "%Choice"; got != want {
		t.Fatalf("llvmNamedType(%q, %d, %d, %q, %q) = %q, want %q", "Choice", 1, 0, "", "%Choice", got, want)
	}
	if got, want := llvmNamedType("List", 2, 0, "", ""), "ptr"; got != want {
		t.Fatalf("llvmNamedType(%q, %d, %d, %q, %q) = %q, want %q", "List", 2, 0, "", "", got, want)
	}
	if got, want := llvmNamedType("Unknown", 1, 0, "", ""), ""; got != want {
		t.Fatalf("llvmNamedType(%q, %d, %d, %q, %q) = %q, want %q", "Unknown", 1, 0, "", "", got, want)
	}
	if got, want := llvmRuntimeAbiNamedType("String", 1, 0, "", ""), "ptr"; got != want {
		t.Fatalf("llvmRuntimeAbiNamedType(%q, %d, %d, %q, %q) = %q, want %q", "String", 1, 0, "", "", got, want)
	}
	if got, want := llvmRuntimeAbiNamedType("Pair", 1, 0, "%Pair", ""), "%Pair"; got != want {
		t.Fatalf("llvmRuntimeAbiNamedType(%q, %d, %d, %q, %q) = %q, want %q", "Pair", 1, 0, "%Pair", "", got, want)
	}
	if got, want := llvmRuntimeAbiNamedType("List", 2, 1, "", ""), "ptr"; got != want {
		t.Fatalf("llvmRuntimeAbiNamedType(%q, %d, %d, %q, %q) = %q, want %q", "List", 2, 1, "", "", got, want)
	}
	if got, want := llvmEnumPayloadNamedType("Float", 1, 0), "double"; got != want {
		t.Fatalf("llvmEnumPayloadNamedType(%q, %d, %d) = %q, want %q", "Float", 1, 0, got, want)
	}
	if got, want := llvmEnumPayloadNamedType("Bytes", 1, 0), ""; got != want {
		t.Fatalf("llvmEnumPayloadNamedType(%q, %d, %d) = %q, want %q", "Bytes", 1, 0, got, want)
	}
	if got, want := llvmEnumPayloadNamedType("Float", 2, 0), ""; got != want {
		t.Fatalf("llvmEnumPayloadNamedType(%q, %d, %d) = %q, want %q", "Float", 2, 0, got, want)
	}
	if got := llvmNominalDeclHeaderDiagnostic("struct", "Pair", true, 0, 0); got.kind != "" {
		t.Fatalf("llvmNominalDeclHeaderDiagnostic(%q, %q, true, 0, 0).kind = %q, want empty", "struct", "Pair", got.kind)
	}
	if got, want := llvmNominalDeclHeaderDiagnostic("enum", "bad-name", false, 0, 0).message, `enum name "bad-name"`; got != want {
		t.Fatalf("llvmNominalDeclHeaderDiagnostic(%q, %q, false, 0, 0).message = %q, want %q", "enum", "bad-name", got, want)
	}
	if got, want := llvmNominalDeclHeaderDiagnostic("struct", "Pair", true, 1, 0).message, `generic struct "Pair" is not supported`; got != want {
		t.Fatalf("llvmNominalDeclHeaderDiagnostic(%q, %q, true, 1, 0).message = %q, want %q", "struct", "Pair", got, want)
	}
	if got := llvmFunctionHeaderDiagnostic("main", true, false, 0, true, true, 0, false); got.kind != "" {
		t.Fatalf("llvmFunctionHeaderDiagnostic(%q, true, false, 0, true, true, 0, false).kind = %q, want empty", "main", got.kind)
	}
	if got := llvmFunctionHeaderDiagnostic("methodish", true, true, 0, true, false, 0, false); got.kind != "" {
		t.Fatalf("llvmFunctionHeaderDiagnostic(%q, true, true, 0, true, false, 0, false).kind = %q, want empty", "methodish", got.kind)
	}
	if got, want := llvmFunctionHeaderDiagnostic("main", true, false, 0, true, true, 1, false).message, "LLVM main must have no params and no return type"; got != want {
		t.Fatalf("llvmFunctionHeaderDiagnostic(%q, true, false, 0, true, true, 1, false).message = %q, want %q", "main", got, want)
	}
	if !llvmIsRuntimeAbiListType("List", 1, 1) {
		t.Fatal(`llvmIsRuntimeAbiListType("List", 1, 1) = false, want true`)
	}
	if llvmIsRuntimeAbiListType("List", 1, 0) {
		t.Fatal(`llvmIsRuntimeAbiListType("List", 1, 0) = true, want false`)
	}
	if got, want := llvmStructFieldDiagnostic("Pair", "bad-name", false, false, false, false, "").message, `struct "Pair" field name "bad-name"`; got != want {
		t.Fatalf("llvmStructFieldDiagnostic(%q, %q, false, false, false, false, %q).message = %q, want %q", "Pair", "bad-name", "", got, want)
	}
	if got, want := llvmStructFieldDiagnostic("Pair", "left", true, false, true, false, "").message, `struct "Pair" duplicate field "left"`; got != want {
		t.Fatalf("llvmStructFieldDiagnostic(%q, %q, true, false, true, false, %q).message = %q, want %q", "Pair", "left", "", got, want)
	}
	if got, want := llvmStructFieldDiagnostic("Pair", "left", true, false, false, false, `type "Unknown"`).message, `struct "Pair" field "left": type "Unknown"`; got != want {
		t.Fatalf("llvmStructFieldDiagnostic(%q, %q, true, false, false, false, %q).message = %q, want %q", "Pair", "left", `type "Unknown"`, got, want)
	}
	if got, want := llvmEnumVariantHeaderDiagnostic("Choice", "bad-name", false, 0, false).message, `enum "Choice" variant name "bad-name"`; got != want {
		t.Fatalf("llvmEnumVariantHeaderDiagnostic(%q, %q, false, 0, false).message = %q, want %q", "Choice", "bad-name", got, want)
	}
	if got, want := llvmEnumVariantHeaderDiagnostic("Choice", "Some", true, 2, false).message, `enum "Choice" variant "Some" has 2 payload fields; only one scalar payload is supported`; got != want {
		t.Fatalf("llvmEnumVariantHeaderDiagnostic(%q, %q, true, 2, false).message = %q, want %q", "Choice", "Some", got, want)
	}
	if got, want := llvmEnumPayloadDiagnostic("Choice", "Some", "unsupported payload", "", "").message, `enum "Choice" variant "Some" payload: unsupported payload`; got != want {
		t.Fatalf("llvmEnumPayloadDiagnostic(%q, %q, %q, %q, %q).message = %q, want %q", "Choice", "Some", "unsupported payload", "", "", got, want)
	}
	if got, want := llvmEnumPayloadDiagnostic("Choice", "Some", "", "i64", "double").message, `enum "Choice" mixes payload types i64 and double`; got != want {
		t.Fatalf("llvmEnumPayloadDiagnostic(%q, %q, %q, %q, %q).message = %q, want %q", "Choice", "Some", "", "i64", "double", got, want)
	}
	if got, want := llvmRuntimeFfiHeaderUnsupported(true, 0), "methods are not supported"; got != want {
		t.Fatalf("llvmRuntimeFfiHeaderUnsupported(true, 0) = %q, want %q", got, want)
	}
	if got, want := llvmRuntimeFfiHeaderUnsupported(false, 1), "generic functions are not supported"; got != want {
		t.Fatalf("llvmRuntimeFfiHeaderUnsupported(false, 1) = %q, want %q", got, want)
	}
	if got, want := llvmRuntimeFfiReturnUnsupported(`type "Unknown"`), `return type: type "Unknown"`; got != want {
		t.Fatalf("llvmRuntimeFfiReturnUnsupported(%q) = %q, want %q", `type "Unknown"`, got, want)
	}
	if got, want := llvmRuntimeFfiParamUnsupported("", true, false, ""), "nil parameter"; got != want {
		t.Fatalf("llvmRuntimeFfiParamUnsupported(%q, true, false, %q) = %q, want %q", "", "", got, want)
	}
	if got, want := llvmRuntimeFfiParamUnsupported("arg0", false, true, ""), "pattern/default parameters are not supported"; got != want {
		t.Fatalf("llvmRuntimeFfiParamUnsupported(%q, false, true, %q) = %q, want %q", "arg0", "", got, want)
	}
	if got, want := llvmRuntimeFfiParamUnsupported("arg0", false, false, `type "Unknown"`), `parameter "arg0": type "Unknown"`; got != want {
		t.Fatalf("llvmRuntimeFfiParamUnsupported(%q, false, false, %q) = %q, want %q", "arg0", `type "Unknown"`, got, want)
	}
	if got, want := llvmFunctionReturnDiagnostic("sum", `type "Unknown"`).message, `function "sum" return type: type "Unknown"`; got != want {
		t.Fatalf("llvmFunctionReturnDiagnostic(%q, %q).message = %q, want %q", "sum", `type "Unknown"`, got, want)
	}
	if got, want := llvmFunctionParamDiagnostic("sum", "", true, false, true, "").message, `function "sum" has non-identifier parameter`; got != want {
		t.Fatalf("llvmFunctionParamDiagnostic(%q, %q, true, false, true, %q).message = %q, want %q", "sum", "", "", got, want)
	}
	if got, want := llvmFunctionParamDiagnostic("sum", "value", false, true, true, "").message, `function "sum" has default parameter values`; got != want {
		t.Fatalf("llvmFunctionParamDiagnostic(%q, %q, false, true, true, %q).message = %q, want %q", "sum", "value", "", got, want)
	}
	if got, want := llvmFunctionParamDiagnostic("sum", "bad-name", false, false, false, "").message, `parameter name "bad-name"`; got != want {
		t.Fatalf("llvmFunctionParamDiagnostic(%q, %q, false, false, false, %q).message = %q, want %q", "sum", "bad-name", "", got, want)
	}
	if got, want := llvmFunctionParamDiagnostic("sum", "value", false, false, true, `type "Unknown"`).message, `function "sum" parameter "value": type "Unknown"`; got != want {
		t.Fatalf("llvmFunctionParamDiagnostic(%q, %q, false, false, true, %q).message = %q, want %q", "sum", "value", `type "Unknown"`, got, want)
	}
}

func TestGenerateLeavesRawStringGlobalsUnmanagedUntilHeapLowering(t *testing.T) {
	ir := generateLLVMForTest(t, `
fn main() {
    let mut msg = "before"
    msg = "after"
    println(msg)
}
`)

	assertGeneratedIRContains(t, ir, "@.str0 = private unnamed_addr constant [7 x i8] c\"before\\00\"")
	assertGeneratedIRContains(t, ir, "@.str1 = private unnamed_addr constant [6 x i8] c\"after\\00\"")
	assertGeneratedIRContains(t, ir, "store ptr @.str1")
	assertGeneratedIRContains(t, ir, "  ret i32 0")
	if strings.Contains(ir, "@osty.gc.") {
		t.Fatalf("raw string globals should not opt into GC runtime yet\nIR:\n%s", ir)
	}
}
