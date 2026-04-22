package backend

import (
	"errors"
	"fmt"
	"os"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// stdlibBodyLoweringEnabled reports whether the `PrepareEntry` step
// should inject Osty-bodied stdlib functions into the user module. The
// feature is off by default during rollout — users opt in by setting
// `OSTY_STDLIB_BODY_LOWER=1`. Once the pipeline is stable the default
// flips and this gate is removed.
func stdlibBodyLoweringEnabled() bool {
	switch os.Getenv("OSTY_STDLIB_BODY_LOWER") {
	case "", "0", "false", "off":
		return false
	}
	return true
}

// PrepareEntry lowers a checked front-end source unit into the backend-neutral
// IR contract. Validation failures are returned as an error because they
// indicate a broken lowering contract rather than a user-visible backend gap.
//
// Stage 3 of the MIR migration additionally produces a MIR module alongside
// the HIR module. MIR lowering runs after HIR monomorphization + validation,
// so the HIR path remains the primary contract while backends that have
// migrated can consume `Entry.MIR` directly. MIR validation issues are
// recorded on the entry as non-fatal warnings — they do not short-circuit
// the backend dispatch, because MIR is opt-in at this stage.
//
// PrepareEntry handles the single-file path. For a multi-file package
// where every sibling .osty file should contribute its top-level
// declarations to the same emitted module, use PreparePackage so the
// full Decls slice reaches the backend in one shot.
func PrepareEntry(packageName, sourcePath string, file *ast.File, res *resolve.Result, chk *check.Result) (Entry, error) {
	entry := Entry{
		PackageName: packageName,
		SourcePath:  sourcePath,
		File:        file,
		Resolve:     res,
		Check:       chk,
	}
	if file == nil {
		return entry, fmt.Errorf("backend: nil source file")
	}
	mod, issues := ir.Lower(packageName, file, res, chk)
	entry.IRIssues = append(entry.IRIssues, issues...)
	return finalizeEntryModule(entry, mod)
}

// mirOptimizeEnabled reports whether `PrepareEntry` should call
// `mir.Optimize` on each freshly lowered module. Defaults to ON; set
// `OSTY_MIR_OPTIMIZE=0` (or `off`, `false`) to skip the pass — useful
// for bisecting regressions or comparing MIR dumps with and without
// optimisation.
func mirOptimizeEnabled() bool {
	switch os.Getenv("OSTY_MIR_OPTIMIZE") {
	case "0", "false", "off":
		return false
	}
	return true
}

// PreparePackage is the multi-file analogue of PrepareEntry. It lowers
// every file in a resolved package into one merged ir.Module, then runs
// the same stdlib injection / monomorphize / validate / MIR pipeline as
// the single-file path.
//
// pkg supplies the full set of files plus per-file resolve handles
// (`pf.Refs`, `pf.TypeRefs`, `pf.FileScope`); chk is the package-level
// `check.Result` returned by `check.Package`. entryFile, when non-nil,
// is the file the build orchestrator picked as "the" source for
// diagnostic purposes (typically `main.osty` for binaries) and is what
// the resulting Entry.File / Entry.Resolve point at — the IR itself
// already carries every file's contributions, so backends do not need
// the AST of the non-entry files.
//
// When entryFile is nil the first file in pkg.Files acts as the
// diagnostic anchor.
func PreparePackage(packageName, sourcePath string, pkg *resolve.Package, entryFile *resolve.PackageFile, chk *check.Result) (Entry, error) {
	if pkg == nil {
		return Entry{}, fmt.Errorf("backend: nil package")
	}
	if entryFile == nil {
		for _, pf := range pkg.Files {
			if pf != nil && pf.File != nil {
				entryFile = pf
				break
			}
		}
	}
	entry := Entry{
		PackageName: packageName,
		SourcePath:  sourcePath,
		Check:       chk,
	}
	if entryFile != nil {
		entry.File = entryFile.File
		entry.Source = entryFile.Source
		entry.Resolve = &resolve.Result{
			FileScope: entryFile.FileScope,
		}
	}
	mod, issues := ir.LowerPackage(packageName, pkg, chk)
	entry.IRIssues = append(entry.IRIssues, issues...)
	if mod == nil {
		return entry, fmt.Errorf("backend: ir.LowerPackage returned nil module")
	}
	return finalizeEntryModule(entry, mod)
}

// finalizeEntryModule runs the post-lowering pipeline (stdlib injection
// gate, monomorphize, validate, MIR lowering + optional optimize +
// validate) shared by PrepareEntry and PreparePackage. Splitting this
// out keeps the two entry points in lock-step: any new pass added here
// applies to both single-file and multi-file builds at the same time.
func finalizeEntryModule(entry Entry, mod *ir.Module) (Entry, error) {
	if stdlibBodyLoweringEnabled() {
		reg := stdlib.LoadCached()
		injected, injectionErrs := injectReachableStdlibBodies(mod, reg)
		entry.IRIssues = append(entry.IRIssues, injectionErrs...)
		mod.Decls = append(mod.Decls, injected...)
		// Option B Phase 1: inject built-in generic type decls
		// (Map<K,V>, Option<T>, List<T>, Set<T>, Result<T,E>) so
		// ir.Monomorphize can specialize their methods per user-code
		// instantiation. Without this step the monomorphizer never
		// sees the generic templates and falls back to per-helper
		// hand-emit at the LLVM layer.
		injectedTypes, typeIssues := injectReachableStdlibTypes(mod, reg)
		entry.IRIssues = append(entry.IRIssues, typeIssues...)
		mod.Decls = append(mod.Decls, injectedTypes...)
	}
	if monoMod, monoErrs := ir.Monomorphize(mod); monoMod != nil {
		mod = monoMod
		entry.IRIssues = append(entry.IRIssues, monoErrs...)
	}
	entry.IR = mod
	if validateErrs := ir.Validate(mod); len(validateErrs) != 0 {
		entry.IRIssues = append(entry.IRIssues, validateErrs...)
		return entry, errors.Join(validateErrs...)
	}
	mirMod := mir.Lower(mod)
	if mirMod != nil {
		if mirOptimizeEnabled() {
			mir.Optimize(mirMod)
		}
		entry.MIRIssues = append(entry.MIRIssues, mirMod.Issues...)
		if mirValidateErrs := mir.Validate(mirMod); len(mirValidateErrs) != 0 {
			entry.MIRIssues = append(entry.MIRIssues, mirValidateErrs...)
		}
		entry.MIR = mirMod
	}
	return entry, nil
}
