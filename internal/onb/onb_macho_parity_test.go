package onb

import (
	"bytes"
	"testing"
)

// TestMachoHeaderParityVsOstyTable pins the Go-side Mach-O header
// against the Osty `onbMachoWriteHeader` test's pinned bytes. The
// header is 32 bytes of fixed layout (magic, cpu/subtype,
// filetype, ncmds, sizeofcmds, flags, reserved).
func TestMachoHeaderParityVsOstyTable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeU32(&buf, machoMagic64)
	writeU32(&buf, machoCPUTypeARM64)
	writeU32(&buf, machoCPUSubtypeARM64All)
	writeU32(&buf, machoFileTypeObject)
	writeU32(&buf, 3)              // ncmds
	writeU32(&buf, 100)            // sizeOfCmds
	writeU32(&buf, machoMHSubsectionsViaSyms)
	writeU32(&buf, 0)              // reserved
	got := buf.Bytes()
	if len(got) != machoHeader64Size {
		t.Fatalf("header size = %d, want %d", len(got), machoHeader64Size)
	}
	// Magic, little-endian: cf fa ed fe
	if got[0] != 0xcf || got[1] != 0xfa || got[2] != 0xed || got[3] != 0xfe {
		t.Errorf("magic bytes = % x, want cf fa ed fe", got[0:4])
	}
	// cputype ARM64: 0c 00 00 01
	if got[4] != 0x0c || got[5] != 0 || got[6] != 0 || got[7] != 0x01 {
		t.Errorf("cputype = % x, want 0c 00 00 01", got[4:8])
	}
	// filetype = MH_OBJECT (1)
	if got[12] != 0x01 {
		t.Errorf("filetype byte 0 = 0x%02x, want 0x01", got[12])
	}
	// flags = MH_SUBSECTIONS_VIA_SYMS (0x2000) → bytes 24..27 = 00 20 00 00
	if got[24] != 0 || got[25] != 0x20 || got[26] != 0 || got[27] != 0 {
		t.Errorf("flags bytes = % x, want 00 20 00 00", got[24:28])
	}
}

// TestMachoNameFieldPaddingParityVsOstyTable verifies the Go
// writer NUL-pads name fields the same way the Osty
// `onbMachoWriteName16` helper does.
func TestMachoNameFieldPaddingParityVsOstyTable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeName16(&buf, "__text")
	got := buf.Bytes()
	if len(got) != 16 {
		t.Fatalf("name field length = %d, want 16", len(got))
	}
	want := []byte("__text")
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("byte %d = 0x%02x, want 0x%02x", i, got[i], want[i])
		}
	}
	for i := len(want); i < 16; i++ {
		if got[i] != 0 {
			t.Errorf("padding byte %d = 0x%02x, want 0", i, got[i])
		}
	}
}

// TestMachoRelocBranch26ParityVsOstyTable pins the packed flags
// word the Go `writeMachOReloc` produces for a Branch26 reloc
// matching the Osty test's case (codeOffset 0x18, symbolnum 5).
func TestMachoRelocBranch26ParityVsOstyTable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeMachOReloc(&buf, machoReloc{
		address:   0x18,
		symbolnum: 5,
		pcrel:     true,
		length:    2,
		extern:    true,
		typ:       machoARM64RelocBranch26,
	})
	got := buf.Bytes()
	if len(got) != machoRelocSize {
		t.Fatalf("reloc length = %d, want %d", len(got), machoRelocSize)
	}
	// address LE = 0x18 → 18 00 00 00
	if got[0] != 0x18 || got[1] != 0 || got[2] != 0 || got[3] != 0 {
		t.Errorf("address bytes = % x, want 18 00 00 00", got[0:4])
	}
	// word = 0x2d000005 → bytes 05 00 00 2d
	if got[4] != 0x05 || got[5] != 0 || got[6] != 0 || got[7] != 0x2d {
		t.Errorf("word bytes = % x, want 05 00 00 2d", got[4:8])
	}
}

// TestMachoNlist64ParityVsOstyTable pins the Go `writeMachONlist64`
// output against the Osty test's expected bytes for an external
// function symbol (typeBits = N_EXT | N_SECT, sect = 1, value =
// 0x18).
func TestMachoNlist64ParityVsOstyTable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeMachONlist64(&buf, 5, machoNExt|machoNSect, 1, 0, 0x18)
	got := buf.Bytes()
	if len(got) != machoNlist64Size {
		t.Fatalf("nlist64 length = %d, want %d", len(got), machoNlist64Size)
	}
	if got[0] != 5 || got[1] != 0 || got[2] != 0 || got[3] != 0 {
		t.Errorf("strx bytes = % x, want 05 00 00 00", got[0:4])
	}
	if got[4] != 0x0f {
		t.Errorf("typeBits = 0x%02x, want 0x0f", got[4])
	}
	if got[5] != 1 {
		t.Errorf("sect = %d, want 1", got[5])
	}
	if got[8] != 0x18 {
		t.Errorf("value byte 0 = 0x%02x, want 0x18", got[8])
	}
}
