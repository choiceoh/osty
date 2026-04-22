// target.go — target-triple canonicalization shim.
//
// The Osty profile layer describes targets in `<arch>-<os>` form
// (e.g. `amd64-linux`, `arm64-darwin`, `wasm-js`). LLVM/clang expect
// the richer `<arch>-<vendor>-<sys>[-<abi>]` form
// (e.g. `x86_64-unknown-linux-gnu`, `arm64-apple-darwin`,
// `x86_64-pc-windows-msvc`).
//
// Without a bridge, `--target amd64-linux` would flow straight into
// `target triple = "amd64-linux"` — which every LLVM frontend rejects
// as unknown — and an empty triple would produce a module that silently
// adopts the host default, so identical inputs emit different IR
// depending on the machine that ran the compiler.
//
// Canonicalization rules implemented here:
//
//   - Empty input → host triple derived from runtime.GOOS / GOARCH.
//     Every emitted module carries an explicit triple + datalayout
//     pair so the IR is reproducible and self-describing.
//   - Osty short form (`<arch>-<os>`) → expand via the lookup tables
//     below.
//   - Anything with three or more `-`-separated segments is treated as
//     a pre-canonical LLVM triple and passed through untouched. This
//     keeps the backend open to power users who want to pin a specific
//     vendor/abi (e.g. `aarch64-unknown-linux-musl`).
//
// The companion `dataLayoutFor` returns the standard LLVM datalayout
// string for the triple — it is what pins pointer size, ABI alignment,
// and (crucially) address-space-0 as the generic address space for
// heap/stack/global references. LLVM infers this from the triple when
// omitted, but pinning it in the module makes the contract explicit
// and defends against host/target drift.
//
// This file is Go-only on purpose: `runtime.GOOS` / `runtime.GOARCH`
// are host-boundary I/O (CLAUDE.md §언어 선택 규칙). The pure mapping
// from Osty triple to LLVM triple could live in toolchain/llvmgen.osty
// once Osty gains a `std.env.hostTriple()` primitive — until then the
// shim keeps policy next to the call sites that consume it.
package llvmgen

import (
	"runtime"
	"strings"
)

// CanonicalLLVMTarget normalizes target into a form LLVM/clang will
// accept. See the package doc comment for the rules.
func CanonicalLLVMTarget(target string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return hostLLVMTriple()
	}
	if strings.Count(t, "-") >= 2 {
		return t
	}
	arch, os, ok := splitOstyTriple(t)
	if !ok {
		return t
	}
	return llvmTripleFor(arch, os)
}

// dataLayoutFor returns the LLVM datalayout string matching target.
// Returns "" when no bundled mapping covers the triple; callers should
// skip emission in that case rather than guess.
//
// Datalayouts are lifted verbatim from clang's
// `llvm::TargetMachine::createDataLayout` output for the corresponding
// triple so they round-trip cleanly through `llc`, `opt`, and `clang`.
func dataLayoutFor(target string) string {
	switch {
	case strings.Contains(target, "-windows-msvc"),
		strings.Contains(target, "-pc-windows-msvc"):
		if strings.HasPrefix(target, "aarch64") || strings.HasPrefix(target, "arm64") {
			return "e-m:w-p:64:64-i32:32-i64:64-i128:128-n32:64-S128"
		}
		return "e-m:w-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
	case strings.Contains(target, "-apple-darwin"),
		strings.Contains(target, "-apple-macos"):
		if strings.HasPrefix(target, "aarch64") || strings.HasPrefix(target, "arm64") {
			return "e-m:o-i64:64-i128:128-n32:64-S128"
		}
		return "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
	case strings.Contains(target, "-linux-gnu"),
		strings.Contains(target, "-linux-musl"):
		switch {
		case strings.HasPrefix(target, "aarch64"):
			return "e-m:e-i8:8:32-i16:16:32-i64:64-i128:128-n32:64-S128"
		case strings.HasPrefix(target, "x86_64"):
			return "e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
		}
	}
	return ""
}

// withDataLayout injects a `target datalayout = ...` directive
// immediately after the `target triple = ...` line when a mapping is
// available. The post-processing pass runs on the finished IR so the
// Osty-side renderers stay agnostic of the datalayout policy.
//
// Keeping the triple and datalayout adjacent matches clang's own
// output and keeps the module header easy to grep.
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

func splitOstyTriple(s string) (arch, osName string, ok bool) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func hostLLVMTriple() string {
	return llvmTripleFor(runtime.GOARCH, runtime.GOOS)
}

// llvmTripleFor maps a (Go-style arch, Go-style os) pair to the LLVM
// triple the host toolchain uses by default. Unknown archs pass
// through unchanged so exotic targets still produce *something*
// deterministic rather than silently falling back to host.
func llvmTripleFor(arch, osName string) string {
	switch osName {
	case "darwin":
		return darwinArch(arch) + "-apple-darwin"
	case "linux":
		return linuxArch(arch) + "-unknown-linux-gnu"
	case "windows":
		return linuxArch(arch) + "-pc-windows-msvc"
	case "js":
		// Emscripten is the only pairing the Osty toolchain ships for
		// wasm today; keep the shape explicit rather than invent a
		// generic unknown triple that clang would reject.
		return "wasm32-unknown-emscripten"
	default:
		return linuxArch(arch) + "-unknown-" + osName
	}
}

// linuxArch maps Go's GOARCH to the LLVM arch component used on
// Linux / Windows triples. The Apple ecosystem prefers `arm64` over
// `aarch64`, so darwinArch keeps that spelling separate.
func linuxArch(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "386":
		return "i386"
	case "arm64":
		return "aarch64"
	default:
		return arch
	}
}

func darwinArch(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "arm64":
		// Apple's own triples write `arm64-apple-darwin`, not aarch64.
		return "arm64"
	default:
		return arch
	}
}
