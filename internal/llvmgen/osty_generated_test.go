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
	ir, err := generateFromAST(file, Options{SourcePath: "/tmp/managed.osty"})
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

func TestGeneratedSafepointChunkPlanIsOstyOwned(t *testing.T) {
	chunks := llvmPlanSafepointChunks(llvmSafepointKindLoop(), 11, 7, 3)
	if got, want := len(chunks), 3; got != want {
		t.Fatalf("len(chunks) = %d, want %d", got, want)
	}
	if got, want := chunks[0].start, 0; got != want {
		t.Fatalf("chunks[0].start = %d, want %d", got, want)
	}
	if got, want := chunks[0].end, 3; got != want {
		t.Fatalf("chunks[0].end = %d, want %d", got, want)
	}
	if got, want := chunks[0].id, llvmEncodeSafepointId(llvmSafepointKindLoop(), 11); got != want {
		t.Fatalf("chunks[0].id = %d, want %d", got, want)
	}
	if got, want := chunks[1].start, 3; got != want {
		t.Fatalf("chunks[1].start = %d, want %d", got, want)
	}
	if got, want := chunks[1].end, 6; got != want {
		t.Fatalf("chunks[1].end = %d, want %d", got, want)
	}
	if got, want := chunks[1].id, llvmEncodeSafepointId(llvmSafepointKindLoop(), 12); got != want {
		t.Fatalf("chunks[1].id = %d, want %d", got, want)
	}
	if got, want := chunks[2].start, 6; got != want {
		t.Fatalf("chunks[2].start = %d, want %d", got, want)
	}
	if got, want := chunks[2].end, 7; got != want {
		t.Fatalf("chunks[2].end = %d, want %d", got, want)
	}
	if got, want := chunks[2].id, llvmEncodeSafepointId(llvmSafepointKindLoop(), 13); got != want {
		t.Fatalf("chunks[2].id = %d, want %d", got, want)
	}

	whole := llvmPlanSafepointChunks(llvmSafepointKindCall(), 5, 4, 0)
	if got, want := len(whole), 1; got != want {
		t.Fatalf("len(whole) = %d, want %d", got, want)
	}
	if got, want := whole[0].start, 0; got != want {
		t.Fatalf("whole[0].start = %d, want %d", got, want)
	}
	if got, want := whole[0].end, 4; got != want {
		t.Fatalf("whole[0].end = %d, want %d", got, want)
	}
	if got, want := whole[0].id, llvmEncodeSafepointId(llvmSafepointKindCall(), 5); got != want {
		t.Fatalf("whole[0].id = %d, want %d", got, want)
	}

	if got := len(llvmPlanSafepointChunks(llvmSafepointKindCall(), 99, 0, 3)); got != 0 {
		t.Fatalf("empty chunk plan len = %d, want 0", got)
	}
}

func TestGeneratedClangLinkBinaryArgsAcceptMultipleObjects(t *testing.T) {
	args := llvmClangLinkBinaryArgs("", []string{"/tmp/main.o", "/tmp/runtime/gc_runtime.o"}, "/tmp/app")
	got := strings.Join(args, " ")

	assertGeneratedIRContains(t, got, "/tmp/main.o")
	assertGeneratedIRContains(t, got, "/tmp/runtime/gc_runtime.o")
	assertGeneratedIRContains(t, got, "-o /tmp/app")
}

// The POSIX runtime branch needs -pthread to pull libpthread / libSystem.
// The Windows runtime branch uses Win32 primitives from kernel32 and
// clang-msvc rejects -pthread, so the flag must be dropped for windows
// targets. This test pins the per-target branching.
func TestGeneratedClangLinkBinaryArgsPthreadWindowsBranching(t *testing.T) {
	cases := []struct {
		target string
		want   bool // true = expect -pthread, false = expect NO -pthread
	}{
		{"", true},
		{"x86_64-unknown-linux-gnu", true},
		{"aarch64-unknown-linux-gnu", true},
		{"x86_64-apple-darwin", true},
		{"arm64-apple-darwin", true},
		{"x86_64-pc-windows-msvc", false},
		{"aarch64-pc-windows-msvc", false},
		{"x86_64-pc-windows-gnu", false},
	}
	for _, c := range cases {
		args := llvmClangLinkBinaryArgs(c.target, []string{"/tmp/main.o"}, "/tmp/app")
		got := strings.Join(args, " ")
		has := strings.Contains(got, "-pthread")
		if has != c.want {
			t.Errorf("target=%q: -pthread present=%v, want=%v; full args: %s",
				c.target, has, c.want, got)
		}
	}
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
	// Non-ASCII text is now accepted — llvmCStringEscape byte-escapes
	// every non-printable / high byte via `\HH`, so the gate is a
	// no-op. The earlier `"bad €" => false` assertion was the legacy
	// ASCII-only behaviour; see commit history / PR on the
	// string_non_ascii wall for context.
	if !llvmIsAsciiStringText("bom \ufeff ok") {
		t.Fatal(`llvmIsAsciiStringText("bom \ufeff ok") = false, want true`)
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
	if !llvmIsKnownRuntimeFfiPath("runtime.cabi") {
		t.Fatal(`llvmIsKnownRuntimeFfiPath("runtime.cabi") = false, want true`)
	}
	if !llvmIsKnownRuntimeFfiPath("runtime.cabi.libm") {
		t.Fatal(`llvmIsKnownRuntimeFfiPath("runtime.cabi.libm") = false, want true`)
	}
	if got, want := llvmRuntimeFfiSymbol("runtime.cabi.libm", "cos"), "cos"; got != want {
		t.Fatalf("llvmRuntimeFfiSymbol(%q, %q) = %q, want %q", "runtime.cabi.libm", "cos", got, want)
	}
	if got, want := llvmRuntimeFfiSymbol("runtime.cabi", "abort"), "abort"; got != want {
		t.Fatalf("llvmRuntimeFfiSymbol(%q, %q) = %q, want %q", "runtime.cabi", "abort", got, want)
	}
	if got, want := llvmRuntimeFfiAlias("", "libm", "runtime.cabi.libm"), "libm"; got != want {
		t.Fatalf("llvmRuntimeFfiAlias(%q, %q, %q) = %q, want %q", "", "libm", "runtime.cabi.libm", got, want)
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
	if got := llvmEnumVariantHeaderDiagnostic("Choice", "Some", true, 2, false); got.kind != "" {
		t.Fatalf("llvmEnumVariantHeaderDiagnostic multi-field should no longer reject: kind=%q msg=%q", got.kind, got.message)
	}
	if got := llvmEnumBoxedMultiFieldDiagnostic("Choice", "Mix", 2); got.code != "LLVM011" || got.hint == "" {
		t.Fatalf("llvmEnumBoxedMultiFieldDiagnostic: code=%q hint=%q, want LLVM011 + non-empty hint", got.code, got.hint)
	}
	if got, want := llvmEnumBoxedMultiFieldDiagnostic("Choice", "Mix", 2).message, `enum "Choice" variant "Mix" has 2 payload fields with heterogeneous types across variants; boxed multi-field payloads are not supported yet`; got != want {
		t.Fatalf("llvmEnumBoxedMultiFieldDiagnostic message = %q, want %q", got, want)
	}
	if got := llvmStructFieldDiagnostic("Tree", "left", true, false, false, true, ""); got.code != "LLVM011" || got.hint == "" {
		t.Fatalf("llvmStructFieldDiagnostic recursive: code=%q hint=%q, want LLVM011 + non-empty hint", got.code, got.hint)
	}
	if got, want := llvmStructFieldDiagnostic("Tree", "left", true, false, false, true, "").message, `struct "Tree" recursive field "left" requires indirection`; got != want {
		t.Fatalf("llvmStructFieldDiagnostic recursive message = %q, want %q", got, want)
	}
	if got, want := llvmEnumPayloadDiagnostic("Choice", "Some", "unsupported payload", "", "").message, `enum "Choice" variant "Some" payload: unsupported payload`; got != want {
		t.Fatalf("llvmEnumPayloadDiagnostic(%q, %q, %q, %q, %q).message = %q, want %q", "Choice", "Some", "unsupported payload", "", "", got, want)
	}
	if got, want := llvmEnumPayloadDiagnostic("Choice", "Some", "", "i64", "double").message, `enum "Choice" mixes payload types i64 and double; heterogeneous-payload enums require boxed representation (deferred)`; got != want {
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

func TestGeneratedListPushDispatchesBySuffix(t *testing.T) {
	cases := []struct {
		valTyp string
		valReg string
		want   string
	}{
		{"i64", "42", "call void @osty_rt_list_push_i64(ptr %list, i64 42)"},
		{"i1", "true", "call void @osty_rt_list_push_i1(ptr %list, i1 true)"},
		{"double", "3.14", "call void @osty_rt_list_push_f64(ptr %list, double 3.14)"},
		{"ptr", "@.str0", "call void @osty_rt_list_push_ptr(ptr %list, ptr @.str0)"},
	}
	for _, c := range cases {
		em := llvmEmitter()
		llvmListPush(em,
			&LlvmValue{typ: "ptr", name: "%list"},
			&LlvmValue{typ: c.valTyp, name: c.valReg})
		got := strings.Join(em.body, "\n")
		assertGeneratedIRContains(t, got, c.want)
	}
}

func TestGeneratedListRuntimeSymbolsAreOstyOwned(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"new", llvmListRuntimeNewSymbol(), "osty_rt_list_new"},
		{"len", llvmListRuntimeLenSymbol(), "osty_rt_list_len"},
		{"push_i64", llvmListRuntimePushSymbol("i64"), "osty_rt_list_push_i64"},
		{"push_f64", llvmListRuntimePushSymbol("f64"), "osty_rt_list_push_f64"},
		{"get_ptr", llvmListRuntimeGetSymbol("ptr"), "osty_rt_list_get_ptr"},
		{"set_i1", llvmListRuntimeSetSymbol("i1"), "osty_rt_list_set_i1"},
		{"sorted_i64", llvmListRuntimeSortedSymbol("i64", false), "osty_rt_list_sorted_i64"},
		{"sorted_string", llvmListRuntimeSortedSymbol("ptr", true), "osty_rt_list_sorted_string"},
		{"sorted_unsupported", llvmListRuntimeSortedSymbol("%MyStruct", false), ""},
		{"to_set_f64", llvmListRuntimeToSetSymbol("double", false), "osty_rt_list_to_set_f64"},
		{"to_set_string", llvmListRuntimeToSetSymbol("ptr", true), "osty_rt_list_to_set_string"},
		{"to_set_unsupported", llvmListRuntimeToSetSymbol("%MyStruct", false), ""},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestGeneratedListElementSuffixMatchesRuntimeAbi(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"i64", "i64"},
		{"i1", "i1"},
		{"ptr", "ptr"},
		{"double", "f64"},
		{"", "ptr"},
		{"%MyStruct", "_MyStruct"},
		{"<4 x i32>", "_4_x_i32_"},
	}
	for _, c := range cases {
		if got := llvmListElementSuffix(c.in); got != c.want {
			t.Fatalf("llvmListElementSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	typed := []string{"i64", "i1", "double", "ptr"}
	for _, t0 := range typed {
		if !llvmListUsesTypedRuntime(t0) {
			t.Fatalf("llvmListUsesTypedRuntime(%q) = false, want true", t0)
		}
	}
	for _, t0 := range []string{"", "%MyStruct", "float"} {
		if llvmListUsesTypedRuntime(t0) {
			t.Fatalf("llvmListUsesTypedRuntime(%q) = true, want false", t0)
		}
	}
}

func TestGeneratedListRuntimeDeclarationsAreOstyOwned(t *testing.T) {
	decls := strings.Join(llvmListRuntimeDeclarations(), "\n")

	want := []string{
		"declare ptr @osty_rt_list_new()",
		"declare i64 @osty_rt_list_len(ptr)",
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"declare void @osty_rt_list_push_i1(ptr, i1)",
		"declare void @osty_rt_list_push_f64(ptr, double)",
		"declare void @osty_rt_list_push_ptr(ptr, ptr)",
		"declare i64 @osty_rt_list_get_i64(ptr, i64)",
		"declare i1 @osty_rt_list_get_i1(ptr, i64)",
		"declare double @osty_rt_list_get_f64(ptr, i64)",
		"declare ptr @osty_rt_list_get_ptr(ptr, i64)",
		"declare void @osty_rt_list_set_i64(ptr, i64, i64)",
		"declare void @osty_rt_list_set_i1(ptr, i64, i1)",
		"declare void @osty_rt_list_set_f64(ptr, i64, double)",
		"declare void @osty_rt_list_set_ptr(ptr, i64, ptr)",
	}
	for _, w := range want {
		assertGeneratedIRContains(t, decls, w)
	}
}

func TestGeneratedListBasicSmokeIR(t *testing.T) {
	ir := llvmSmokeListBasicIR("/tmp/list_basic.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/list_basic.osty\"")
	assertGeneratedIRContains(t, ir, "declare ptr @osty_rt_list_new()")
	assertGeneratedIRContains(t, ir, "declare i64 @osty_rt_list_len(ptr)")
	assertGeneratedIRContains(t, ir, "declare void @osty_rt_list_push_i64(ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare i64 @osty_rt_list_get_i64(ptr, i64)")
	assertGeneratedIRContains(t, ir, "%t0 = call ptr @osty_rt_list_new()")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_list_push_i64(ptr %t0, i64 10)")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_list_push_i64(ptr %t0, i64 20)")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_list_push_i64(ptr %t0, i64 30)")
	assertGeneratedIRContains(t, ir, "= call i64 @osty_rt_list_len(ptr %t0)")
	assertGeneratedIRContains(t, ir, "= call i64 @osty_rt_list_get_i64(ptr %t0, i64 1)")
	assertGeneratedIRContains(t, ir, "ret i32 0")
}

func TestGeneratedClosureEnvTypeNameJoinsTags(t *testing.T) {
	cases := []struct {
		tags []string
		want string
	}{
		{[]string{"ptr"}, "ClosureEnv.ptr"},
		{[]string{"ptr", "i64"}, "ClosureEnv.ptr.i64"},
		{[]string{"ptr", "i64", "double", "ptr"}, "ClosureEnv.ptr.i64.double.ptr"},
	}
	for _, c := range cases {
		if got := llvmClosureEnvTypeName(c.tags); got != c.want {
			t.Fatalf("llvmClosureEnvTypeName(%v) = %q, want %q", c.tags, got, c.want)
		}
	}
}

func TestGeneratedClosureEnvTypeDefRendersStruct(t *testing.T) {
	got := llvmClosureEnvTypeDef("ClosureEnv.ptr.i64", []string{"ptr", "i64"})
	want := "%ClosureEnv.ptr.i64 = type { ptr, i64 }"
	if got != want {
		t.Fatalf("llvmClosureEnvTypeDef = %q, want %q", got, want)
	}
}

func TestGeneratedClosureThunkNameIsOstyOwned(t *testing.T) {
	if got := llvmClosureThunkName("double_val"); got != "__osty_closure_thunk_double_val" {
		t.Fatalf("llvmClosureThunkName = %q", got)
	}
	if got := llvmClosureThunkName("add"); got != "__osty_closure_thunk_add" {
		t.Fatalf("llvmClosureThunkName = %q", got)
	}
}

func TestGeneratedClosureThunkDefinitionForwardsArgs(t *testing.T) {
	def := llvmClosureThunkDefinition("double_val", "i64", []string{"i64"})

	assertGeneratedIRContains(t, def, "define private i64 @__osty_closure_thunk_double_val(ptr %env, i64 %arg0)")
	assertGeneratedIRContains(t, def, "%ret = call i64 @double_val(i64 %arg0)")
	assertGeneratedIRContains(t, def, "ret i64 %ret")
}

func TestGeneratedClosureThunkDefinitionVoidReturn(t *testing.T) {
	def := llvmClosureThunkDefinition("noop", "void", []string{})

	assertGeneratedIRContains(t, def, "define private void @__osty_closure_thunk_noop(ptr %env)")
	assertGeneratedIRContains(t, def, "call void @noop()")
	assertGeneratedIRContains(t, def, "ret void")
	if strings.Contains(def, "%ret = call") {
		t.Fatalf("void thunk should not bind a return register\nIR:\n%s", def)
	}
}

func TestGeneratedClosureThunkDefinitionEmptyReturnNormalizesToVoid(t *testing.T) {
	def := llvmClosureThunkDefinition("sink", "", []string{"ptr"})

	assertGeneratedIRContains(t, def, "define private void @__osty_closure_thunk_sink(ptr %env, ptr %arg0)")
	assertGeneratedIRContains(t, def, "call void @sink(ptr %arg0)")
	assertGeneratedIRContains(t, def, "ret void")
}

func TestGeneratedFnValueCallIndirectLoadsFlatEnvSlot(t *testing.T) {
	emitter := llvmEmitter()
	out := llvmFnValueCallIndirect(
		emitter,
		"i64",
		&LlvmValue{typ: "ptr", name: "%env", pointer: false},
		[]*LlvmValue{{typ: "i64", name: "42", pointer: false}},
	)
	ir := strings.Join(emitter.body, "\n")

	if got, want := out.typ, "i64"; got != want {
		t.Fatalf("out.typ = %q, want %q", got, want)
	}
	if out.name == "" {
		t.Fatalf("out.name empty, want temp register")
	}
	assertGeneratedIRContains(t, ir, "= load ptr, ptr %env")
	assertGeneratedIRContains(t, ir, "= call i64 (ptr, i64) ")
	assertGeneratedIRContains(t, ir, "(ptr %env, i64 42)")
}

func TestGeneratedFnValueCallIndirectVoidReturnOmitsResultBinding(t *testing.T) {
	emitter := llvmEmitter()
	out := llvmFnValueCallIndirect(
		emitter,
		"",
		&LlvmValue{typ: "ptr", name: "%env", pointer: false},
		[]*LlvmValue{{typ: "ptr", name: "%arg0", pointer: false}},
	)
	ir := strings.Join(emitter.body, "\n")

	if got, want := out.typ, "void"; got != want {
		t.Fatalf("out.typ = %q, want %q", got, want)
	}
	if got := out.name; got != "" {
		t.Fatalf("out.name = %q, want empty", got)
	}
	assertGeneratedIRContains(t, ir, "= load ptr, ptr %env")
	assertGeneratedIRContains(t, ir, "call void (ptr, ptr) ")
	assertGeneratedIRContains(t, ir, "(ptr %env, ptr %arg0)")
	if strings.Contains(ir, "= call void") {
		t.Fatalf("void indirect call should not bind a result\nIR:\n%s", ir)
	}
}

func TestGeneratedClosureBareFnEnvTypeDefIsOneSlot(t *testing.T) {
	got := llvmClosureBareFnEnvTypeDef()
	want := "%ClosureEnv.ptr = type { ptr }"
	if got != want {
		t.Fatalf("llvmClosureBareFnEnvTypeDef = %q, want %q", got, want)
	}
}

func TestGeneratedClosureThunkSmokeIR(t *testing.T) {
	ir := llvmSmokeClosureThunkIR("/tmp/closure_thunk.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/closure_thunk.osty\"")
	assertGeneratedIRContains(t, ir, "%ClosureEnv.ptr = type { ptr }")
	assertGeneratedIRContains(t, ir, "define i64 @double_val(i64 %x)")
	assertGeneratedIRContains(t, ir, "define private i64 @__osty_closure_thunk_double_val(ptr %env, i64 %arg0)")
	assertGeneratedIRContains(t, ir, "%ret = call i64 @double_val(i64 %arg0)")
	assertGeneratedIRContains(t, ir, "define i32 @main()")
	assertGeneratedIRContains(t, ir, "%t0 = alloca %ClosureEnv.ptr")
	assertGeneratedIRContains(t, ir, "store ptr @__osty_closure_thunk_double_val, ptr %t1")
	assertGeneratedIRContains(t, ir, "= call i64 (ptr, i64) ")
	assertGeneratedIRContains(t, ir, "(ptr %t0, i64 21)")
	assertGeneratedIRContains(t, ir, "call i32 (ptr, ...) @printf(ptr @.fmt_i64, i64 ")
	assertGeneratedIRContains(t, ir, "ret i32 0")
}

func TestGeneratedClosureBasicSmokeIR(t *testing.T) {
	ir := llvmSmokeClosureBasicIR("/tmp/closure_basic.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/closure_basic.osty\"")
	assertGeneratedIRContains(t, ir, "%ClosureEnv.ptr.i64 = type { ptr, i64 }")
	assertGeneratedIRContains(t, ir, "define i64 @closure_body(ptr %env)")
	assertGeneratedIRContains(t, ir, "getelementptr %ClosureEnv.ptr.i64, ptr %env, i32 0, i32 1")
	assertGeneratedIRContains(t, ir, "= load i64, ptr ")
	assertGeneratedIRContains(t, ir, "define i32 @main()")
	assertGeneratedIRContains(t, ir, "%t0 = alloca %ClosureEnv.ptr.i64")
	assertGeneratedIRContains(t, ir, "%t1 = getelementptr %ClosureEnv.ptr.i64, ptr %t0, i32 0, i32 0")
	assertGeneratedIRContains(t, ir, "store ptr @closure_body, ptr %t1")
	assertGeneratedIRContains(t, ir, "%t2 = getelementptr %ClosureEnv.ptr.i64, ptr %t0, i32 0, i32 1")
	assertGeneratedIRContains(t, ir, "store i64 42, ptr %t2")
	assertGeneratedIRContains(t, ir, "= load ptr, ptr ")
	assertGeneratedIRContains(t, ir, "= call i64 (ptr) ")
	assertGeneratedIRContains(t, ir, "(ptr %t0)")
	assertGeneratedIRContains(t, ir, "call i32 (ptr, ...) @printf(ptr @.fmt_i64, i64 ")
	assertGeneratedIRContains(t, ir, "ret i32 0")
}

func TestGeneratedSetRuntimeSymbolsAreOstyOwned(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"new", llvmSetRuntimeNewSymbol(), "osty_rt_set_new"},
		{"len", llvmSetRuntimeLenSymbol(), "osty_rt_set_len"},
		{"to_list", llvmSetRuntimeToListSymbol(), "osty_rt_set_to_list"},
		{"contains_i64", llvmSetRuntimeContainsSymbol("i64", false), "osty_rt_set_contains_i64"},
		{"insert_f64", llvmSetRuntimeInsertSymbol("double", false), "osty_rt_set_insert_f64"},
		{"remove_string", llvmSetRuntimeRemoveSymbol("ptr", true), "osty_rt_set_remove_string"},
		{"insert_bytes", llvmSetRuntimeInsertSymbol("", false), "osty_rt_set_insert_bytes"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestGeneratedSetRuntimeDeclarationsAreOstyOwned(t *testing.T) {
	decls := strings.Join(llvmSetRuntimeDeclarations(), "\n")

	want := []string{
		"declare ptr @osty_rt_set_new(i64)",
		"declare i64 @osty_rt_set_len(ptr)",
		"declare ptr @osty_rt_set_to_list(ptr)",
		"declare i1 @osty_rt_set_contains_i64(ptr, i64)",
		"declare i1 @osty_rt_set_contains_string(ptr, ptr)",
		"declare i1 @osty_rt_set_insert_i64(ptr, i64)",
		"declare i1 @osty_rt_set_insert_f64(ptr, double)",
		"declare i1 @osty_rt_set_insert_string(ptr, ptr)",
		"declare i1 @osty_rt_set_remove_i64(ptr, i64)",
		"declare i1 @osty_rt_set_remove_string(ptr, ptr)",
	}
	for _, w := range want {
		assertGeneratedIRContains(t, decls, w)
	}
}

func TestGeneratedSetBasicSmokeIR(t *testing.T) {
	ir := llvmSmokeSetBasicIR("/tmp/set_basic.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/set_basic.osty\"")
	assertGeneratedIRContains(t, ir, "declare ptr @osty_rt_set_new(i64)")
	assertGeneratedIRContains(t, ir, "declare i1 @osty_rt_set_insert_i64(ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare i1 @osty_rt_set_contains_i64(ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare i1 @osty_rt_set_remove_i64(ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare ptr @osty_rt_set_to_list(ptr)")
	assertGeneratedIRContains(t, ir, "%t0 = call ptr @osty_rt_set_new(i64 1)")
	assertGeneratedIRContains(t, ir, "%t1 = call i1 @osty_rt_set_insert_i64(ptr %t0, i64 10)")
	assertGeneratedIRContains(t, ir, "= call i1 @osty_rt_set_contains_i64(ptr %t0, i64 20)")
	assertGeneratedIRContains(t, ir, "= call i1 @osty_rt_set_remove_i64(ptr %t0, i64 20)")
	assertGeneratedIRContains(t, ir, "= call i64 @osty_rt_set_len(ptr %t0)")
	assertGeneratedIRContains(t, ir, "= call ptr @osty_rt_set_to_list(ptr %t0)")
	assertGeneratedIRContains(t, ir, "ret i32 0")
}

func TestGeneratedChanRuntimeSymbolsAreOstyOwned(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"make", llvmChanRuntimeMakeSymbol(), "osty_rt_thread_chan_make"},
		{"close", llvmChanRuntimeCloseSymbol(), "osty_rt_thread_chan_close"},
		{"is_closed", llvmChanRuntimeIsClosedSymbol(), "osty_rt_thread_chan_is_closed"},
		{"send_bytes", llvmChanRuntimeSendBytesSymbol(), "osty_rt_thread_chan_send_bytes_v1"},
		{"send_i64", llvmChanRuntimeSendSymbol("i64"), "osty_rt_thread_chan_send_i64"},
		{"send_f64", llvmChanRuntimeSendSymbol("f64"), "osty_rt_thread_chan_send_f64"},
		{"recv_ptr", llvmChanRuntimeRecvSymbol("ptr"), "osty_rt_thread_chan_recv_ptr"},
		{"recv_bytes", llvmChanRuntimeRecvSymbol("bytes_v1"), "osty_rt_thread_chan_recv_bytes_v1"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestGeneratedChanElementSuffixMatchesPolicy(t *testing.T) {
	cases := []struct {
		elem string
		want string
	}{
		{"i64", "i64"},
		{"i1", "i1"},
		{"double", "f64"},
		{"ptr", "ptr"},
		{"%MyStruct", "bytes_v1"},
		{"", "bytes_v1"},
	}
	for _, c := range cases {
		if got := llvmChanElementSuffix(c.elem); got != c.want {
			t.Fatalf("llvmChanElementSuffix(%q) = %q, want %q", c.elem, got, c.want)
		}
	}
}

func TestGeneratedChanRuntimeDeclarationsAreOstyOwned(t *testing.T) {
	decls := strings.Join(llvmChanRuntimeDeclarations(), "\n")

	want := []string{
		"declare ptr @osty_rt_thread_chan_make(i64)",
		"declare void @osty_rt_thread_chan_close(ptr)",
		"declare i1 @osty_rt_thread_chan_is_closed(ptr)",
		"declare void @osty_rt_thread_chan_send_i64(ptr, i64)",
		"declare void @osty_rt_thread_chan_send_f64(ptr, double)",
		"declare void @osty_rt_thread_chan_send_bytes_v1(ptr, ptr, i64)",
		"declare { i64, i64 } @osty_rt_thread_chan_recv_i64(ptr)",
		"declare { i64, i64 } @osty_rt_thread_chan_recv_bytes_v1(ptr)",
	}
	for _, w := range want {
		assertGeneratedIRContains(t, decls, w)
	}
}

func TestGeneratedChanSendDispatchesBySuffix(t *testing.T) {
	cases := []struct {
		valTyp string
		valReg string
		want   string
	}{
		{"i64", "42", "call void @osty_rt_thread_chan_send_i64(ptr %ch, i64 42)"},
		{"double", "3.14", "call void @osty_rt_thread_chan_send_f64(ptr %ch, double 3.14)"},
		{"ptr", "@.str0", "call void @osty_rt_thread_chan_send_ptr(ptr %ch, ptr @.str0)"},
	}
	for _, c := range cases {
		em := llvmEmitter()
		llvmChanSend(em,
			&LlvmValue{typ: "ptr", name: "%ch"},
			&LlvmValue{typ: c.valTyp, name: c.valReg})
		got := strings.Join(em.body, "\n")
		assertGeneratedIRContains(t, got, c.want)
	}
}

func TestGeneratedChanRecvReturnsAggregate(t *testing.T) {
	em := llvmEmitter()
	result := llvmChanRecv(em, &LlvmValue{typ: "ptr", name: "%ch"}, "i64")
	if result.typ != "{ i64, i64 }" {
		t.Fatalf("recv result typ = %q, want %q", result.typ, "{ i64, i64 }")
	}
	assertGeneratedIRContains(t, strings.Join(em.body, "\n"),
		"= call { i64, i64 } @osty_rt_thread_chan_recv_i64(ptr %ch)")
}

func TestGeneratedStringRuntimeSymbolsAreOstyOwned(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"equal", llvmStringRuntimeEqualSymbol(), "osty_rt_strings_Equal"},
		{"hasPrefix", llvmStringRuntimeHasPrefixSymbol(), "osty_rt_strings_HasPrefix"},
		{"hasSuffix", llvmStringRuntimeHasSuffixSymbol(), "osty_rt_strings_HasSuffix"},
		{"split", llvmStringRuntimeSplitSymbol(), "osty_rt_strings_Split"},
		{"concat", llvmStringRuntimeConcatSymbol(), "osty_rt_strings_Concat"},
		{"int_to_string", llvmIntRuntimeToStringSymbol(), "osty_rt_int_to_string"},
		{"float_to_string", llvmFloatRuntimeToStringSymbol(), "osty_rt_float_to_string"},
		{"bool_to_string", llvmBoolRuntimeToStringSymbol(), "osty_rt_bool_to_string"},
		{"byteLen", llvmStringRuntimeByteLenSymbol(), "osty_rt_strings_ByteLen"},
		{"count", llvmStringRuntimeCountSymbol(), "osty_rt_strings_Count"},
		{"indexOf", llvmStringRuntimeIndexOfSymbol(), "osty_rt_strings_IndexOf"},
		{"repeat", llvmStringRuntimeRepeatSymbol(), "osty_rt_strings_Repeat"},
		{"replace", llvmStringRuntimeReplaceSymbol(), "osty_rt_strings_Replace"},
		{"replaceAll", llvmStringRuntimeReplaceAllSymbol(), "osty_rt_strings_ReplaceAll"},
		{"slice", llvmStringRuntimeSliceSymbol(), "osty_rt_strings_Slice"},
		{"trimStart", llvmStringRuntimeTrimStartSymbol(), "osty_rt_strings_TrimStart"},
		{"trimEnd", llvmStringRuntimeTrimEndSymbol(), "osty_rt_strings_TrimEnd"},
		{"trimPrefix", llvmStringRuntimeTrimPrefixSymbol(), "osty_rt_strings_TrimPrefix"},
		{"trimSuffix", llvmStringRuntimeTrimSuffixSymbol(), "osty_rt_strings_TrimSuffix"},
		{"chars", llvmStringRuntimeCharsSymbol(), "osty_rt_strings_Chars"},
		{"bytes", llvmStringRuntimeBytesSymbol(), "osty_rt_strings_Bytes"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestGeneratedStringRuntimeDeclarationsAreOstyOwned(t *testing.T) {
	decls := strings.Join(llvmStringRuntimeDeclarations(), "\n")

	want := []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"declare i1 @osty_rt_strings_HasSuffix(ptr, ptr)",
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare ptr @osty_rt_float_to_string(double)",
		"declare ptr @osty_rt_bool_to_string(i1)",
		"declare i64 @osty_rt_strings_ByteLen(ptr)",
		"declare i64 @osty_rt_strings_Count(ptr, ptr)",
		"declare i64 @osty_rt_strings_IndexOf(ptr, ptr)",
		"declare ptr @osty_rt_strings_Repeat(ptr, i64)",
		"declare ptr @osty_rt_strings_Replace(ptr, ptr, ptr)",
		"declare ptr @osty_rt_strings_ReplaceAll(ptr, ptr, ptr)",
		"declare ptr @osty_rt_strings_Slice(ptr, i64, i64)",
		"declare ptr @osty_rt_strings_TrimStart(ptr)",
		"declare ptr @osty_rt_strings_TrimEnd(ptr)",
		"declare ptr @osty_rt_strings_TrimPrefix(ptr, ptr)",
		"declare ptr @osty_rt_strings_TrimSuffix(ptr, ptr)",
		"declare ptr @osty_rt_strings_Chars(ptr)",
		"declare ptr @osty_rt_strings_Bytes(ptr)",
	}
	for _, w := range want {
		assertGeneratedIRContains(t, decls, w)
	}
}

func TestGeneratedStringConcatSmokeIR(t *testing.T) {
	ir := llvmSmokeStringConcatIR("/tmp/string_concat.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/string_concat.osty\"")
	assertGeneratedIRContains(t, ir, "declare ptr @osty_rt_strings_Concat(ptr, ptr)")
	assertGeneratedIRContains(t, ir, "declare ptr @osty_rt_int_to_string(i64)")
	assertGeneratedIRContains(t, ir, "declare ptr @osty_rt_float_to_string(double)")
	assertGeneratedIRContains(t, ir, "declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)")
	assertGeneratedIRContains(t, ir, "declare i64 @osty_rt_strings_ByteLen(ptr)")
	assertGeneratedIRContains(t, ir, "@.str0 = private unnamed_addr constant [8 x i8] c\"hello, \\00\"")
	assertGeneratedIRContains(t, ir, "@.str1 = private unnamed_addr constant [5 x i8] c\"osty\\00\"")
	assertGeneratedIRContains(t, ir, "%t0 = call ptr @osty_rt_strings_Concat(ptr @.str0, ptr @.str1)")
	assertGeneratedIRContains(t, ir, "%t1 = call i1 @osty_rt_strings_HasPrefix(ptr %t0, ptr @.str0)")
	assertGeneratedIRContains(t, ir, "%t2 = call i64 @osty_rt_strings_ByteLen(ptr %t0)")
	assertGeneratedIRContains(t, ir, "call i32 (ptr, ...) @printf(ptr @.fmt_i64, i64 %t2)")
	assertGeneratedIRContains(t, ir, "call i32 (ptr, ...) @printf(ptr @.fmt_str, ptr %t0)")
	assertGeneratedIRContains(t, ir, "ret i32 0")
}

func TestGenerateInterpolatedIntAndFloatUseRuntimeToString(t *testing.T) {
	file := parseLLVMGenFile(t, `fn render(n: Int, f: Float) -> String {
    "{n}:{f}"
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/interp_numeric.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	assertGeneratedIRContains(t, got, "declare ptr @osty_rt_int_to_string(i64)")
	assertGeneratedIRContains(t, got, "declare ptr @osty_rt_float_to_string(double)")
	assertGeneratedIRContains(t, got, "call ptr @osty_rt_int_to_string(i64")
	assertGeneratedIRContains(t, got, "call ptr @osty_rt_float_to_string(double")
	if strings.Count(got, "call ptr @osty_rt_strings_Concat") != 2 {
		t.Fatalf("expected exactly 2 string concat calls, got IR:\n%s", got)
	}
}

func TestGeneratedMapRuntimeSymbolsAreOstyOwned(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"new", llvmMapRuntimeNewSymbol(), "osty_rt_map_new"},
		{"len", llvmMapRuntimeLenSymbol(), "osty_rt_map_len"},
		{"keys", llvmMapRuntimeKeysSymbol(), "osty_rt_map_keys"},
		{"contains_i64", llvmMapRuntimeContainsSymbol("i64", false), "osty_rt_map_contains_i64"},
		{"insert_string", llvmMapRuntimeInsertSymbol("ptr", true), "osty_rt_map_insert_string"},
		{"remove_f64", llvmMapRuntimeRemoveSymbol("double", false), "osty_rt_map_remove_f64"},
		{"get_or_abort_ptr", llvmMapRuntimeGetOrAbortSymbol("ptr", false), "osty_rt_map_get_or_abort_ptr"},
		{"insert_bytes", llvmMapRuntimeInsertSymbol("", false), "osty_rt_map_insert_bytes"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestGeneratedMapKeySuffixMatchesRuntimeAbi(t *testing.T) {
	cases := []struct {
		typ      string
		isString bool
		want     string
	}{
		{"i64", false, "i64"},
		{"i1", false, "i1"},
		{"double", false, "f64"},
		{"ptr", false, "ptr"},
		{"ptr", true, "string"},
		{"i64", true, "string"},
		{"", false, "bytes"},
		{"%MyStruct", false, "bytes"},
	}
	for _, c := range cases {
		if got := llvmMapKeySuffix(c.typ, c.isString); got != c.want {
			t.Fatalf("llvmMapKeySuffix(%q, %v) = %q, want %q", c.typ, c.isString, got, c.want)
		}
	}
}

func TestGeneratedMapRuntimeDeclarationsAreOstyOwned(t *testing.T) {
	decls := strings.Join(llvmMapRuntimeDeclarations(), "\n")

	want := []string{
		"declare ptr @osty_rt_map_new()",
		"declare i64 @osty_rt_map_len(ptr)",
		"declare ptr @osty_rt_map_keys(ptr)",
		"declare i1 @osty_rt_map_contains_i64(ptr, i64)",
		"declare i1 @osty_rt_map_contains_string(ptr, ptr)",
		"declare void @osty_rt_map_insert_i64(ptr, i64, ptr)",
		"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)",
		"declare i1 @osty_rt_map_remove_i64(ptr, i64)",
		"declare void @osty_rt_map_get_or_abort_i64(ptr, i64, ptr)",
		"declare void @osty_rt_map_get_or_abort_string(ptr, ptr, ptr)",
	}
	for _, w := range want {
		assertGeneratedIRContains(t, decls, w)
	}
}

func TestGeneratedMapBasicSmokeIR(t *testing.T) {
	ir := llvmSmokeMapBasicIR("/tmp/map_basic.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/map_basic.osty\"")
	assertGeneratedIRContains(t, ir, "declare ptr @osty_rt_map_new()")
	assertGeneratedIRContains(t, ir, "declare void @osty_rt_map_insert_i64(ptr, i64, ptr)")
	assertGeneratedIRContains(t, ir, "declare i1 @osty_rt_map_contains_i64(ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare void @osty_rt_map_get_or_abort_i64(ptr, i64, ptr)")
	assertGeneratedIRContains(t, ir, "%t0 = call ptr @osty_rt_map_new()")
	assertGeneratedIRContains(t, ir, "%t1 = alloca i64")
	assertGeneratedIRContains(t, ir, "store i64 42, ptr %t1")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_map_insert_i64(ptr %t0, i64 7, ptr %t1)")
	assertGeneratedIRContains(t, ir, "= call i1 @osty_rt_map_contains_i64(ptr %t0, i64 7)")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_map_get_or_abort_i64(ptr %t0, i64 7, ptr")
	assertGeneratedIRContains(t, ir, "= call i64 @osty_rt_map_len(ptr %t0)")
	assertGeneratedIRContains(t, ir, "ret i32 0")
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
