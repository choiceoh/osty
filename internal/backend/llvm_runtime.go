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
// additional driver binaries without rerunning backend codegen.
func EnsureRuntimeObject(ctx context.Context, artifacts Artifacts, target string) (string, error) {
	return ensureLocalGCRuntimeObject(ctx, clangToolchain{}, artifacts, target)
}

func ensureLocalGCRuntimeObject(ctx context.Context, tc llvmToolchain, artifacts Artifacts, target string) (string, error) {
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
	if err := tc.CompileCObject(ctx, runtimeSourcePath, runtimeObjectPath, target); err != nil {
		return "", err
	}
	return runtimeObjectPath, nil
}
