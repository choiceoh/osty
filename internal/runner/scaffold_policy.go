// scaffold_policy.go is the Go snapshot of
// toolchain/scaffold_policy.osty. Osty is the source of truth; the
// drift test in this package enforces parity.
package runner

// ScaffoldFixtureCasesDefault and ScaffoldFixtureCasesMax govern
// the table-size policy for `osty new fixture`. Exported so flag
// defaults and the cap live in one place.
//
// Osty: toolchain/scaffold_policy.osty:13
const (
	ScaffoldFixtureCasesDefault = 3
	ScaffoldFixtureCasesMax     = 64
)

// FixtureCaseCount mirrors toolchain/scaffold_policy.osty's
// FixtureCaseCount. `OverCap` signals that the host should emit
// the "exceeds the N row cap" diagnostic.
//
// Osty: toolchain/scaffold_policy.osty:50
type FixtureCaseCount struct {
	Count   int
	OverCap bool
}

// IsValidScaffoldName reports whether `name` can be used as both a
// directory name and the `name` field of osty.toml. Rule matches
// cargo-style project names: non-empty, leading char is letter or
// `_`, subsequent chars add digits and `-`.
//
// Osty: toolchain/scaffold_policy.osty:29
func IsValidScaffoldName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !isScaffoldLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !isScaffoldLetter(r) && !isScaffoldDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// ResolveFixtureCases picks the row count for the generated
// fixture table. Zero/negative → default; over-cap → cap + overCap
// flag so the host can emit its diagnostic.
//
// Osty: toolchain/scaffold_policy.osty:55
func ResolveFixtureCases(requested int) FixtureCaseCount {
	if requested > ScaffoldFixtureCasesMax {
		return FixtureCaseCount{Count: ScaffoldFixtureCasesMax, OverCap: true}
	}
	if requested <= 0 {
		return FixtureCaseCount{Count: ScaffoldFixtureCasesDefault, OverCap: false}
	}
	return FixtureCaseCount{Count: requested, OverCap: false}
}

func isScaffoldLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isScaffoldDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
