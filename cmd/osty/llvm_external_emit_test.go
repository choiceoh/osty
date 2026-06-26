package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/resolve"
)

func TestTryExternalPackageLLVMArtifactsUsesCoveredExternalIR(t *testing.T) {
	pkg := &resolve.Package{
		Dir:  t.TempDir(),
		Name: "demo",
	}

	oldTry := tryExternalPackageLLVMIR
	oldEmit := emitPrebuiltLLVMIR
	t.Cleanup(func() {
		tryExternalPackageLLVMIR = oldTry
		emitPrebuiltLLVMIR = oldEmit
	})

	tryExternalPackageLLVMIR = func(entryPath string, gotPkg *resolve.Package) ([]byte, bool, []error, error) {
		if entryPath != "/tmp/main.osty" {
			t.Fatalf("entryPath = %q, want /tmp/main.osty", entryPath)
		}
		if gotPkg != pkg {
			t.Fatal("got unexpected package pointer")
		}
		return []byte("; external ir"), true, []error{os.ErrExist}, nil
	}
	emitPrebuiltLLVMIR = func(_ context.Context, req backend.Request, irOut []byte, warnings []error) (*backend.Result, error) {
		if req.Emit != backend.EmitObject {
			t.Fatalf("emit mode = %q, want object", req.Emit)
		}
		if req.Layout.Profile != "debug" {
			t.Fatalf("profile = %q, want debug", req.Layout.Profile)
		}
		if req.BinaryName != "app" {
			t.Fatalf("binary name = %q, want app", req.BinaryName)
		}
		if want := []string{"osty_qt", "WebView2Loader"}; !slices.Equal(req.LinkLibraries, want) {
			t.Fatalf("link libraries = %v, want %v", req.LinkLibraries, want)
		}
		if string(irOut) != "; external ir" {
			t.Fatalf("ir = %q, want external ir", irOut)
		}
		if len(warnings) != 1 || warnings[0] != os.ErrExist {
			t.Fatalf("warnings = %#v, want propagated warning", warnings)
		}
		return &backend.Result{
			Backend: backend.NameLLVM,
			Emit:    req.Emit,
			Artifacts: backend.Artifacts{
				Object: filepath.Join(req.Layout.Root, ".osty", "out", req.Layout.Key(), "llvm", "main.o"),
			},
		}, nil
	}

	result, used, err := tryExternalPackageLLVMArtifacts(context.Background(), backend.EmitObject, backend.Layout{
		Root:    pkg.Dir,
		Profile: "debug",
	}, "app", nil, []string{"osty_qt", "WebView2Loader"}, nil, "/tmp/main.osty", pkg)
	if err != nil {
		t.Fatalf("tryExternalPackageLLVMArtifacts() error = %v", err)
	}
	if !used {
		t.Fatal("used = false, want true")
	}
	if result == nil || filepath.Base(result.Artifacts.Object) != "main.o" {
		t.Fatalf("result = %#v, want object artifact", result)
	}
}
