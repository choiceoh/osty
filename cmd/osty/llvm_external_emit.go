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

var emitPrebuiltLLVMIR = backend.EmitPrebuiltLLVMIR

func tryExternalPackageLLVMArtifacts(ctx context.Context, emitMode backend.EmitMode, layout backend.Layout, binaryName string, features []string, entryPath string, pkg *resolve.Package) (*backend.Result, bool, error) {
	if pkg == nil || !backend.UseNativeOwnedLLVMIR(features, emitMode) {
		return nil, false, nil
	}
	out, ok, warnings, err := tryExternalPackageLLVMIR(entryPath, pkg)
	if err != nil || !ok {
		return nil, false, nil
	}
	result, err := emitPrebuiltLLVMIR(ctx, backend.Request{
		Layout:     layout,
		Emit:       emitMode,
		BinaryName: binaryName,
		Features:   features,
	}, out, warnings)
	return result, true, err
}
