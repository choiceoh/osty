package backend

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

const (
	bundledRuntimeSourceName = "osty_runtime.c"
	bundledRuntimeObjectName = "osty_runtime.o"
)

//go:embed runtime/osty_runtime.c
var bundledRuntimeSource string

func materializeBundledRuntime(runtimeDir string) (string, error) {
	if runtimeDir == "" {
		return "", fmt.Errorf("llvm backend: missing runtime artifact directory")
	}
	sourcePath := filepath.Join(runtimeDir, bundledRuntimeSourceName)
	if err := os.WriteFile(sourcePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		return "", err
	}
	return sourcePath, nil
}

func compileBundledRuntime(ctx context.Context, tc llvmToolchain, runtimeDir, target string) ([]string, error) {
	sourcePath, err := materializeBundledRuntime(runtimeDir)
	if err != nil {
		return nil, err
	}
	objectPath := filepath.Join(runtimeDir, bundledRuntimeObjectName)
	if err := tc.CompileCObject(ctx, sourcePath, objectPath, target); err != nil {
		return nil, err
	}
	return []string{objectPath}, nil
}
