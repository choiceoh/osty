package onb

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/mir"
)

func TestWriteULEB128(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input uint64
		want  []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{0x3FFF_FFFF_FFFF_FFFF, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x3f}},
	}
	for _, tc := range cases {
		var b bytes.Buffer
		writeULEB128(&b, tc.input)
		if !bytes.Equal(b.Bytes(), tc.want) {
			t.Fatalf("writeULEB128(%d) = % x, want % x", tc.input, b.Bytes(), tc.want)
		}
	}
}

func TestWriteSLEB128(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input int64
		want  []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{-1, []byte{0x7f}},
		{63, []byte{0x3f}},
		{64, []byte{0xc0, 0x00}},
		{-64, []byte{0x40}},
		{-65, []byte{0xbf, 0x7f}},
	}
	for _, tc := range cases {
		var b bytes.Buffer
		writeSLEB128(&b, tc.input)
		if !bytes.Equal(b.Bytes(), tc.want) {
			t.Fatalf("writeSLEB128(%d) = % x, want % x", tc.input, b.Bytes(), tc.want)
		}
	}
}

func TestEmitDwarfLineProducesValidPrologue(t *testing.T) {
	t.Parallel()

	prog := dwarfLineProgram{
		IncludeDirs: []string{"/tmp"},
		Files:       []dwarfLineFile{{Name: "main.osty", Dir: 1}},
		Rows: []dwarfLineRow{
			{PC: 0x10, File: 1, Line: 1, Column: 1},
			{PC: 0x20, File: 1, Line: 2, Column: 1},
		},
		TextSize: 0x40,
	}
	out, err := emitDwarfLine(prog)
	if err != nil {
		t.Fatalf("emitDwarfLine returned error: %v", err)
	}
	if len(out) < 12 {
		t.Fatalf("emitDwarfLine output too short: %d bytes", len(out))
	}
	// unit_length excludes the 4-byte length field itself; verify the
	// header reports a sane DWARF 4 prologue.
	version := uint16(out[4]) | uint16(out[5])<<8
	if version != 4 {
		t.Fatalf("dwarf version = %d, want 4", version)
	}
}

// TestEmitObjectIncludesDwarfLineSection exercises the full ONB pipeline
// and checks the produced Mach-O has a __DWARF segment carrying a
// non-empty __debug_line section.
func TestEmitObjectIncludesDwarfLineSection(t *testing.T) {
	t.Parallel()

	mod := &mir.Module{
		Functions: []*mir.Function{intPrintlnMainMIR(42)},
	}
	target := Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"}
	program, err := LowerMIR(mod, target)
	if err != nil {
		t.Fatalf("LowerMIR returned error: %v", err)
	}
	program.SourcePath = "/tmp/main.osty"
	program.Package = "main"
	// Force a non-zero LineSpan so the DWARF emitter agrees there's
	// something to publish — the synthetic MIR fixtures don't carry
	// real source positions.
	for fi := range program.Functions {
		for bi := range program.Functions[fi].Blocks {
			for i := range program.Functions[fi].Blocks[bi].LineSpans {
				program.Functions[fi].Blocks[bi].LineSpans[i] = LineSpan{Line: 1, Column: 1}
			}
		}
	}
	obj, err := EmitObject(program)
	if err != nil {
		t.Fatalf("EmitObject returned error: %v", err)
	}
	f, err := macho.NewFile(bytes.NewReader(obj))
	if err != nil {
		t.Fatalf("macho.NewFile returned error: %v", err)
	}
	var sawDebugLine bool
	for _, sec := range f.Sections {
		if sec.Seg == "__DWARF" && sec.Name == "__debug_line" {
			sawDebugLine = true
			data, err := sec.Data()
			if err != nil {
				t.Fatalf("__debug_line Data() error: %v", err)
			}
			if len(data) < 12 {
				t.Fatalf("__debug_line section is implausibly short: %d bytes", len(data))
			}
		}
	}
	if !sawDebugLine {
		t.Fatalf("Mach-O sections missing __DWARF/__debug_line: %+v", f.Sections)
	}
}

// TestDwarfdumpAcceptsLineProgram boots the full ONB pipeline and runs the
// host's `dwarfdump` against the resulting object. It is darwin-only
// because dwarfdump on linux uses different invocation conventions and
// the ONB target itself is mach-o-only at this slice. Skips when
// dwarfdump isn't installed.
func TestDwarfdumpAcceptsLineProgram(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("dwarfdump validation requires darwin/arm64 host")
	}
	if _, err := exec.LookPath("dwarfdump"); err != nil {
		t.Skip("dwarfdump not on PATH")
	}

	mod := &mir.Module{
		Functions: []*mir.Function{intPrintlnMainMIR(42)},
	}
	target := Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"}
	program, err := LowerMIR(mod, target)
	if err != nil {
		t.Fatalf("LowerMIR returned error: %v", err)
	}
	program.SourcePath = "/tmp/main.osty"
	program.Package = "main"
	for fi := range program.Functions {
		for bi := range program.Functions[fi].Blocks {
			for i := range program.Functions[fi].Blocks[bi].LineSpans {
				program.Functions[fi].Blocks[bi].LineSpans[i] = LineSpan{Line: int(i + 1), Column: 1}
			}
		}
	}
	obj, err := EmitObject(program)
	if err != nil {
		t.Fatalf("EmitObject returned error: %v", err)
	}
	dir := t.TempDir()
	objPath := filepath.Join(dir, "main.o")
	if err := writeObjectFile(objPath, obj); err != nil {
		t.Fatalf("write object: %v", err)
	}
	out, err := exec.Command("dwarfdump", "--debug-line", objPath).CombinedOutput()
	if err != nil {
		t.Fatalf("dwarfdump returned error: %v\n%s", err, out)
	}
	text := string(out)
	if strings.Contains(text, "warning") {
		t.Fatalf("dwarfdump emitted warnings (likely malformed prologue):\n%s", text)
	}
	for _, want := range []string{"version: 4", "default_is_stmt: 1", "line_base: -5", "line_range: 14", "opcode_base: 13", "main.osty"} {
		if !strings.Contains(text, want) {
			t.Fatalf("dwarfdump missing %q:\n%s", want, text)
		}
	}
}

func writeObjectFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
