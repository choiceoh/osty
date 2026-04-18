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
	// Inject bodied stdlib functions reached from the user module before
	// monomorphization runs, so any stdlib-owned generic body participates
	// in the same mono pass. Rewriting callsites after injection rebinds
	// `strings.foo(...)` to the mangled symbol the injected decl carries.
	// Gated behind OSTY_STDLIB_BODY_LOWER during rollout.
	if stdlibBodyLoweringEnabled() {
		injected, injectionErrs := injectReachableStdlibBodies(mod, stdlib.LoadCached())
		entry.IRIssues = append(entry.IRIssues, injectionErrs...)
		mod.Decls = append(mod.Decls, injected...)
	}
	// Monomorphize generic free functions in-place on the lowered module.
	// The backend contract is "no TypeVar leaves IR once it reaches the
	// emitter", so we run this transform before validation rather than
	// leaving it to each backend. A nil input produces a nil output; keep
	// the original module in that pathological case so the validator can
	// still report its own issues below.
	if monoMod, monoErrs := ir.Monomorphize(mod); monoMod != nil {
		mod = monoMod
		entry.IRIssues = append(entry.IRIssues, monoErrs...)
	}
	entry.IR = mod
	if validateErrs := ir.Validate(mod); len(validateErrs) != 0 {
		entry.IRIssues = append(entry.IRIssues, validateErrs...)
		return entry, errors.Join(validateErrs...)
	}
	// Produce MIR alongside the HIR module. MIR consumes the monomorphic
	// HIR; lowerer-reported issues become entry.MIRIssues and the structural
	// validator adds any post-condition failures. MIR is not yet a
	// backend-blocking contract — callers that consume it should check for
	// nil and fall back to the HIR path.
	mirMod := mir.Lower(mod)
	if mirMod != nil {
		entry.MIRIssues = append(entry.MIRIssues, mirMod.Issues...)
		if mirValidateErrs := mir.Validate(mirMod); len(mirValidateErrs) != 0 {
			entry.MIRIssues = append(entry.MIRIssues, mirValidateErrs...)
		}
		entry.MIR = mirMod
	}
	return entry, nil
}
