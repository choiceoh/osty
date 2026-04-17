// Package runner is a manual Go snapshot of the pure `osty run`
// policy that lives in toolchain/runner.osty.
//
// The Osty file is the source of truth per the project's self-host
// rule ("Osty로 작성 가능한 것은 Go로 작성 금지"). This package
// exists so cmd/osty and internal callers can link the policy
// without requiring a live compile of Osty at `go build` time. When
// the self-host pipeline can fully consume toolchain/runner.osty
// this file should be regenerated rather than hand-edited.
//
// Rules for editors:
//
//   - Any behaviour change starts in toolchain/runner.osty with a
//     test in toolchain/runner_test.osty.
//   - This file is then updated to match. The line markers below
//     ("// Osty: toolchain/runner.osty:NN") flag the counterpart in
//     the Osty source.
//   - No I/O, no filepath, no os/runtime lookups live here — the
//     caller injects sep/goos so this package remains pure.
package runner

import "strings"

// Osty: toolchain/runner.osty:13
type Diag struct {
	Severity string
	Code     string
	Message  string
	Hint     string
}

// Osty: toolchain/runner.osty:21
type EnvEntry struct {
	Key   string
	Value string
}

// Osty: toolchain/runner.osty:32
type CrossCompileOutcome struct {
	Blocked bool
	Diag    *Diag
}

// BinaryBaseName picks the binary filename stem from the manifest.
// Precedence: [bin].name override, then package name, then "app".
//
// Osty: toolchain/runner.osty:44
func BinaryBaseName(binName, pkgName string) string {
	if binName != "" {
		return binName
	}
	if pkgName != "" {
		return pkgName
	}
	return "app"
}

// BuildBinaryName is the full binary-filename policy. Cross-compile
// runs get a "-<triple>" suffix; Windows binaries get ".exe", but
// only when the invoking host is Windows (the only place .exe
// matters for exec semantics). Idempotent w.r.t. ".exe".
//
// Osty: toolchain/runner.osty:58
func BuildBinaryName(binName, pkgName, targetTriple, targetOS, hostGoos string) string {
	out := BinaryBaseName(binName, pkgName)
	if targetTriple != "" {
		out = out + "-" + targetTriple
	}
	effectiveOS := targetOS
	if effectiveOS == "" {
		effectiveOS = hostGoos
	}
	producesWindowsBinary := targetTriple == "" || effectiveOS == "windows"
	if hostGoos == "windows" && producesWindowsBinary && !strings.HasSuffix(out, ".exe") {
		out = out + ".exe"
	}
	return out
}

// BinaryNameFor is the `osty run` shorthand wrapper: host build, so
// triple / targetOS collapse away.
//
// Osty: toolchain/runner.osty:95
func BinaryNameFor(binName, pkgName, goos string) string {
	return BuildBinaryName(binName, pkgName, "", "", goos)
}

// EntryPathFor resolves the entry source file from manifest input.
// sep is injected (filepath.Separator in host code) so this package
// stays free of filepath.
//
// Osty: toolchain/runner.osty:76
func EntryPathFor(root, binPath, sep string) string {
	if binPath == "" {
		return joinPath(root, "main.osty", sep)
	}
	return joinPath(root, binPath, sep)
}

// CrossCompileGuard rejects non-host target triples. `""` means
// "host default" and is always allowed. When blocked, the returned
// diagnostic steers the user at `osty build --target`.
//
// Osty: toolchain/runner.osty:87
func CrossCompileGuard(targetTriple string) CrossCompileOutcome {
	if targetTriple == "" {
		return CrossCompileOutcome{Blocked: false, Diag: nil}
	}
	return CrossCompileOutcome{
		Blocked: true,
		Diag: &Diag{
			Severity: "error",
			Code:     "R0001",
			Message:  "cannot execute cross-compiled binary for " + targetTriple + " on host",
			Hint:     "use `osty build --target " + targetTriple + "` to produce the binary",
		},
	}
}

// MergeEnv overlays per-build env overrides on top of the parent
// environment. A later entry with the same key wins; overrides
// appear first in the output so readers see them without scanning
// the inherited parent. Matches os/exec's lookup convention.
//
// Osty: toolchain/runner.osty:110
func MergeEnv(parent []string, overrides []EnvEntry) []string {
	if len(overrides) == 0 {
		return parent
	}

	out := make([]string, 0, len(parent)+len(overrides))
	seen := make(map[string]struct{}, len(overrides))
	for _, o := range overrides {
		out = append(out, o.Key+"="+o.Value)
		seen[o.Key] = struct{}{}
	}
	for _, kv := range parent {
		key := envEntryKey(kv)
		if key == "" {
			out = append(out, kv)
			continue
		}
		if _, shadowed := seen[key]; shadowed {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Osty: toolchain/runner.osty:151
func envEntryKey(kv string) string {
	if eq := strings.IndexByte(kv, '='); eq >= 0 {
		return kv[:eq]
	}
	return ""
}

func joinPath(dir, rest, sep string) string {
	if dir == "" {
		return rest
	}
	if strings.HasSuffix(dir, sep) {
		return dir + rest
	}
	return dir + sep + rest
}

// DefaultEmitMode picks the --emit artifact mode when the caller
// doesn't override it. gen/pipeline default to a text artifact; the
// concrete text format (llvm-ir vs go source) is pinned by the
// backend's own rules downstream. Everything else wants a binary.
//
// Osty: toolchain/runner.osty:207
func DefaultEmitMode(tool string) string {
	if tool == "gen" || tool == "pipeline" {
		return "llvm-ir"
	}
	return "binary"
}

// ToolEmitCompat enforces the tool-level rules that sit on top of a
// backend's native capability matrix. The backend's own
// ValidateEmit still runs afterwards for "this backend can't emit
// this format at all"; this function handles the CLI-ergonomic
// extras (gen/pipeline text-mode must match backend, run/test need
// binary). Returns nil when the combination is allowed.
//
// Osty: toolchain/runner.osty:226
func ToolEmitCompat(tool, backend, emit string) *Diag {
	if tool == "gen" || tool == "pipeline" {
		if backend == "go" && emit != "go" {
			return toolEmitMismatch(tool, backend, emit, "go", "R0010")
		}
		if backend == "llvm" && emit != "llvm-ir" {
			return toolEmitMismatch(tool, backend, emit, "llvm-ir", "R0011")
		}
	}
	if tool == "run" || tool == "test" {
		if emit != "binary" {
			return &Diag{
				Severity: "error",
				Code:     "R0012",
				Message:  tool + ` requires --emit="binary"`,
				Hint:     "",
			}
		}
	}
	return nil
}

func toolEmitMismatch(tool, backend, got, want, code string) *Diag {
	return &Diag{
		Severity: "error",
		Code:     code,
		Message:  tool + ` with backend "` + backend + `" cannot emit "` + got + `" (want "` + want + `")`,
		Hint:     "",
	}
}
