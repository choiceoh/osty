package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/backend"
)

func TestBackendUnsupportedDetailPrefersStructuredLLVMRoute(t *testing.T) {
	result := &backend.Result{
		Warnings: []error{
			errors.New("lowering warning without route"),
			errors.New("LLVM013 expression: call target; hint: reduce; backend-route: mir-direct"),
			backend.ErrLLVMNotImplemented,
		},
	}

	got := backendUnsupportedDetail(result)
	if got == nil {
		t.Fatal("backendUnsupportedDetail returned nil")
	}
	if want := "backend-route: mir-direct"; !strings.Contains(got.Error(), want) {
		t.Fatalf("detail = %q, want substring %q", got.Error(), want)
	}
}
