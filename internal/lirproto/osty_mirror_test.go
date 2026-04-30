package lirproto

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ostyfmt "github.com/osty/osty/internal/format"
	"github.com/osty/osty/internal/parser"
)

func TestOstyMirrorParsesAndStaysFormatted(t *testing.T) {
	for _, rel := range []string{
		"toolchain/lir_proto.osty",
		"toolchain/lir_proto_test.osty",
	} {
		requireOstyMirrorSource(t, rel)
	}
}

func TestOstyMirrorExportsRequiredSurface(t *testing.T) {
	src := string(requireOstyMirrorSource(t, "toolchain/lir_proto.osty"))
	required := []string{
		"pub struct LirType",
		"pub struct LirOperand",
		"pub struct LirInstr",
		"pub struct LirTerm",
		"pub struct LirBlock",
		"pub struct LirFunction",
		"pub struct LirModule",
		"pub struct LirLowerConfig",
		"pub struct LirLowerResult",
		"pub fn lirRenderModule(",
		"pub fn lirValidateModule(",
		"pub fn lirLowerMirModule(",
		"pub fn lirPlanScalarCastOpcode(",
		"pub fn lirPlanSameFamilyCoercionOpcode(",
		"pub fn lirCastInstrForCoercion(",
		"pub fn lirProjectionPathIndices(",
	}
	for _, needle := range required {
		if !strings.Contains(src, needle) {
			t.Errorf("toolchain/lir_proto.osty missing required surface %q", needle)
		}
	}
}

func requireOstyMirrorSource(t *testing.T, rel string) []byte {
	t.Helper()
	path := filepath.Join(repoRootForLIRProtoTest(t), rel)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if file == nil {
		t.Fatalf("parse %s returned nil file: %v", rel, diags)
	}
	for _, d := range diags {
		if d != nil && d.Severity.String() == "error" {
			t.Fatalf("parse %s: %s", rel, d.Message)
		}
	}
	formatted, fmtDiags, err := ostyfmt.Source(src)
	if err != nil {
		t.Fatalf("format %s: %v (diagnostics: %v)", rel, err, fmtDiags)
	}
	if !bytes.Equal(formatted, src) {
		t.Fatalf("%s is not formatted; run `go run ./cmd/osty fmt -w %s`", rel, rel)
	}
	return src
}

func repoRootForLIRProtoTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from package dir")
		}
		dir = parent
	}
}
