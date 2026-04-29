package osty

import (
	"github.com/osty/osty/internal/query"
)

// WorkspacePackage describes one package member in a workspace query graph.
// Dir is the normalized on-disk package directory; DotPath is the import key
// used by resolve.Workspace ("" for the root package, "lib" for a sibling,
// or an external dependency key); Name is the human-readable package name.
type WorkspacePackage struct {
	Dir     string
	DotPath string
	Name    string
}

// Inputs groups the three primitive input handles that drive the
// entire Osty query graph. Callers (LSP, CLI) use these to push the
// current world state into the Database; every derived query reads
// from them transitively.
//
// Key discipline: all path-shaped keys must be normalized via
// [NormalizePath] (and for LSP URIs, via [FromURI]). Mixing raw and
// normalized forms will produce duplicate slots.
type Inputs struct {
	// SourceText maps a normalized file path to the current raw UTF-8
	// source bytes. The LSP's `didChange` handler pushes the new
	// buffer here; CLI entry points read files from disk and push
	// once at startup. Setting bytes that hash-equal the existing
	// value is a no-op — no revision bump, no downstream invalidation.
	SourceText *query.Input[string, []byte]

	// PackageFiles maps a normalized package directory to the list of
	// .osty source files belonging to the package. Kept as a separate
	// input (rather than recomputed from the filesystem inside a
	// query) so the LSP and CLI can control when disk is scanned.
	// Unsaved files should appear in this list only if they belong
	// to the package by path convention.
	PackageFiles *query.Input[string, []string]

	// WorkspaceMembers lists every package directory in the current
	// workspace. Keyed by struct{} so there is exactly one slot.
	// CLI entry points populate this from the osty.toml manifest;
	// the LSP populates it by discovering the workspace root on open.
	WorkspaceMembers *query.Input[struct{}, []string]

	// WorkspacePackages is the package-member input used by build/CLI
	// consumers that already know the exact import key for every package,
	// including vendored external dependencies. Keyed by normalized workspace
	// root. ResolveWorkspace prefers this input when seeded, then falls back to
	// WorkspaceMembers for older LSP callers.
	WorkspacePackages *query.Input[string, []WorkspacePackage]
}

func registerInputs(db *query.Database) Inputs {
	return Inputs{
		SourceText:        query.RegisterInput[string, []byte](db, "SourceText", hashBytesInput),
		PackageFiles:      query.RegisterInput[string, []string](db, "PackageFiles", hashStringSlice),
		WorkspaceMembers:  query.RegisterInput[struct{}, []string](db, "WorkspaceMembers", hashStringSlice),
		WorkspacePackages: query.RegisterInput[string, []WorkspacePackage](db, "WorkspacePackages", hashWorkspacePackageSlice),
	}
}
