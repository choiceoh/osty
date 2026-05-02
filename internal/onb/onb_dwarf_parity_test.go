package onb

import (
	"bytes"
	"testing"
)

// TestDwarfLEB128ParityVsOstyTable verifies the Go ULEB128 / SLEB128
// encoders produce the same byte sequences `toolchain/onb_dwarf_test.osty`
// pins for its LEB128 round-trips. If either side drifts, both
// expected columns need to update together.
func TestDwarfLEB128ParityVsOstyTable(t *testing.T) {
	t.Parallel()

	checkULEB := func(name string, v uint64, want []byte) {
		t.Helper()
		var buf bytes.Buffer
		writeULEB128(&buf, v)
		got := buf.Bytes()
		if !bytes.Equal(got, want) {
			t.Errorf("%s: Go = % x; Osty = % x", name, got, want)
		}
	}

	checkSLEB := func(name string, v int64, want []byte) {
		t.Helper()
		var buf bytes.Buffer
		writeSLEB128(&buf, v)
		got := buf.Bytes()
		if !bytes.Equal(got, want) {
			t.Errorf("%s: Go = % x; Osty = % x", name, got, want)
		}
	}

	// Single-byte ULEB128 boundary cases.
	checkULEB("uleb 0", 0, []byte{0x00})
	checkULEB("uleb 0x7f", 0x7f, []byte{0x7f})
	// Two-byte (continuation bit kicks in at 0x80).
	checkULEB("uleb 0x80", 0x80, []byte{0x80, 0x01})
	checkULEB("uleb 0x3fff", 0x3fff, []byte{0xff, 0x7f})
	// Three-byte.
	checkULEB("uleb 0x4000", 0x4000, []byte{0x80, 0x80, 0x01})

	// SLEB128 positive boundary.
	checkSLEB("sleb 0", 0, []byte{0x00})
	checkSLEB("sleb 63", 63, []byte{0x3f})
	checkSLEB("sleb 64", 64, []byte{0xc0, 0x00})

	// SLEB128 negative boundary.
	checkSLEB("sleb -1", -1, []byte{0x7f})
	checkSLEB("sleb -64", -64, []byte{0x40})
	checkSLEB("sleb -65", -65, []byte{0xbf, 0x7f})
}

// TestDwarfLineParityVsOstyTable pins the line-section unit header
// bytes the Go encoder produces against the Osty test's
// expected layout. We compare specific positions rather than the
// whole byte stream because the header includes filename / dir
// strings that may legitimately differ in length across changes
// — but the metadata block (first 6 bytes after unit_length /
// version / header_length) and the version word must stay
// stable.
func TestDwarfLineParityVsOstyTable(t *testing.T) {
	t.Parallel()

	prog := dwarfLineProgram{
		Files: []dwarfLineFile{{Name: "main.osty"}},
	}
	got, err := emitDwarfLine(prog)
	if err != nil {
		t.Fatalf("emitDwarfLine: %v", err)
	}
	if len(got.Bytes) < 16 {
		t.Fatalf("line section too short: %d bytes", len(got.Bytes))
	}
	// Bytes 4-5: version (LE u16) = 4
	version := uint16(got.Bytes[4]) | uint16(got.Bytes[5])<<8
	if version != dwarfVersion {
		t.Errorf("version = %d, want %d", version, dwarfVersion)
	}
	// Bytes 10..16 are the metadata block (header begins at byte 10
	// = 4 unit_length + 2 version + 4 header_length).
	if got.Bytes[10] != dwarfMinInstLength {
		t.Errorf("min_inst_length = 0x%02x, want 0x%02x", got.Bytes[10], dwarfMinInstLength)
	}
	if got.Bytes[11] != dwarfMaxOpsPerInst {
		t.Errorf("max_ops_per_inst = 0x%02x, want 0x%02x", got.Bytes[11], dwarfMaxOpsPerInst)
	}
	if got.Bytes[12] != dwarfDefaultIsStmt {
		t.Errorf("default_is_stmt = 0x%02x, want 0x%02x", got.Bytes[12], dwarfDefaultIsStmt)
	}
	if got.Bytes[13] != dwarfLineBaseByte {
		t.Errorf("line_base byte = 0x%02x, want 0x%02x", got.Bytes[13], dwarfLineBaseByte)
	}
	if got.Bytes[14] != dwarfLineRange {
		t.Errorf("line_range = 0x%02x, want 0x%02x", got.Bytes[14], dwarfLineRange)
	}
	if got.Bytes[15] != dwarfOpcodeBase {
		t.Errorf("opcode_base = 0x%02x, want 0x%02x", got.Bytes[15], dwarfOpcodeBase)
	}
}

// TestDwarfInfoParityVsOstyTable verifies the Go info-section
// emitter produces the canonical header (version + abbrev_offset
// + address_size) the Osty `onbEmitDwarfInfo` test asserts. The
// per-DIE byte sequences depend on string offsets that vary with
// table insertion order, so this gate stays focused on the
// invariant unit-header prefix + the count of reloc anchors.
func TestDwarfInfoParityVsOstyTable(t *testing.T) {
	t.Parallel()

	strs := newDwarfStringTable()
	cu := dwarfCompileUnitInputs{
		ProducerStrOffset: strs.Add("osty (onb)"),
		Language:          dwarfLangC99,
		NameStrOffset:     strs.Add("main.osty"),
		CompDirStrOffset:  strs.Add("/tmp"),
		LowPC:             0,
		HighPCSize:        0x40,
		StmtListOffset:    0,
		StringTable:       strs,
	}
	got := emitDwarfInfo(cu, nil, nil)

	if len(got.Bytes) < 11 {
		t.Fatalf("info section too short: %d bytes", len(got.Bytes))
	}
	// Version word is bytes 4-5 (LE u16) = 4
	version := uint16(got.Bytes[4]) | uint16(got.Bytes[5])<<8
	if version != dwarfVersion {
		t.Errorf("version = %d, want %d", version, dwarfVersion)
	}
	// debug_abbrev_offset is bytes 6-9 (LE u32) = 0
	abbrevOff := uint32(got.Bytes[6]) | uint32(got.Bytes[7])<<8 |
		uint32(got.Bytes[8])<<16 | uint32(got.Bytes[9])<<24
	if abbrevOff != 0 {
		t.Errorf("debug_abbrev_offset = %d, want 0", abbrevOff)
	}
	// address_size is byte 10 = 8 (aarch64)
	if got.Bytes[10] != dwarfAddressSize {
		t.Errorf("address_size = %d, want %d", got.Bytes[10], dwarfAddressSize)
	}
	// CU's own low_pc anchor is the only reloc target when no
	// subprograms participate.
	if len(got.LowPCOffsets) != 1 {
		t.Errorf("lowPCOffsets count = %d, want 1", len(got.LowPCOffsets))
	}
}

// TestDwarfInfoParityWithSubprograms pins the count of low_pc
// reloc anchors when the CU carries multiple subprograms — one
// per subprogram + the CU's own.
func TestDwarfInfoParityWithSubprograms(t *testing.T) {
	t.Parallel()

	strs := newDwarfStringTable()
	cu := dwarfCompileUnitInputs{
		ProducerStrOffset: strs.Add("osty (onb)"),
		Language:          dwarfLangC99,
		NameStrOffset:     strs.Add("main.osty"),
		CompDirStrOffset:  strs.Add("/tmp"),
		LowPC:             0,
		HighPCSize:        0x80,
		StmtListOffset:    0,
		StringTable:       strs,
	}
	subs := []dwarfSubprogramInput{
		{NameStrOffset: strs.Add("main"), LowPC: 0, SizeBytes: 0x40},
		{NameStrOffset: strs.Add("helper"), LowPC: 0x40, SizeBytes: 0x40},
	}
	got := emitDwarfInfo(cu, subs, nil)
	if len(got.LowPCOffsets) != 3 {
		t.Errorf("lowPCOffsets count = %d, want 3 (1 CU + 2 subs)", len(got.LowPCOffsets))
	}
}

// TestDwarfAbbrevParityVsOstyTable pins the first three bytes of the
// abbrev table (compile-unit code + tag + has-children flag) the
// way the Osty test asserts. A full byte-by-byte comparison would
// be hostage to encoder-internal layout choices; this lighter
// gate is enough to catch most drift.
func TestDwarfAbbrevParityVsOstyTable(t *testing.T) {
	t.Parallel()

	got := emitDwarfAbbrev()
	if len(got) < 3 {
		t.Fatalf("abbrev too short: %d bytes", len(got))
	}
	if got[0] != 0x01 {
		t.Errorf("abbrev[0] = 0x%02x, want 0x01 (compile-unit code)", got[0])
	}
	if got[1] != 0x11 {
		t.Errorf("abbrev[1] = 0x%02x, want 0x11 (DW_TAG_compile_unit)", got[1])
	}
	if got[2] != 0x01 {
		t.Errorf("abbrev[2] = 0x%02x, want 0x01 (DW_CHILDREN_yes)", got[2])
	}
	// End-of-table byte is the final 0.
	if got[len(got)-1] != 0x00 {
		t.Errorf("abbrev[last] = 0x%02x, want 0x00 (table terminator)", got[len(got)-1])
	}
	if len(got) <= 70 {
		t.Errorf("abbrev too short for 7 entries: got %d, want > 70", len(got))
	}
}
