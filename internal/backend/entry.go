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

// ErrMIRCoverageIncomplete means HIR successfully lowered and validated, but
// the MIR contract could not cover the module completely. Once backend entry
// reaches this point there is no legacy lowering retry; a non-empty MIR issue
// list is a compiler/backend coverage bug that must be fixed at the MIR layer.
var ErrMIRCoverageIncomplete = errors.New("backend: MIR coverage incomplete")

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
// The MIR migration now produces a MIR module as part of the backend entry
// contract. MIR lowering runs after HIR monomorphization + validation; any
// MIR lowering or validation issue is fatal because backend dispatch no longer
// has a legacy HIR retry path.
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
	entry, err := LowerEntryIR(packageName, sourcePath, file, res, chk)
	if err != nil {
		return entry, err
	}
	return LowerEntryMIR(entry)
}

// LowerEntryIR lowers a checked single-file front-end result into optimized,
// validated HIR. It intentionally stops before MIR so incremental callers can
// cache lowerIR(package) and lowerMIR(package) as separate query nodes.
func LowerEntryIR(packageName, sourcePath string, file *ast.File, res *resolve.Result, chk *check.Result) (Entry, error) {
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
	return finalizeEntryIR(entry, mod)
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
	entry, err := LowerPackageIR(packageName, sourcePath, pkg, entryFile, chk)
	if err != nil {
		return entry, err
	}
	return LowerEntryMIR(entry)
}

// LowerPackageIR is the multi-file analogue of LowerEntryIR. It lowers every
// file in a resolved package into one optimized, validated HIR module and
// leaves MIR construction to LowerEntryMIR.
func LowerPackageIR(packageName, sourcePath string, pkg *resolve.Package, entryFile *resolve.PackageFile, chk *check.Result) (Entry, error) {
	if pkg == nil {
		return Entry{}, fmt.Errorf("backend: nil package")
	}
	if entryFile == nil {
		for _, pf := range pkg.Files {
			if pf.CanMaterializeFile() {
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
		entry.File = entryFile.EnsureFile()
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
	return finalizeEntryIR(entry, mod)
}

// PrepareGraphPackage lowers one package selected from a first-class
// PackageGraph. It is the graph-native spelling of PreparePackage and lets
// build orchestrators keep the compile target explicit through backend setup.
func PrepareGraphPackage(packageName, sourcePath string, graph *resolve.PackageGraph, packagePath string, entryFile *resolve.PackageFile, chk *check.Result) (Entry, error) {
	entry, err := LowerGraphPackageIR(packageName, sourcePath, graph, packagePath, entryFile, chk)
	if err != nil {
		return entry, err
	}
	return LowerEntryMIR(entry)
}

// LowerGraphPackageIR is the PackageGraph analogue of LowerPackageIR.
func LowerGraphPackageIR(packageName, sourcePath string, graph *resolve.PackageGraph, packagePath string, entryFile *resolve.PackageFile, chk *check.Result) (Entry, error) {
	if graph == nil {
		return Entry{}, fmt.Errorf("backend: nil package graph")
	}
	pkg := graph.Package(packagePath)
	if pkg == nil {
		return Entry{}, fmt.Errorf("backend: graph package %q not found", packagePath)
	}
	return LowerPackageIR(packageName, sourcePath, pkg, entryFile, chk)
}

// finalizeEntryIR runs the post-HIR-lowering pipeline (stdlib injection gate,
// monomorphize, optimize, validate) shared by LowerEntryIR and LowerPackageIR.
// Splitting MIR into LowerEntryMIR lets the incremental query graph cache the
// HIR and MIR boundaries independently without changing PrepareEntry /
// PreparePackage behavior for existing callers.
func finalizeEntryIR(entry Entry, mod *ir.Module) (Entry, error) {
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
	mod = ir.Optimize(mod, ir.OptimizeOptions{})
	entry.IR = mod
	if validateErrs := ir.Validate(mod); len(validateErrs) != 0 {
		entry.IRIssues = append(entry.IRIssues, validateErrs...)
		return entry, errors.Join(validateErrs...)
	}
	return entry, nil
}

// LowerEntryMIR lowers an already-valid HIR backend entry into MIR, applies the
// optional MIR optimizer, and records MIR validation findings as entry warnings.
// Any MIR coverage issue is fatal because backend dispatch no longer retries a
// legacy HIR path.
func LowerEntryMIR(entry Entry) (Entry, error) {
	mirMod := mir.Lower(entry.IR)
	if mirMod == nil {
		return entry, errors.Join(ErrMIRCoverageIncomplete, fmt.Errorf("mir.Lower returned nil module"))
	}
	if mirOptimizeEnabled() {
		mir.Optimize(mirMod)
	}
	entry.MIRIssues = append(entry.MIRIssues, mirMod.Issues...)
	if mirValidateErrs := mir.Validate(mirMod); len(mirValidateErrs) != 0 {
		entry.MIRIssues = append(entry.MIRIssues, mirValidateErrs...)
	}
	entry.MIR = mirMod
	if len(entry.MIRIssues) != 0 {
		return entry, errors.Join(ErrMIRCoverageIncomplete, errors.Join(entry.MIRIssues...))
	}
	return entry, nil
}
