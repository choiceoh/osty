// llvmgen.go — public API surface for the LLVM backend: `RenderSkeleton`,
// `NeedsObjectArtifact`, `NeedsBinaryArtifact`, `ClangCompile/LinkArgs`,
// `Unsupported*` diagnostics, `SmokeExecutableCorpus`, plus the internal
// `generateFromAST`/`generateASTFile` entry points used by ir_module.go
// and the in-package tests.
//
// All heavy lifting lives in sibling files:
//
//   - generator.go       — *generator state machine + shared helpers
//   - decl.go            — top-level declaration collection + function entry
//   - stmt.go            — statement-position emission
//   - expr.go            — value-position emission
//   - type.go            — source-type → LLVM type mapping + static inference
//   - ffi.go             — Go FFI / unknown runtime-FFI detection
//   - runtime_ffi.go     — Osty runtime C ABI surface + symbol tables
//   - ir_module.go       — IR → AST bridge (transitional; see doc.go)
//   - support_snapshot.go — generated snapshot of the Osty-authored
//     `llvm*` helpers in toolchain/llvmgen.osty
//
// Keep this file small: it is the contract with external callers.
package llvmgen

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/osty/osty/internal/ast"
)

// ErrUnsupported marks source shapes that this early LLVM emitter does
// not lower yet. Callers observing this sentinel must render the
// skeleton IR themselves — the LLVM backend dispatcher no longer falls
// back to an AST path when IR lowering hits a gap.
var ErrUnsupported = errors.New("llvmgen: unsupported source shape")

// Options configures textual LLVM IR emission.
//
// UseMIR selects the MIR-direct emitter. The LLVM backend now enables
// it by default and falls back automatically on ErrUnsupported; direct
// callers can still leave the zero value to stay on the legacy
// HIR→AST bridge or set it explicitly when running dual-emission
// tests.
//
// EmitGC asks the MIR emitter to instrument generated code with the
// Osty GC runtime contract — root-slot binding for managed locals,
// release on return, and safepoint calls at function entry + loop
// back-edges. Off by default so existing MIR-emission tests stay
// byte-stable; opt in when you want to link against the managed
// runtime.
type Options struct {
	PackageName string
	SourcePath  string
	// Source carries the UTF-8 source bytes of the primary file. The
	// emitter uses it for diagnostic-oriented lookups like rendering
	// the original expression text of `testing.assertEq` arguments in
	// failure messages. Optional — leave nil to skip text capture.
	Source []byte
	Target string
	UseMIR bool
	EmitGC bool
}

// SmokeExecutableCase describes one LLVM executable parity case. The data is
// generated from the Osty-authored backend core; Go tests only execute it.
type SmokeExecutableCase struct {
	Name    string
	Fixture string
	Stdout  string
}

// UnsupportedDiagnostic is the self-hosted LLVM backend unsupported policy
// projected into the Go bootstrap bridge.
type UnsupportedDiagnostic struct {
	Code    string
	Kind    string
	Message string
	Hint    string
}

// UnsupportedError keeps errors.Is(err, ErrUnsupported) working while carrying
// the self-hosted diagnostic policy.
type UnsupportedError struct {
	Diagnostic UnsupportedDiagnostic
}

func (e *UnsupportedError) Error() string {
	return UnsupportedSummary(e.Diagnostic)
}

func (e *UnsupportedError) Unwrap() error {
	return ErrUnsupported
}

// RenderSkeleton emits the inspectable unsupported-source LLVM placeholder.
// The implementation is generated from toolchain/llvmgen.osty so
// the skeleton shape remains owned by the Osty emitter core.
func RenderSkeleton(packageName, sourcePath, emit, target string, reason error) []byte {
	unsupported := ""
	if reason != nil {
		unsupported = reason.Error()
	}
	canonical := CanonicalLLVMTarget(target)
	ir := []byte(llvmRenderSkeleton(packageName, filepath.ToSlash(sourcePath), emit, canonical, unsupported))
	return withDataLayout(ir, canonical)
}

// NeedsObjectArtifact reports whether an LLVM emit mode should continue past
// textual IR into an object file. The decision is generated from the
// Osty-authored backend core.
func NeedsObjectArtifact(emit string) bool {
	return llvmNeedsObjectArtifact(emit)
}

// NeedsBinaryArtifact reports whether an LLVM emit mode should link a host
// binary after object emission. The decision is generated from the
// Osty-authored backend core.
func NeedsBinaryArtifact(emit string) bool {
	return llvmNeedsBinaryArtifact(emit)
}

// ClangCompileObjectArgs returns the argv shape for `.ll -> .o`, generated
// from the Osty-authored backend core. The target is canonicalized so
// Osty-profile triples (e.g. `amd64-linux`) reach clang as the full
// LLVM triple it expects (e.g. `x86_64-unknown-linux-gnu`).
func ClangCompileObjectArgs(target, irPath, objectPath string) []string {
	return llvmClangCompileObjectArgs(CanonicalLLVMTarget(target), irPath, objectPath)
}

// ClangLinkBinaryArgs returns the argv shape for `.o -> binary`, generated
// from the Osty-authored backend core. See ClangCompileObjectArgs for
// the target canonicalization contract.
func ClangLinkBinaryArgs(target string, objectPaths []string, binaryPath string) []string {
	return llvmClangLinkBinaryArgs(CanonicalLLVMTarget(target), objectPaths, binaryPath)
}

func MissingClangMessage() string {
	return llvmMissingClangMessage()
}

func MissingBinaryArtifactMessage() string {
	return llvmMissingBinaryArtifactMessage()
}

func ClangFailureMessage(action, command, output string) string {
	return llvmClangFailureMessage(action, command, output)
}

func UnsupportedBackendErrorMessage() string {
	return llvmUnsupportedBackendErrorMessage()
}

func UnsupportedDiagnosticFor(kind, detail string) UnsupportedDiagnostic {
	return fromUnsupportedDiagnostic(llvmUnsupportedDiagnostic(kind, detail))
}

func UnsupportedSummary(diag UnsupportedDiagnostic) string {
	return llvmUnsupportedSummary(toUnsupportedDiagnostic(diag))
}

func UnsupportedDiagnosticForError(err error) UnsupportedDiagnostic {
	var unsupported *UnsupportedError
	if errors.As(err, &unsupported) {
		return unsupported.Diagnostic
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	return UnsupportedDiagnosticFor("unsupported-source", detail)
}

func unsupported(kind, detail string) error {
	return &UnsupportedError{Diagnostic: UnsupportedDiagnosticFor(kind, detail)}
}

func unsupportedf(kind, format string, args ...any) error {
	return unsupported(kind, fmt.Sprintf(format, args...))
}

func unsupportedMessage(err error) string {
	if err == nil {
		return ""
	}
	var unsupported *UnsupportedError
	if errors.As(err, &unsupported) {
		return unsupported.Diagnostic.Message
	}
	return err.Error()
}

// SmokeExecutableCorpus returns the self-hosted executable parity plan for the
// LLVM smoke corpus.
func SmokeExecutableCorpus() []SmokeExecutableCase {
	cases := llvmSmokeExecutableCorpus()
	out := make([]SmokeExecutableCase, 0, len(cases))
	for _, tc := range cases {
		out = append(out, SmokeExecutableCase{
			Name:    tc.name,
			Fixture: tc.fixture,
			Stdout:  tc.stdout,
		})
	}
	return out
}

func fromUnsupportedDiagnostic(diag *LlvmUnsupportedDiagnostic) UnsupportedDiagnostic {
	if diag == nil {
		return UnsupportedDiagnostic{}
	}
	return UnsupportedDiagnostic{
		Code:    diag.code,
		Kind:    diag.kind,
		Message: diag.message,
		Hint:    diag.hint,
	}
}

func toUnsupportedDiagnostic(diag UnsupportedDiagnostic) *LlvmUnsupportedDiagnostic {
	return &LlvmUnsupportedDiagnostic{
		code:    diag.Code,
		kind:    diag.Kind,
		message: diag.Message,
		hint:    diag.Hint,
	}
}

// generateFromAST emits textual LLVM IR from a type-checked AST. It is
// used only as an internal helper: the public entry point is
// GenerateModule, which consumes the backend-neutral IR. The in-package
// test suite still exercises this function directly because the
// generator's rewrite to consume IR in place of AST is a separate
// refactor — see ir_module.go for the current IR→AST bridge.
//
// External callers must route through GenerateModule. The LLVM backend
// dispatcher (internal/backend/llvm.go) no longer falls back to an AST
// path when the IR lowering hits a gap.
func generateFromAST(file *ast.File, opts Options) ([]byte, error) {
	return generateASTFile(file, opts)
}

func generateASTFile(file *ast.File, opts Options) ([]byte, error) {
	if file == nil {
		return nil, unsupported("source-layout", "nil file")
	}
	if diag, ok := fileUnsupportedDiagnostic(file); ok {
		return nil, &UnsupportedError{Diagnostic: diag}
	}
	g := &generator{
		sourcePath:      filepath.ToSlash(llvmFirstNonEmpty(opts.SourcePath, "<unknown>")),
		source:          opts.Source,
		target:          CanonicalLLVMTarget(opts.Target),
		runtimeFFI:      map[string]map[string]*runtimeFFIFunction{},
		runtimeFFIPaths: map[string]string{},
		runtimeDecls:    map[string]runtimeDecl{},
		traceHelpers:    map[string]string{},
		tupleTypes:      map[string]tupleTypeInfo{},
		resultTypes:     map[string]builtinResultType{},
		rangeTypes:      map[string]builtinRangeType{},
	}
	if len(file.Stmts) > 0 {
		if len(file.Decls) > 0 {
			return nil, unsupported("source-layout", "mixed script statements and declarations")
		}
		env := typeEnv{}
		g.tupleTypes = collectTupleTypes(file, env)
		g.runtimeFFI = collectRuntimeFFI(file, env)
		g.runtimeFFIPaths = collectRuntimeFFIPaths(file)
		g.resultTypes = collectBuiltinResultTypes(file, env)
		g.rangeTypes = collectBuiltinRangeTypes(file, env)
		g.testingAliases = collectStdTestingAliases(file)
		g.stdTestingGenAliases = collectStdTestingGenAliases(file)
		g.stdIoAliases = collectStdIoAliases(file)
		g.stdBytesAliases = collectStdBytesAliases(file)
		g.stdStringsAliases = collectStdStringsAliases(file)
		g.stdEnvAliases = collectStdEnvAliases(file)
		g.stdRandomAliases = collectStdRandomAliases(file)
		mainIR, err := g.emitScriptMain(file.Stmts)
		if err != nil {
			return nil, err
		}
		return g.render([]string{mainIR}), nil
	}
	decls, err := collectDeclarations(file)
	if err != nil {
		return nil, err
	}
	g.functions = decls.functionsByName
	g.methods = decls.methodsByType
	g.structs = decls.structsOrdered
	g.structsByName = decls.structsByName
	g.structsByType = decls.structsByType
	g.enums = decls.enumsOrdered
	g.enumsByName = decls.enumsByName
	g.enumsByType = decls.enumsByType
	g.interfacesByName = decls.interfacesByName
	g.typeAliasesByName = decls.typeAliasesByName
	g.globals = map[string]value{}
	g.globalConsts = map[string]constValue{}
	g.tupleTypes = collectTupleTypes(file, g.typeEnv())
	g.resultTypes = collectBuiltinResultTypes(file, g.typeEnv())
	g.rangeTypes = collectBuiltinRangeTypes(file, g.typeEnv())
	g.runtimeFFI = collectRuntimeFFI(file, g.typeEnv())
	g.runtimeFFIPaths = collectRuntimeFFIPaths(file)
	g.testingAliases = collectStdTestingAliases(file)
	g.stdTestingGenAliases = collectStdTestingGenAliases(file)
	g.stdIoAliases = collectStdIoAliases(file)
	g.stdBytesAliases = collectStdBytesAliases(file)
	g.stdStringsAliases = collectStdStringsAliases(file)
	g.stdEnvAliases = collectStdEnvAliases(file)
	g.stdRandomAliases = collectStdRandomAliases(file)
	if err := g.emitGlobalLets(decls.globalsOrdered); err != nil {
		return nil, err
	}
	var defs []string
	for _, sig := range decls.functionsOrdered {
		if sig.name == "main" {
			continue
		}
		def, err := g.emitUserFunction(sig)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	mainSig := decls.functionsByName["main"]
	if mainSig == nil {
		if len(defs) == 0 {
			return nil, unsupported("source-layout", "missing main function or script statements")
		}
		return g.render(defs), nil
	}
	mainIR, err := g.emitMainFunction(mainSig)
	if err != nil {
		return nil, err
	}
	defs = append(defs, mainIR)
	return g.render(defs), nil
}
