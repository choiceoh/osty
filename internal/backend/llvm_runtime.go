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
