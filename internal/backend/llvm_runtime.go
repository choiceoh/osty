package backend

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
)

const (
	bundledRuntimeSourceName = "osty_runtime.c"
	bundledRuntimeObjectName = "osty_runtime.o"
)

//go:embed runtime/osty_runtime.c
var bundledRuntimeSource string

// EnsureRuntimeObject materializes the bundled runtime object alongside
// an already-emitted LLVM object artifact so external callers can link
// additional driver binaries without rerunning backend codegen. It
// preserves the release-tier `-O3 -flto=thin` compile pipeline; callers
// that have a profile available (e.g. `osty test` running a debug build)
// should call EnsureRuntimeObjectForProfile instead so debug runtime
// objects link without a multi-minute ThinLTO tail.
func EnsureRuntimeObject(ctx context.Context, artifacts Artifacts, target string) (string, error) {
	return EnsureRuntimeObjectForProfile(ctx, artifacts, target, "")
}

// EnsureRuntimeObjectForProfile is the profile-aware variant of
// EnsureRuntimeObject. profile == "debug" routes the runtime compile
// through the same `-O0` no-LTO pipeline the IR-side debug build uses
// (see llvmabi.ClangCompileObjectArgsForProfile + clangCompileCObjectArgs).
func EnsureRuntimeObjectForProfile(ctx context.Context, artifacts Artifacts, target, profile string) (string, error) {
	return ensureLocalGCRuntimeObject(ctx, clangToolchain{}, artifacts, target, profile)
}

func ensureLocalGCRuntimeObject(ctx context.Context, tc llvmToolchain, artifacts Artifacts, target, profile string) (string, error) {
	if artifacts.RuntimeDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(artifacts.RuntimeDir, 0o755); err != nil {
		return "", err
	}
	runtimeSourcePath := filepath.Join(artifacts.RuntimeDir, bundledRuntimeSourceName)
	runtimeObjectPath := filepath.Join(artifacts.RuntimeDir, bundledRuntimeObjectName)
	if err := os.WriteFile(runtimeSourcePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		return "", err
	}
	if err := tc.CompileCObject(ctx, runtimeSourcePath, runtimeObjectPath, target, profile); err != nil {
		return "", err
	}
	return runtimeObjectPath, nil
}
