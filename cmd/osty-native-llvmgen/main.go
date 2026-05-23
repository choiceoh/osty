package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/nativelirproto"
	"github.com/osty/osty/internal/nativellvmgen"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type llvmgenRequest = nativellvmgen.Request
type llvmgenPackageInput = nativellvmgen.PackageInput
type llvmgenPackageFile = nativellvmgen.PackageFile
type llvmgenMIRInput = nativellvmgen.MIRInput
type llvmgenResponse = nativellvmgen.Response

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	var req llvmgenRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode llvmgen request: %w", err)
	}
	// Install the managed-subprocess checker so the in-process
	// `check.PackageGraph` call inside `preparePackageEntry` resolves
	// against the same Osty-native checker the host uses. Without this
	// the default factory returns `(nil, "no native checker
	// installed")`, every check call returns an empty Result, and IR
	// lowering falls back to AST-only inference — which can leave
	// generic method calls like `xs.flatMap(|x| [x, x])` with a
	// `List<R>` return whose `R` is still a TyVar, surfacing as `?`
	// in the monomorph-mangled symbol and breaking clang.
	//
	// The host (`cmd/osty/main.go`) exports `OSTY_NATIVE_CHECKER_BIN`
	// before invoking us so the env-override branch wins; we still
	// install the managed-subprocess factory as a defensive fallback
	// when the env var is unset (e.g. an integration test invoking
	// this binary directly).
	check.UseManagedSubprocessChecker(".")
	if req.MIR != nil {
		return runMIRRequest(req, stdout)
	}
	var (
		entry backend.Entry
		err   error
	)
	if req.Package != nil {
		entry, err = preparePackageEntry(req)
	} else {
		entry, err = prepareSourceEntry(req)
	}
	if err != nil {
		return err
	}
	if req.Package != nil && req.Package.LibraryMode {
		stripMainForLibraryMode(&entry)
		if req.Package.PackageName != "" {
			qualifyExportedSymbolsForLibraryMode(&entry, req.Package.PackageName)
		}
	}
	ir, ok, warnings, err := backend.TryEmitNativeOwnedLLVMIRText(entry, "")
	if err != nil {
		return fmt.Errorf("emit native llvm-ir: %w", err)
	}
	resp := llvmgenResponse{
		Covered:  ok,
		Warnings: renderWarnings(warnings),
	}
	if ok {
		resp.LLVMIR = string(ir)
	}
	return json.NewEncoder(stdout).Encode(resp)
}

func runMIRRequest(req llvmgenRequest, stdout io.Writer) error {
	if req.MIR.Module == nil {
		return fmt.Errorf("decode MIR llvmgen request: missing module")
	}
	warnings := []string(nil)
	if ir, lirWarnings, ok := tryMIRRequestViaLIRProto(req); ok {
		return json.NewEncoder(stdout).Encode(llvmgenResponse{Covered: true, LLVMIR: string(ir), Warnings: lirWarnings})
	} else {
		warnings = append(warnings, lirWarnings...)
	}
	resp := llvmgenResponse{Covered: false, Warnings: append(warnings, "MIR payload requires the Osty-owned LIR Proto backend; Go MIR emitter fallback has been removed")}
	return json.NewEncoder(stdout).Encode(resp)
}

func tryMIRRequestViaLIRProto(req llvmgenRequest) ([]byte, []string, bool) {
	if req.MIR == nil {
		return nil, []string{"lir-proto bridge skipped: MIR payload missing"}, false
	}
	resp, err := nativelirproto.Run(req.MIR.SourcePath, nativelirproto.Request{
		PackageName: req.MIR.PackageName,
		SourcePath:  req.MIR.SourcePath,
		Source:      req.MIR.Source,
		Target:      req.MIR.Target,
		MIR:         req.MIR.Module,
	})
	if err != nil {
		return nil, []string{err.Error()}, false
	}
	if resp.Error != "" {
		return nil, []string{resp.Error}, false
	}
	if resp.Declined {
		return nil, []string{"native lirproto subprocess declined the MIR request"}, false
	}
	if resp.LLVMIR == "" {
		return nil, []string{"native lirproto subprocess produced empty IR text"}, false
	}
	return []byte(resp.LLVMIR), nil, true
}

func prepareSourceEntry(req llvmgenRequest) (backend.Entry, error) {
	// Wrap the single source as a one-file package and reuse the
	// workspace/arena pipeline — identical to preparePackageEntry
	// but without requiring req.Package to be set.
	synthetic := llvmgenRequest{
		Path: req.Path,
		Package: &llvmgenPackageInput{
			Files: []llvmgenPackageFile{{
				Path:   req.Path,
				Source: req.Source,
			}},
		},
	}
	return preparePackageEntry(synthetic)
}

func preparePackageEntry(req llvmgenRequest) (backend.Entry, error) {
	root, entryPath, err := writePackageRequest(req)
	if err != nil {
		return backend.Entry{}, err
	}
	defer os.RemoveAll(root)

	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		return backend.Entry{}, err
	}
	ws.Stdlib = stdlib.LoadCached()
	if _, err := ws.LoadPackageNative(""); err != nil {
		return backend.Entry{}, err
	}
	if req.Package != nil {
		if pkg := ws.Packages[""]; pkg != nil {
			pkg.RuntimeCapability = req.Package.RuntimeCapability
		}
	}
	graph := resolve.NewPackageGraph(ws)
	results := resolve.ResolveGraph(graph)
	checks := check.PackageGraph(graph, results, check.Opts{

		Stdlib: ws.Stdlib,
	})
	pkg := ws.Packages[""]
	if pkg == nil {
		return backend.Entry{}, fmt.Errorf("%s: no package sources were loaded", root)
	}
	var entryFile *resolve.PackageFile
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		fp, err := filepath.Abs(pf.Path)
		if err != nil {
			continue
		}
		if fp == entryPath {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		return backend.Entry{}, fmt.Errorf("%s is not part of the generated package rooted at %s", entryPath, root)
	}
	chk := checks[""]
	if chk == nil {
		chk = &check.Result{}
	}
	return backend.PrepareGraphPackage("main", entryPath, graph, "", entryFile, chk)
}

// stripMainForLibraryMode removes any top-level `main` function from
// the entry's MIR + IR before LLVM emission. Called when the cmd/osty
// build path requests a dependency package built as a library .o so
// the resulting object can link alongside a consumer's own `main`
// without `_main` symbol collision (PR-G2 cross-pkg link). All other
// pub bodies are preserved so the consumer's `declare`d symbols get
// resolved at link time.
func stripMainForLibraryMode(entry *backend.Entry) {
	if entry == nil {
		return
	}
	if entry.MIR != nil {
		filtered := entry.MIR.Functions[:0]
		for _, fn := range entry.MIR.Functions {
			if fn == nil {
				continue
			}
			if fn.Name == "main" {
				continue
			}
			filtered = append(filtered, fn)
		}
		entry.MIR.Functions = filtered
	}
}

// qualifyExportedSymbolsForLibraryMode rewrites every exported MIR
// function's `Name` to `<packageName>.<oldName>` and patches every
// `CallInstr` Callee.Symbol that targets one of the renamed functions
// so internal dep-to-dep calls keep resolving after the rename.
//
// The cross-pkg consumer side of this dance lives in
// `internal/mir/lower.go::qualifiedSymbol`, which mangles
// `use <pkg> as <alias>; alias.fn(...)` to the LLVM symbol
// `<use.RawPath>.<fn>` (use.RawPath == the dep's package name when
// loaded by cmd/osty's path-dep workspace resolver). Without this
// rename the dep's library `.o` exports `@frontInvalidTypeRepr` while
// the consumer's `main.ll` references `@toolchain.frontInvalidTypeRepr`
// — the link step fails with `undefined symbol: <pkg>.<fn>`.
//
// Non-exported (private) helpers stay bare so the package's internal
// link layout is unchanged. The rename map is keyed on the *bare* old
// name so a private helper that happens to share a name with an
// exported function in another dep does not get accidentally renamed.
//
// `IndirectCall` callees do not carry symbols (they go through an
// operand), so they are untouched. `FnConst` operands in argument
// position carry an LLVM symbol though — those are rewritten too so a
// closure-typed argument keeps pointing at the renamed callable.
//
// Scope: this measurement-doc PR pairs with the cross-pkg link wall
// captured in `docs/llvm-selfhost-plan-cross-pkg-link-measurement.md`
// Path α. Documented as the surgical companion to
// `stripMainForLibraryMode`.
func qualifyExportedSymbolsForLibraryMode(entry *backend.Entry, packageName string) {
	if entry == nil || entry.MIR == nil || packageName == "" {
		return
	}
	renames := make(map[string]string)
	for _, fn := range entry.MIR.Functions {
		if fn == nil || !fn.Exported || fn.Name == "" {
			continue
		}
		// `#[export("name")]` overrides the symbol verbatim
		// (LANG_SPEC §19.6 / mir.go:143). Skip the rename for those
		// — the user asked for an exact symbol name, so honoring it
		// avoids breaking FFI consumers that expect the verbatim
		// spelling.
		if fn.ExportSymbol != "" {
			continue
		}
		// `main` was already filtered by stripMainForLibraryMode; the
		// belt-and-braces check here keeps the rename idempotent if
		// the strip step is ever reordered.
		if fn.Name == "main" {
			continue
		}
		renames[fn.Name] = packageName + "." + fn.Name
	}
	if len(renames) == 0 {
		return
	}
	for _, fn := range entry.MIR.Functions {
		if fn == nil {
			continue
		}
		if newName, ok := renames[fn.Name]; ok {
			fn.Name = newName
		}
		for _, bb := range fn.Blocks {
			if bb == nil {
				continue
			}
			for _, instr := range bb.Instrs {
				switch ix := instr.(type) {
				case *mir.CallInstr:
					if ix == nil {
						continue
					}
					if ref, ok := ix.Callee.(*mir.FnRef); ok && ref != nil {
						if newName, found := renames[ref.Symbol]; found {
							ref.Symbol = newName
						}
					}
				}
			}
		}
	}
}

func writePackageRequest(req llvmgenRequest) (string, string, error) {
	root, err := os.MkdirTemp("", "osty-native-llvmgen-*")
	if err != nil {
		return "", "", err
	}
	files := req.Package.Files
	if len(files) == 0 {
		return "", "", fmt.Errorf("prepare llvmgen package: no files provided")
	}
	entryName := packageEntryName(req)
	if entryName == "" {
		entryName = packageFileName(files[0], 0)
	}
	entryPath := ""
	for i, file := range files {
		name := packageFileName(file, i)
		dst := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			os.RemoveAll(root)
			return "", "", err
		}
		if err := os.WriteFile(dst, []byte(file.Source), 0o644); err != nil {
			os.RemoveAll(root)
			return "", "", err
		}
		if filepath.Base(name) == entryName {
			entryPath = dst
		}
	}
	if entryPath == "" {
		entryPath = filepath.Join(root, packageFileName(files[0], 0))
	}
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		os.RemoveAll(root)
		return "", "", err
	}
	return root, absEntry, nil
}

func packageEntryName(req llvmgenRequest) string {
	if req.Path == "" {
		return ""
	}
	return filepath.Base(req.Path)
}

func packageFileName(file llvmgenPackageFile, idx int) string {
	if file.Name != "" {
		return filepath.Base(file.Name)
	}
	if file.Path != "" {
		return filepath.Base(file.Path)
	}
	return fmt.Sprintf("file%d.osty", idx)
}

func renderWarnings(warnings []error) []string {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning == nil {
			continue
		}
		out = append(out, warning.Error())
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
