package main

import (
	"context"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/nativellvmgen"
	"github.com/osty/osty/internal/resolve"
)

var tryExternalPackageLLVMIR = func(entryPath string, pkg *resolve.Package) ([]byte, bool, []error, error) {
	if pkg == nil {
		return nil, false, nil, nil
	}
	return nativellvmgen.TryPackage(".", entryPath, pkg)
}

// tryExternalPackageLibraryLLVMIR is the test-bundle variant of
// `tryExternalPackageLLVMIR`. It uses `nativellvmgen.TryPackageLibrary`
// so the subprocess applies `stripMainForLibraryMode` to the lowered
// MIR — leaving the user's `main` out of the resulting LLVM IR. The
// C test driver (`buildNativeTestDriver` in `test_native.go`) supplies
// the binary's `main` symbol; without this variant a binary-style
// package like `toolchain/` would hit a duplicate-symbol error at
// link time, which used to be guarded by an outright E_RUNNER refusal
// in `discoverNativeTests`.
var tryExternalPackageLibraryLLVMIR = func(entryPath string, pkg *resolve.Package) ([]byte, bool, []error, error) {
	if pkg == nil {
		return nil, false, nil, nil
	}
	return nativellvmgen.TryPackageLibrary(".", entryPath, pkg)
}

var emitPrebuiltLLVMIR = backend.EmitPrebuiltLLVMIR

func tryExternalPackageLLVMArtifacts(ctx context.Context, emitMode backend.EmitMode, layout backend.Layout, binaryName string, features []string, linkLibraries []string, extraObjects []string, entryPath string, pkg *resolve.Package) (*backend.Result, bool, error) {
	return tryExternalPackageLLVMArtifactsImpl(ctx, emitMode, layout, binaryName, features, linkLibraries, extraObjects, entryPath, pkg, tryExternalPackageLLVMIR)
}

// tryExternalPackageLLVMArtifactsForTests mirrors
// `tryExternalPackageLLVMArtifacts` but routes through the library-mode
// subprocess wrapper so the user's `main` does not leak into the
// emitted object. Used only from `compileNativeTestBundle`.
func tryExternalPackageLLVMArtifactsForTests(ctx context.Context, emitMode backend.EmitMode, layout backend.Layout, binaryName string, features []string, linkLibraries []string, extraObjects []string, entryPath string, pkg *resolve.Package) (*backend.Result, bool, error) {
	return tryExternalPackageLLVMArtifactsImpl(ctx, emitMode, layout, binaryName, features, linkLibraries, extraObjects, entryPath, pkg, tryExternalPackageLibraryLLVMIR)
}

func tryExternalPackageLLVMArtifactsImpl(ctx context.Context, emitMode backend.EmitMode, layout backend.Layout, binaryName string, features []string, linkLibraries []string, extraObjects []string, entryPath string, pkg *resolve.Package, tryFn func(string, *resolve.Package) ([]byte, bool, []error, error)) (*backend.Result, bool, error) {
	if pkg == nil || !backend.UseNativeOwnedLLVMIR(features, emitMode) {
		return nil, false, nil
	}
	out, ok, warnings, err := tryFn(entryPath, pkg)
	if err != nil || !ok {
		return nil, false, nil
	}
	result, err := emitPrebuiltLLVMIR(ctx, backend.Request{
		Layout:        layout,
		Emit:          emitMode,
		BinaryName:    binaryName,
		Features:      features,
		LinkLibraries: linkLibraries,
		ExtraObjects:  extraObjects,
	}, out, warnings)
	return result, true, err
}
