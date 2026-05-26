package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/nativellvmgen"
	"github.com/osty/osty/internal/profile"
	ostyquery "github.com/osty/osty/internal/query/osty"
	"github.com/osty/osty/internal/resolve"
)

// buildCrossPkgDepObjects compiles every cross-package dependency the
// main entry reaches through the workspace into a standalone library
// `.o` artifact and returns the absolute paths so the link step can
// resolve `declare`d symbols. Returns nil when there are no deps to
// compile.
//
// Each dep is compiled with `nativellvmgen.TryPackageLibrary` which
// tells the subprocess to skip emitting `main` — without that, the
// dep's own `_main` would collide with the consumer's `_main` at link
// time. Dep IR + object artifacts land under
// `<dep_dir>/.osty/out/<profile>/llvm-lib/` so they're discoverable
// for caching purposes in a future iteration.
//
// **Opt-in via `OSTY_CROSS_PKG_LINK=1`**. The dep-compile path stays
// gated for general builds because compiling a large dependency as
// one library object remains expensive. When enabled, the path first
// extracts the exact symbols referenced by the consumer MIR and asks
// the native llvmgen subprocess to emit only those functions plus
// their transitive local callees.
//
// Errors compiling any single dep are surfaced as warnings to stderr
// but don't abort the build — the consumer link will simply fail
// with the original undefined-symbol error if a needed dep didn't
// produce an object, which is no worse than the pre-PR-G1 status quo.
func buildCrossPkgDepObjects(ctx context.Context, _ string, m *manifest.Manifest, eng *ostyquery.Engine, lower ostyquery.LowerKey, resolved *profile.Resolved, _ map[string]bool, layout backend.Layout, entryPath string) []string {
	if !crossPkgLinkEnabled(m) {
		return nil
	}
	if eng == nil || !backend.UseNativeOwnedLLVMIR(resolvedFeatures(resolved), backend.EmitBinary) {
		return nil
	}
	if lower.WorkspaceRoot == "" {
		return nil
	}
	rw := eng.Queries.ResolveWorkspace.Get(eng.DB, lower.WorkspaceRoot)
	if rw == nil {
		return nil
	}
	mainDir := ostyquery.NormalizePath(lower.Dir)
	mainMIR := lowerMainMIRForCrossPkgDeps(eng, lower, entryPath)
	if mainMIR == nil {
		return nil
	}
	var objects []string
	for _, pkg := range rw.Packages() {
		if pkg == nil {
			continue
		}
		depDir := ostyquery.NormalizePath(pkg.Dir)
		if depDir == "" || depDir == mainDir {
			continue
		}
		required := requiredCrossPkgSymbolsForDep(mainMIR, pkg, rw.DotPathByDir(depDir))
		if len(required) == 0 {
			continue
		}
		objPath, err := compileDepLibraryObject(ctx, pkg, layout, required)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty build: warning: dep %q library compile failed: %v\n", pkg.Name, err)
			continue
		}
		if objPath != "" {
			objects = append(objects, objPath)
		}
	}
	return objects
}

// crossPkgLinkEnabled reads `OSTY_CROSS_PKG_LINK` and returns true
// when the value is one of the canonical truthy spellings. Default
// false so production builds keep the pre-PR-G2 behavior unless the
// caller explicitly opts into the experimental workspace-link path.
func crossPkgLinkEnabled(m *manifest.Manifest) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OSTY_CROSS_PKG_LINK"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// compileDepLibraryObject runs `nativellvmgen.TryPackageLibrary` for
// `pkg`, materializes the IR + object under the dep's own
// `<dep>/.osty/out/<profile>/llvm-lib/` directory via
// `backend.EmitPrebuiltLLVMIR(EmitObject)`, and returns the object
// path on success.
func compileDepLibraryObject(ctx context.Context, pkg *resolve.Package, layout backend.Layout, required []string) (string, error) {
	entryPath := depEntryPath(pkg)
	if entryPath == "" {
		return "", fmt.Errorf("no source files")
	}
	ir, covered, warnings, err := nativellvmgen.TryPackageLibraryForSymbols(".", entryPath, pkg, required)
	if err != nil {
		return "", fmt.Errorf("native llvmgen: %w", err)
	}
	if !covered || len(ir) == 0 {
		return "", fmt.Errorf("native llvmgen returned no IR (subprocess declined coverage): %s", renderCrossPkgWarnings(warnings))
	}
	depLayout := backend.Layout{
		Root:    pkg.Dir,
		Profile: layout.Profile,
		Target:  layout.Target,
	}
	req := backend.Request{
		Layout:     depLayout,
		Emit:       backend.EmitObject,
		BinaryName: "lib",
	}
	res, err := backend.EmitPrebuiltLLVMIR(ctx, req, ir, nil)
	if err != nil {
		return "", fmt.Errorf("compile object: %w", err)
	}
	if res == nil || res.Artifacts.Object == "" {
		return "", fmt.Errorf("EmitPrebuiltLLVMIR returned no object artifact")
	}
	return res.Artifacts.Object, nil
}

func lowerMainMIRForCrossPkgDeps(eng *ostyquery.Engine, lower ostyquery.LowerKey, entryPath string) *mir.Module {
	if eng == nil {
		return nil
	}
	mainLower := lower
	mainLower.PackageName = "main"
	mainLower.SourcePath = entryPath
	mainLower.EntryPath = entryPath
	lowered := eng.Queries.LowerMIRPackage.Get(eng.DB, mainLower)
	if lowered.Err != nil {
		fmt.Fprintf(os.Stderr, "osty build: warning: cross-package dep scan failed: %v\n", lowered.Err)
		return nil
	}
	return lowered.Entry.MIR
}

func requiredCrossPkgSymbolsForDep(module *mir.Module, pkg *resolve.Package, dotPath string) []string {
	if module == nil || pkg == nil {
		return nil
	}
	prefixes := crossPkgSymbolPrefixes(pkg, dotPath)
	if len(prefixes) == 0 {
		return nil
	}
	seen := map[string]bool{}
	collectMIRModuleSymbols(module, func(sym string) {
		for _, prefix := range prefixes {
			if strings.HasPrefix(sym, prefix) && len(sym) > len(prefix) {
				seen[sym] = true
				return
			}
		}
	})
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for sym := range seen {
		out = append(out, sym)
	}
	sort.Strings(out)
	return out
}

func crossPkgSymbolPrefixes(pkg *resolve.Package, dotPath string) []string {
	seen := map[string]bool{}
	var prefixes []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		prefixes = append(prefixes, name+".")
	}
	add(dotPath)
	if pkg != nil {
		add(pkg.Name)
	}
	return prefixes
}

func collectMIRModuleSymbols(module *mir.Module, visit func(string)) {
	if module == nil || visit == nil {
		return
	}
	for _, fn := range module.Functions {
		collectMIRFunctionSymbols(fn, visit)
	}
	for _, global := range module.Globals {
		if global != nil {
			collectMIRFunctionSymbols(global.Init, visit)
		}
	}
}

func collectMIRFunctionSymbols(fn *mir.Function, visit func(string)) {
	if fn == nil || visit == nil {
		return
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			collectMIRInstrSymbols(instr, visit)
		}
		collectMIRTermSymbols(bb.Term, visit)
	}
}

func collectMIRInstrSymbols(instr mir.Instr, visit func(string)) {
	switch x := instr.(type) {
	case *mir.AssignInstr:
		if x != nil {
			collectMIRRValueSymbols(x.Src, visit)
			collectMIRPlaceSymbols(x.Dest, visit)
		}
	case *mir.CallInstr:
		if x != nil {
			collectMIRCalleeSymbols(x.Callee, visit)
			for _, arg := range x.Args {
				collectMIROperandSymbols(arg, visit)
			}
			if x.Dest != nil {
				collectMIRPlaceSymbols(*x.Dest, visit)
			}
		}
	case *mir.IntrinsicInstr:
		if x != nil {
			for _, arg := range x.Args {
				collectMIROperandSymbols(arg, visit)
			}
			if x.Dest != nil {
				collectMIRPlaceSymbols(*x.Dest, visit)
			}
		}
	}
}

func collectMIRCalleeSymbols(c mir.Callee, visit func(string)) {
	switch x := c.(type) {
	case *mir.FnRef:
		if x != nil && x.Symbol != "" {
			visit(x.Symbol)
		}
	case *mir.IndirectCall:
		if x != nil {
			collectMIROperandSymbols(x.Callee, visit)
		}
	}
}

func collectMIRTermSymbols(term mir.Terminator, visit func(string)) {
	switch x := term.(type) {
	case *mir.BranchTerm:
		if x != nil {
			collectMIROperandSymbols(x.Cond, visit)
		}
	case *mir.SwitchIntTerm:
		if x != nil {
			collectMIROperandSymbols(x.Scrutinee, visit)
		}
	}
}

func collectMIRRValueSymbols(rv mir.RValue, visit func(string)) {
	switch x := rv.(type) {
	case *mir.UseRV:
		if x != nil {
			collectMIROperandSymbols(x.Op, visit)
		}
	case *mir.UnaryRV:
		if x != nil {
			collectMIROperandSymbols(x.Arg, visit)
		}
	case *mir.BinaryRV:
		if x != nil {
			collectMIROperandSymbols(x.Left, visit)
			collectMIROperandSymbols(x.Right, visit)
		}
	case *mir.AggregateRV:
		if x != nil {
			for _, field := range x.Fields {
				collectMIROperandSymbols(field, visit)
			}
		}
	case *mir.DiscriminantRV:
		if x != nil {
			collectMIRPlaceSymbols(x.Place, visit)
		}
	case *mir.LenRV:
		if x != nil {
			collectMIRPlaceSymbols(x.Place, visit)
		}
	case *mir.CastRV:
		if x != nil {
			collectMIROperandSymbols(x.Arg, visit)
		}
	case *mir.AddressOfRV:
		if x != nil {
			collectMIRPlaceSymbols(x.Place, visit)
		}
	case *mir.RefRV:
		if x != nil {
			collectMIRPlaceSymbols(x.Place, visit)
		}
	}
}

func collectMIROperandSymbols(op mir.Operand, visit func(string)) {
	switch x := op.(type) {
	case *mir.CopyOp:
		if x != nil {
			collectMIRPlaceSymbols(x.Place, visit)
		}
	case *mir.MoveOp:
		if x != nil {
			collectMIRPlaceSymbols(x.Place, visit)
		}
	case *mir.ConstOp:
		if x != nil {
			if fn, ok := x.Const.(*mir.FnConst); ok && fn != nil && fn.Symbol != "" {
				visit(fn.Symbol)
			}
		}
	}
}

func collectMIRPlaceSymbols(place mir.Place, visit func(string)) {
	for _, proj := range place.Projections {
		if idx, ok := proj.(*mir.IndexProj); ok && idx != nil {
			collectMIROperandSymbols(idx.Index, visit)
		}
	}
}

func renderCrossPkgWarnings(warnings []error) string {
	if len(warnings) == 0 {
		return "no warnings"
	}
	parts := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning == nil {
			continue
		}
		if text := strings.TrimSpace(warning.Error()); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "no warnings"
	}
	return strings.Join(parts, "; ")
}

func depEntryPath(pkg *resolve.Package) string {
	if pkg == nil {
		return ""
	}
	// The subprocess loads every file in the package; the entry path
	// is only used to anchor the temp workspace, so any file works.
	// Prefer the first non-nil path for stable behavior.
	for _, pf := range pkg.Files {
		if pf != nil && pf.Path != "" {
			return pf.Path
		}
	}
	return ""
}
