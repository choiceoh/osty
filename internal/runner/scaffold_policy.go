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

// ScaffoldDefaultWorkspaceMember is the directory name used for
// the default member of a `--workspace` scaffold when the caller
// did not supply one.
//
// Osty: toolchain/scaffold_policy.osty:74
func ScaffoldDefaultWorkspaceMember() string {
	return "core"
}

// ScaffoldRelativePaths returns the source-order list of relative
// file paths the scaffolder will write for a project of `kind`.
// Path separators are forward slashes (`/`); the host joins each
// entry with the project root and converts to the platform-native
// separator. Unknown kinds fall through to the binary layout.
//
// Osty: toolchain/scaffold_policy.osty:89
func ScaffoldRelativePaths(kind, workspaceMember string) []string {
	switch kind {
	case "lib":
		return []string{"osty.toml", "lib.osty", "lib_test.osty", ".gitignore"}
	case "workspace":
		member := workspaceMember
		if member == "" {
			member = ScaffoldDefaultWorkspaceMember()
		}
		return []string{
			"osty.toml",
			".gitignore",
			member + "/osty.toml",
			member + "/main.osty",
			member + "/main_test.osty",
			member + "/.gitignore",
		}
	case "cli":
		return []string{"osty.toml", "main.osty", "args.osty", "app.osty", "app_test.osty", ".gitignore"}
	case "service":
		return []string{"osty.toml", "main.osty", "routes.osty", "routes_test.osty", ".gitignore"}
	case "gui-qtquick":
		return []string{"osty.toml", "main.osty", "ui/main.qml", "README.md", ".gitignore"}
	case "gui-webview2":
		return []string{
			"osty.toml",
			"src/main.osty",
			"src/main_test.osty",
			"ui/index.html",
			"ui/app.css",
			"ui/app.js",
			"assets/.gitkeep",
			".gitignore",
		}
	}
	return []string{"osty.toml", "main.osty", "main_test.osty", ".gitignore"}
}

func isScaffoldLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isScaffoldDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
