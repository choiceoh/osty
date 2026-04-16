package backend

import (
	"errors"
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
)

// PrepareEntry lowers a checked front-end source unit into the backend-neutral
// IR contract. Validation failures are returned as an error because they
// indicate a broken lowering contract rather than a user-visible backend gap.
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
	return entry, nil
}
