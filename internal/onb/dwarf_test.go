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
	if len(out.Bytes) < 12 {
		t.Fatalf("emitDwarfLine output too short: %d bytes", len(out.Bytes))
	}
	// unit_length excludes the 4-byte length field itself; verify the
	// header reports a sane DWARF 4 prologue.
	version := uint16(out.Bytes[4]) | uint16(out.Bytes[5])<<8
	if version != 4 {
		t.Fatalf("dwarf version = %d, want 4", version)
	}
}

// TestEmitDwarfAbbrevHasCompileUnitEntry verifies the abbrev table starts
// with the expected DW_TAG_compile_unit + DW_CHILDREN_no header so the CU
// DIE encoder agrees with the abbrev format.
func TestEmitDwarfAbbrevHasCompileUnitEntry(t *testing.T) {
	t.Parallel()

	out := emitDwarfAbbrev()
	if len(out) == 0 {
		t.Fatal("emitDwarfAbbrev returned empty bytes")
	}
	// First byte is the abbrev code (uleb128 == 1 for our CU entry), then
	// the tag (uleb128 == 0x11 for DW_TAG_compile_unit).
	if out[0] != byte(dwarfAbbrevCompileUnit) {
		t.Fatalf("abbrev code = %d, want %d", out[0], dwarfAbbrevCompileUnit)
	}
	if out[1] != byte(dwarfTagCompileUnit) {
		t.Fatalf("tag = %#x, want %#x (DW_TAG_compile_unit)", out[1], dwarfTagCompileUnit)
	}
	if out[2] != dwarfChildrenYes {
		t.Fatalf("has_children = %d, want DW_CHILDREN_yes (CU owns subprogram DIEs)", out[2])
	}
}

// TestDwarfStringTableDeduplicates verifies the table returns the same
// offset for repeated inserts of the same string. Skipping dedup would
// break DIE attribute references that expect a stable offset.
func TestDwarfStringTableDeduplicates(t *testing.T) {
	t.Parallel()

	tbl := newDwarfStringTable()
	if off := tbl.Add(""); off != 0 {
		t.Fatalf("empty string offset = %d, want 0", off)
	}
	a := tbl.Add("alpha")
	b := tbl.Add("alpha")
	if a != b {
		t.Fatalf("dedup failed: %d != %d", a, b)
	}
	c := tbl.Add("beta")
	if c == a {
		t.Fatalf("distinct strings collided at offset %d", c)
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

// TestEmitDwarfInfoEmitsSubprogramDIEPerFunction verifies the encoder
// stamps one DW_TAG_subprogram low_pc relocation per function. dsymutil
// silently rejects CUs without subprogram children, so this is a load-
// bearing invariant.
func TestEmitDwarfInfoEmitsSubprogramDIEPerFunction(t *testing.T) {
	t.Parallel()

	cu := dwarfCompileUnitInputs{
		ProducerStrOffset: 1,
		Language:          dwarfLangC99,
		NameStrOffset:     2,
		CompDirStrOffset:  3,
		LowPC:             0,
		HighPCSize:        0x40,
		StmtListOffset:    0,
	}
	subs := []dwarfSubprogramInput{
		{NameStrOffset: 4, LowPC: 0x00, SizeBytes: 0x20},
		{NameStrOffset: 5, LowPC: 0x20, SizeBytes: 0x20},
	}
	enc := emitDwarfInfo(cu, subs)
	if len(enc.LowPCOffsets) != 1+len(subs) {
		t.Fatalf("low_pc offsets count = %d, want %d (CU + 2 subprograms)", len(enc.LowPCOffsets), 1+len(subs))
	}
	for i := 1; i < len(enc.LowPCOffsets); i++ {
		if enc.LowPCOffsets[i] <= enc.LowPCOffsets[i-1] {
			t.Fatalf("low_pc offsets not monotonic at %d: %v", i, enc.LowPCOffsets)
		}
	}
}

// TestEmitObjectIncludesDwarfRelocations checks that the Mach-O writer
// emits non-extern UNSIGNED relocations for both `__debug_info` and
// `__debug_line` DWARF sections. clang-emitted .o uses this exact
// pattern; dsymutil rejects external (symbol-based) relocs in DWARF
// sections with "No valid relocations found".
func TestEmitObjectIncludesDwarfRelocations(t *testing.T) {
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
	for _, sec := range f.Sections {
		switch {
		case sec.Seg == "__DWARF" && sec.Name == "__debug_info":
			if len(sec.Relocs) == 0 {
				t.Fatalf("__debug_info has 0 relocs; dsymutil will skip the .o")
			}
		case sec.Seg == "__DWARF" && sec.Name == "__debug_line":
			if len(sec.Relocs) != 1 {
				t.Fatalf("__debug_line relocs = %d, want 1 (set_address)", len(sec.Relocs))
			}
		}
	}
}

// TestEmitObjectIncludesAllDwarfSections verifies that the four DWARF
// sections lldb expects (`__debug_line`, `__debug_info`, `__debug_abbrev`,
// `__debug_str`) all land in the `__DWARF` segment with non-empty content
// so an external debugger has the full prologue chain to walk.
func TestEmitObjectIncludesAllDwarfSections(t *testing.T) {
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
	want := map[string]bool{
		"__debug_line":   false,
		"__debug_info":   false,
		"__debug_abbrev": false,
		"__debug_str":    false,
	}
	for _, sec := range f.Sections {
		if sec.Seg != "__DWARF" {
			continue
		}
		if _, ok := want[sec.Name]; !ok {
			continue
		}
		data, err := sec.Data()
		if err != nil {
			t.Fatalf("%s Data() error: %v", sec.Name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s section is empty", sec.Name)
		}
		want[sec.Name] = true
	}
	for name, sawIt := range want {
		if !sawIt {
			t.Fatalf("Mach-O missing __DWARF/%s section", name)
		}
	}
}

// TestDwarfdumpAcceptsCompileUnitInfo runs `dwarfdump --debug-info` on the
// emitted Mach-O and verifies the CU DIE contains the producer / language
// / name / low_pc / high_pc / stmt_list attributes wired to non-default
// values. dwarfdump warnings are treated as failures so a half-baked
// abbrev table or off-by-one DIE encoding fails fast.
func TestDwarfdumpAcceptsCompileUnitInfo(t *testing.T) {
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
				program.Functions[fi].Blocks[bi].LineSpans[i] = LineSpan{Line: 1, Column: 1}
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
	out, err := exec.Command("dwarfdump", "--debug-info", objPath).CombinedOutput()
	if err != nil {
		t.Fatalf("dwarfdump returned error: %v\n%s", err, out)
	}
	text := string(out)
	if strings.Contains(text, "warning") {
		t.Fatalf("dwarfdump emitted warnings (likely malformed DIE):\n%s", text)
	}
	for _, want := range []string{
		"DW_TAG_compile_unit",
		"DW_AT_producer",
		"DW_AT_language",
		"DW_AT_name",
		"DW_AT_low_pc",
		"DW_AT_high_pc",
		"DW_AT_stmt_list",
		"main.osty",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dwarfdump missing %q:\n%s", want, text)
		}
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
