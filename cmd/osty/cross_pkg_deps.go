package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/manifest"
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
// **Opt-in via `OSTY_CROSS_PKG_LINK=1`**. The dep-compile path is
// gated because the LIR Proto subprocess currently hangs on
// large-dep library compilation (toolchain ≈100k lines triggers an
// observed >10-minute stall) and because the consumer build itself
// fails earlier on upstream `<error>`-type leaks from PR3-C's
// cross-pkg dispatch arm (separate trajectory). PR-G2 ships the
// wiring; flipping the env var enables the path for small-dep
// experimentation while the upstream gaps close.
//
// Errors compiling any single dep are surfaced as warnings to stderr
// but don't abort the build — the consumer link will simply fail
// with the original undefined-symbol error if a needed dep didn't
// produce an object, which is no worse than the pre-PR-G1 status quo.
func buildCrossPkgDepObjects(ctx context.Context, _ string, _ *manifest.Manifest, eng *ostyquery.Engine, lower ostyquery.LowerKey, resolved *profile.Resolved, _ map[string]bool, layout backend.Layout) []string {
	if !crossPkgLinkEnabled() {
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
	var objects []string
	for _, pkg := range rw.Packages() {
		if pkg == nil {
			continue
		}
		depDir := ostyquery.NormalizePath(pkg.Dir)
		if depDir == "" || depDir == mainDir {
			continue
		}
		objPath, err := compileDepLibraryObject(ctx, pkg, layout)
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
// false so production builds keep the pre-PR-G2 behavior (consumer
// builds still emit `declare` for cross-pkg calls and fail at link
// time with the same undefined-symbol error as before) until the
// upstream `<error>`-type cascade closes.
func crossPkgLinkEnabled() bool {
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
func compileDepLibraryObject(ctx context.Context, pkg *resolve.Package, layout backend.Layout) (string, error) {
	entryPath := depEntryPath(pkg)
	if entryPath == "" {
		return "", fmt.Errorf("no source files")
	}
	ir, covered, _, err := nativellvmgen.TryPackageLibrary(".", entryPath, pkg)
	if err != nil {
		return "", fmt.Errorf("native llvmgen: %w", err)
	}
	if !covered || len(ir) == 0 {
		return "", fmt.Errorf("native llvmgen returned no IR (subprocess declined coverage)")
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
