package backend

import (
	"context"
	"os"
	"path/filepath"
)

const localGCRuntimeIR = `; Osty local GC runtime ABI v1
declare ptr @malloc(i64)
declare void @free(ptr)

define ptr @"osty.gc.alloc_v1"(i64 %kind, i64 %byteSize, ptr %site) {
entry:
  %is_zero = icmp eq i64 %byteSize, 0
  %alloc_size = select i1 %is_zero, i64 1, i64 %byteSize
  %ptr = call ptr @malloc(i64 %alloc_size)
  ret ptr %ptr
}

define void @"osty.gc.post_write_v1"(ptr %owner, ptr %value, i64 %slotKind) {
entry:
  ret void
}

define void @"osty.gc.root_bind_v1"(ptr %value) {
entry:
  ret void
}

define void @"osty.gc.root_release_v1"(ptr %value) {
entry:
  call void @free(ptr %value)
  ret void
}
`

func ensureLocalGCRuntimeObject(ctx context.Context, tc llvmToolchain, artifacts Artifacts, target string) (string, error) {
	if artifacts.RuntimeDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(artifacts.RuntimeDir, 0o755); err != nil {
		return "", err
	}
	runtimeIRPath := filepath.Join(artifacts.RuntimeDir, "gc_runtime.ll")
	runtimeObjectPath := filepath.Join(artifacts.RuntimeDir, "gc_runtime.o")
	if err := os.WriteFile(runtimeIRPath, []byte(localGCRuntimeIR), 0o644); err != nil {
		return "", err
	}
	if err := tc.CompileObject(ctx, runtimeIRPath, runtimeObjectPath, target); err != nil {
		return "", err
	}
	return runtimeObjectPath, nil
}
