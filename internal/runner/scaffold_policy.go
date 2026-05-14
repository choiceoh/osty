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

// scaffoldManifestHeader returns the one-line `# ...` banner that
// introduces an osty.toml for the given kind. Bin/lib/cli/service
// vary only on this header; GUI variants and workspace use
// dedicated templates instead.
//
// Osty: toolchain/scaffold_policy.osty (internal helper)
func scaffoldManifestHeader(kind string) string {
	switch kind {
	case "lib":
		return "# Library project; exposes a public API via `pub` declarations."
	case "cli":
		return "# CLI app project; main.osty splits parsed Args from the testable run() core."
	case "service":
		return "# HTTP service project; main.osty defines Request/Response and a handle() core."
	case "gui-qtquick":
		return "# Qt Quick GUI app project; main.osty owns state/events and ui/main.qml owns layout."
	case "gui-webview2":
		return "# WebView2 GUI app project; src/main.osty opens ui/index.html through std.gui.webview2."
	}
	return "# Binary project; `osty gen main.osty` uses the native LLVM backend and emits LLVM IR for the entry point."
}

// ScaffoldGenericManifest renders the canonical osty.toml for the
// bin/lib/cli/service kinds (and is also what RenderManifest emits
// for the GUI kinds — those swap to the richer templates at
// writeLayout time only).
//
// Osty: toolchain/scaffold_policy.osty:160
func ScaffoldGenericManifest(kind, name, edition string) string {
	return scaffoldManifestHeader(kind) + "\n\n" +
		"[package]\n" +
		"name = \"" + name + "\"\n" +
		"version = \"0.1.0\"\n" +
		"edition = \"" + edition + "\"\n\n" +
		"[dependencies]\n" +
		"# Add dependencies here, for example:\n" +
		"# json-ext = { path = \"../json-ext\" }\n"
}

// ScaffoldGUIWebView2Manifest renders the Windows WebView2 GUI
// osty.toml. Includes [gui], [gui.webview2], and the Windows link
// target table.
//
// Osty: toolchain/scaffold_policy.osty:175
func ScaffoldGUIWebView2Manifest(name, edition string) string {
	return "# WebView2 GUI app project; src/main.osty opens ui/index.html through std.gui.webview2.\n\n" +
		"[package]\n" +
		"name = \"" + name + "\"\n" +
		"version = \"0.1.0\"\n" +
		"edition = \"" + edition + "\"\n\n" +
		"[dependencies]\n\n" +
		"[bin]\n" +
		"path = \"src/main.osty\"\n\n" +
		"[gui]\n" +
		"backend = \"webview2\"\n" +
		"entry = \"ui/index.html\"\n\n" +
		"[gui.webview2]\n" +
		"devtools = true\n" +
		"runtime = \"evergreen\"\n\n" +
		"[target.amd64-windows]\n" +
		"cgo = true\n" +
		"link = [\"osty_webview2\", \"WebView2Loader\", \"user32\", \"ole32\"]\n"
}

// ScaffoldGUIQtQuickManifest renders the Qt Quick GUI osty.toml.
// Links `osty_qt` across all four supported desktop targets.
//
// Osty: toolchain/scaffold_policy.osty:198
func ScaffoldGUIQtQuickManifest(name, edition string) string {
	return "# Qt Quick GUI app project; build libosty_qt and make it visible to the linker/runtime.\n\n" +
		"[package]\n" +
		"name = \"" + name + "\"\n" +
		"version = \"0.1.0\"\n" +
		"edition = \"" + edition + "\"\n\n" +
		"[dependencies]\n\n" +
		"[gui]\n" +
		"backend = \"qtquick\"\n" +
		"entry = \"ui/main.qml\"\n\n" +
		"[target.arm64-darwin]\n" +
		"link = [\"osty_qt\"]\n\n" +
		"[target.amd64-darwin]\n" +
		"link = [\"osty_qt\"]\n\n" +
		"[target.amd64-linux]\n" +
		"link = [\"osty_qt\"]\n\n" +
		"[target.amd64-windows]\n" +
		"link = [\"osty_qt\"]\n"
}

// ScaffoldWorkspaceManifest renders the virtual-root workspace
// osty.toml. `name` may be empty (anonymous root); when supplied
// it surfaces as a leading `# Workspace: NAME` comment. `edition`
// is currently unused but accepted for API symmetry.
//
// Osty: toolchain/scaffold_policy.osty:222
func ScaffoldWorkspaceManifest(name, edition, member string) string {
	_ = edition
	nameLine := ""
	if name != "" {
		nameLine = "# Workspace: " + name + "\n"
	}
	return nameLine +
		"# Virtual workspace root. Member paths below are resolved relative\n" +
		"# to this directory (spec §5). Add members with:\n" +
		"#\n" +
		"#     osty new --bin NAME        # inside this directory\n" +
		"#\n" +
		"# then append the directory name to the members list below.\n\n" +
		"[workspace]\n" +
		"members = [\"" + member + "\"]\n"
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
