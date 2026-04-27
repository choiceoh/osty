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
// skip emission in that case rather than guess. Delegates to the
// Osty-sourced `mirDataLayoutFor` (`toolchain/mir_generator.osty`).
func dataLayoutFor(target string) string {
	return mirDataLayoutFor(target)
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
// triple the host toolchain uses by default. Delegates to the Osty-
// sourced `mirLLVMTripleFor` (`toolchain/mir_generator.osty`).
func llvmTripleFor(arch, osName string) string {
	return mirLLVMTripleFor(arch, osName)
}

// linuxArch maps Go's GOARCH to the LLVM arch component used on
// Linux / Windows triples. Delegates to `mirLinuxArch`.
func linuxArch(arch string) string {
	return mirLinuxArch(arch)
}

// darwinArch maps Go's GOARCH to the LLVM arch component used on
// Apple-sys triples. Delegates to `mirDarwinArch`.
func darwinArch(arch string) string {
	return mirDarwinArch(arch)
}
