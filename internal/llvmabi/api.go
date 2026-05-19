// Package llvmabi owns the small Go host boundary the LLVM backend still
// needs after MIR emission moved out of internal/llvmgen.
package llvmabi

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrUnsupported = errors.New("llvm backend: unsupported source shape")

type Options struct {
	PackageName string
	SourcePath  string
	Source      []byte
	Target      string
	UseMIR      bool
	EmitGC      bool
}

type UnsupportedDiagnostic struct {
	Code    string
	Kind    string
	Message string
	Hint    string
}

type UnsupportedError struct {
	Diagnostic UnsupportedDiagnostic
}

func (e *UnsupportedError) Error() string { return UnsupportedSummary(e.Diagnostic) }
func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }

func UnsupportedDiagnosticFor(kind, detail string) UnsupportedDiagnostic {
	switch kind {
	case "go-ffi":
		if detail == "" {
			detail = "<unknown>"
		}
		return UnsupportedDiagnostic{Code: "LLVM001", Kind: "foreign-ffi", Message: fmt.Sprintf("Go FFI import %s is not supported by the self-hosted native backend", detail), Hint: "rewrite the binding as runtime C ABI or an osty_rt_* runtime helper"}
	case "runtime-ffi":
		if detail == "" {
			detail = "<unknown>"
		}
		return UnsupportedDiagnostic{Code: "LLVM002", Kind: "runtime-ffi", Message: fmt.Sprintf("Osty runtime FFI import %s needs native runtime lowering", detail), Hint: "add the runtime ABI shim and lowering before compiling this source natively"}
	case "source-layout":
		return unsupportedDiagnosticWith("LLVM010", kind, detail, "reshape the file around the current LLVM subset")
	case "type-system":
		return unsupportedDiagnosticWith("LLVM011", kind, detail, "use supported native backend value types")
	case "statement":
		return unsupportedDiagnosticWith("LLVM012", kind, detail, "reduce the statement to the current native backend subset")
	case "expression":
		return unsupportedDiagnosticWith("LLVM013", kind, detail, "reduce the expression to the current native backend subset")
	case "control-flow":
		return unsupportedDiagnosticWith("LLVM014", kind, detail, "use supported native backend control flow")
	case "call":
		return unsupportedDiagnosticWith("LLVM015", kind, detail, "call a function covered by the native backend")
	case "name":
		return unsupportedDiagnosticWith("LLVM016", kind, detail, "use identifiers that the LLVM backend can map directly")
	case "function-signature":
		return unsupportedDiagnosticWith("LLVM017", kind, detail, "use a function signature covered by the native backend")
	case "stdlib-body":
		return unsupportedDiagnosticWith("LLVM018", kind, detail, "call stdlib functions covered by the native backend or add a runtime shim")
	}
	if detail == "" {
		detail = "source shape is not supported by the current LLVM backend"
	}
	return UnsupportedDiagnostic{Code: "LLVM000", Kind: "unsupported-source", Message: detail, Hint: "route through the Osty-owned MIR/LIR Proto backend path"}
}

func unsupportedDiagnosticWith(code, kind, detail, hint string) UnsupportedDiagnostic {
	if detail == "" {
		detail = "source shape is not supported by the current LLVM backend"
	}
	return UnsupportedDiagnostic{Code: code, Kind: kind, Message: detail, Hint: hint}
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

func UnsupportedSummary(diag UnsupportedDiagnostic) string {
	return fmt.Sprintf("%s %s: %s; hint: %s", diag.Code, diag.Kind, diag.Message, diag.Hint)
}

func Unsupported(kind, detail string) error {
	return &UnsupportedError{Diagnostic: UnsupportedDiagnosticFor(kind, detail)}
}

func UnsupportedBackendErrorMessage() string {
	return "llvm backend: code generation is not implemented yet"
}

func RenderSkeleton(packageName, sourcePath, emit, target string, reason error) []byte {
	if packageName == "" {
		packageName = "main"
	}
	if sourcePath == "" {
		sourcePath = "<unknown>"
	}
	canonical := CanonicalLLVMTarget(target)
	unsupported := ""
	if reason != nil {
		unsupported = reason.Error()
	}
	lines := []string{
		"; Osty LLVM backend skeleton",
		fmt.Sprintf("; package: %s", packageName),
		fmt.Sprintf("; source: %s", filepath.ToSlash(sourcePath)),
		fmt.Sprintf("; emit: %s", emit),
	}
	if canonical != "" {
		lines = append(lines, fmt.Sprintf("; target: %s", canonical))
	}
	if unsupported != "" {
		lines = append(lines, fmt.Sprintf("; unsupported: %s", unsupported))
	}
	lines = append(lines, "; code generation is not implemented yet", "")
	lines = append(lines, fmt.Sprintf("source_filename = %q", filepath.ToSlash(sourcePath)))
	if canonical != "" {
		lines = append(lines, fmt.Sprintf("target triple = %q", canonical))
	}
	return withDataLayout([]byte(strings.Join(lines, "\n")+"\n"), canonical)
}

func NeedsObjectArtifact(emit string) bool { return emit == "object" || emit == "binary" }
func NeedsBinaryArtifact(emit string) bool { return emit == "binary" }

func ClangCompileObjectArgs(target, irPath, objectPath string) []string {
	return ClangCompileObjectArgsForProfile(target, "", irPath, objectPath)
}

// ClangCompileObjectArgsForProfile is the profile-aware variant of
// ClangCompileObjectArgs. Profiles whose `profile.Profile.LTO` is false
// (debug / test / profile per internal/profile/profile.go:202-241)
// compile with `-O0` and without LTO so install-self / `osty test` /
// `osty build --profile=...` finish in seconds instead of taking the
// multi-minute ThinLTO link tail. Empty profile (legacy callers
// without one in scope) and "release" keep the `-O3 -flto=thin` pipeline
// that the GC / scheduler runtime have been tuned against.
func ClangCompileObjectArgsForProfile(target, profile, irPath, objectPath string) []string {
	target = CanonicalLLVMTarget(target)
	args := make([]string, 0, 8)
	if target != "" {
		args = append(args, "-target", target)
	}
	if ProfileSkipsLTO(profile) {
		args = append(args, "-O0")
	} else {
		args = append(args, "-O3", "-flto=thin")
	}
	if strings.Contains(target, "x86_64") || strings.Contains(target, "amd64") {
		args = append(args, "-march=x86-64-v3")
	}
	return append(args, "-c", irPath, "-o", objectPath)
}

func ClangLinkBinaryArgs(target string, objectPaths []string, binaryPath string) []string {
	return ClangLinkBinaryArgsForProfile(target, "", objectPaths, binaryPath)
}

// ClangLinkBinaryArgsForProfile is the profile-aware variant of
// ClangLinkBinaryArgs. Profiles whose `profile.Profile.LTO` is false
// (debug / test / profile per internal/profile/profile.go:202-241) drop
// `-flto=thin` and the matching `-mllvm,-import-instr-limit` LLD tuning
// and fall back to `-O0`, which is what made install-self bootstrap-link
// complete in seconds instead of timing out at the ThinLTO import stage
// when the self-host IR grew past ~20 MB. Release / unspecified profiles
// keep the aggressive cross-module-inlining pipeline.
func ClangLinkBinaryArgsForProfile(target, profile string, objectPaths []string, binaryPath string) []string {
	target = CanonicalLLVMTarget(target)
	args := make([]string, 0, len(objectPaths)+10)
	if target != "" {
		args = append(args, "-target", target)
	}
	if ProfileSkipsLTO(profile) {
		args = append(args, "-O0")
		if hasLLD() {
			args = append(args, "-fuse-ld=lld")
		}
	} else {
		args = append(args, "-O3", "-flto=thin")
		// The `-mllvm` flag below is LLD-only — BFD ld parses it as `-m llvm`
		// and bails with "unrecognised emulation mode: llvm". We force LLD
		// when it's reachable and drop the import-instr-limit tuning when
		// it isn't, so fresh-clone hosts that only have BFD ld can still
		// produce a (slightly less aggressively-inlined) binary instead of
		// failing the bootstrap link outright.
		if hasLLD() {
			args = append(args, "-fuse-ld=lld")
			if !isWindowsLLVMTarget(target) {
				args = append(args, "-Wl,-mllvm,-import-instr-limit=500")
			}
		}
	}
	if isWindowsLLVMTarget(target) {
		args = append(args, "-Wl,/subsystem:console")
		args = append(args, "-Wl,/stack:67108864")
	}
	args = append(args, objectPaths...)
	if !strings.Contains(target, "windows") {
		args = append(args, "-pthread", "-lz")
	}
	return append(args, "-o", binaryPath)
}

// ProfileSkipsLTO reports whether the named build profile turns off
// LTO + cross-module inlining for clang invocations. Mirrors the
// canonical built-in table in internal/profile/profile.go's
// Defaults() — the test `TestProfileSkipsLTOMatchesProfileDefaults`
// in api_test.go locks the two in sync at compile time. Empty profile
// (legacy callers without one in scope) and unknown profile names
// preserve release-tier `-O3 -flto=thin` semantics.
func ProfileSkipsLTO(profile string) bool {
	switch profile {
	case "debug", "test", "profile":
		return true
	}
	return false
}

func isWindowsLLVMTarget(target string) bool {
	return strings.Contains(target, "-windows-") || strings.Contains(target, "-pc-windows-") || strings.Contains(target, "windows")
}

func hasLLD() bool {
	for _, name := range []string{"ld.lld", "lld"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

func MissingClangMessage() string {
	return "llvm backend: clang not found on PATH; install clang or use --emit=llvm-ir"
}
func MissingBinaryArtifactMessage() string { return "llvm backend: missing binary artifact path" }
func ClangFailureMessage(action, command, output string) string {
	return "llvm backend: clang " + action + " failed\ncommand: " + command + "\n" + output
}

func IsKnownRuntimeFFIPath(path string) bool {
	return strings.HasPrefix(path, "runtime.package.") || strings.HasPrefix(path, "runtime.cabi.") || path == "runtime.cabi" || path == "runtime.strings" || path == "runtime.path.filepath" || path == "runtime.cihost"
}

func CanonicalLLVMTarget(target string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return hostLLVMTriple()
	}
	if strings.Count(t, "-") >= 2 {
		return t
	}
	arch, osName, ok := strings.Cut(t, "-")
	if !ok || arch == "" || osName == "" {
		return t
	}
	return llvmTripleFor(arch, osName)
}

func hostLLVMTriple() string { return llvmTripleFor(runtime.GOARCH, runtime.GOOS) }

func llvmTripleFor(arch, osName string) string {
	switch osName {
	case "darwin":
		return darwinArch(arch) + "-apple-darwin"
	case "linux":
		return linuxArch(arch) + "-unknown-linux-gnu"
	case "windows":
		return linuxArch(arch) + "-pc-windows-msvc"
	case "js":
		return "wasm32-unknown-emscripten"
	}
	return linuxArch(arch) + "-unknown-" + osName
}

func linuxArch(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "386":
		return "i386"
	case "arm64":
		return "aarch64"
	}
	return arch
}

func darwinArch(arch string) string {
	if arch == "amd64" {
		return "x86_64"
	}
	return arch
}

func withDataLayout(ir []byte, target string) []byte {
	if len(ir) == 0 || target == "" {
		return ir
	}
	layout := dataLayoutFor(target)
	if layout == "" {
		return ir
	}
	triple := "target triple = \"" + target + "\""
	datalayout := "target datalayout = \"" + layout + "\""
	text := string(ir)
	if strings.Contains(text, datalayout) {
		return ir
	}
	idx := strings.Index(text, triple)
	if idx < 0 {
		return ir
	}
	end := idx + len(triple)
	return []byte(text[:end] + "\n" + datalayout + text[end:])
}

func dataLayoutFor(target string) string {
	if strings.Contains(target, "-windows-msvc") || strings.Contains(target, "-pc-windows-msvc") {
		if strings.HasPrefix(target, "aarch64") || strings.HasPrefix(target, "arm64") {
			return "e-m:w-p:64:64-i32:32-i64:64-i128:128-n32:64-S128"
		}
		return "e-m:w-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
	}
	if strings.Contains(target, "-apple-darwin") || strings.Contains(target, "-apple-macos") {
		if strings.HasPrefix(target, "aarch64") || strings.HasPrefix(target, "arm64") {
			return "e-m:o-i64:64-i128:128-n32:64-S128"
		}
		return "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
	}
	if strings.Contains(target, "-linux-gnu") || strings.Contains(target, "-linux-musl") {
		if strings.HasPrefix(target, "aarch64") {
			return "e-m:e-i8:8:32-i16:16:32-i64:64-i128:128-n32:64-S128"
		}
		if strings.HasPrefix(target, "x86_64") {
			return "e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
		}
	}
	return ""
}

const LIRProtoEnvVar = "OSTY_LLVM_LIR_PROTO"

var ErrLIRProtoNotWired = errors.New("llvm backend: LIR Proto path is not wired")

func LIRProtoSelected() bool { return lirProtoEnvOn(strings.TrimSpace(getenv(LIRProtoEnvVar))) }

var getenv = os.Getenv

func lirProtoEnvOn(value string) bool {
	switch value {
	case "", "0", "false", "FALSE", "False", "off", "OFF", "Off", "no", "NO", "No":
		return false
	}
	return true
}

type LIRProtoRequest struct {
	PackageName string
	SourcePath  string
	Source      []byte
	Target      string
}

type LIRProtoRunner interface {
	Run(req LIRProtoRequest) ([]byte, error)
}

type defaultLIRProtoRunner struct{}

func (defaultLIRProtoRunner) Run(LIRProtoRequest) ([]byte, error) { return nil, ErrLIRProtoNotWired }

var registeredLIRProtoRunner LIRProtoRunner = defaultLIRProtoRunner{}

func SetLIRProtoRunner(runner LIRProtoRunner) {
	if runner == nil {
		registeredLIRProtoRunner = defaultLIRProtoRunner{}
		return
	}
	registeredLIRProtoRunner = runner
}

func InvokeLIRProtoRunner(req LIRProtoRequest) ([]byte, error) {
	return registeredLIRProtoRunner.Run(req)
}
