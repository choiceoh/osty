package llvmgen

import (
	"strings"
	"testing"
)

// Char/Byte parameter and return lowering — LLVM011 had previously
// first-walled on `lspUtf16UnitsForChar(ch: Char)` in the native
// toolchain probe. Once Char lowered to i32 and Byte to i8, parameter
// signatures and return types accept them without a diagnostic.
func TestCharParameterLowersAsI32(t *testing.T) {
	file := parseLLVMGenFile(t, `fn width(ch: Char) -> Int {
    if ch.toInt() >= 128 {
        return 2
    }
    return 1
}

fn main() {
    println(width('A'))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_param.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define i64 @width(i32",
		"zext i32",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestByteParameterLowersAsI8(t *testing.T) {
	file := parseLLVMGenFile(t, `fn ordByte(b: Byte) -> Int {
    b.toInt()
}

fn main() {
    println(ordByte(b'Z'))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/byte_param.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define i64 @ordByte(i8",
		"zext i8",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Char comparison lowers via unsigned icmp so codepoints above 127 don't
// flip under signed ordering. The `'0' <= c && c <= '9'` pattern is the
// classic lexer shape that the toolchain front-end relies on.
func TestCharCompareUsesUnsignedPredicate(t *testing.T) {
	file := parseLLVMGenFile(t, `fn isDigit(c: Char) -> Bool {
    '0' <= c && c <= '9'
}

fn main() {
    if isDigit('5') {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_compare.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"icmp ule i32",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "icmp sle i32") {
		t.Fatalf("Char compare used signed predicate; want unsigned:\n%s", got)
	}
}

// Int.toChar() lowers as a trunc so the Osty-level `(n + '0'.toInt()).toChar()`
// pattern in stdlib/char.osty hex digit construction works.
func TestIntToCharTruncates(t *testing.T) {
	file := parseLLVMGenFile(t, `fn digit(n: Int) -> Char {
    ('0'.toInt() + n).toChar()
}

fn main() {
    let c = digit(3)
    if c == '3' {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/int_to_char.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"trunc i64",
		"icmp eq i32",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// lspUtf16UnitsForChar is the exact shape that blocked the native
// toolchain probe with LLVM011. Once Char is lowered, this compiles
// cleanly with no unsupported diagnostic.
func TestLspUtf16ShapeCompiles(t *testing.T) {
	file := parseLLVMGenFile(t, `fn lspUtf16UnitsForChar(ch: Char) -> Int {
    if ch.toInt() >= 0x10000 {
        return 2
    }
    return 1
}

fn main() {
    println(lspUtf16UnitsForChar('A'))
}
`)
	_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/lsp_utf16.osty"})
	if err != nil {
		t.Fatalf("lspUtf16UnitsForChar still errors: %v", err)
	}
}

// Int.toByte() is spec'd as Result<Byte, Error> but the toolchain uses it
// as an infallible trunc at sites like `'\\'.toInt().toByte()`. Verify we
// lower it as `trunc i64 to i8` rather than failing with LLVM015.
func TestIntToByteLowersAsTrunc(t *testing.T) {
	file := parseLLVMGenFile(t, `fn cmpBackslash(b: Byte) -> Bool {
    b == '\\'.toInt().toByte()
}

fn main() {
    if cmpBackslash(b'\\') {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/int_to_byte.osty"})
	if err != nil {
		t.Fatalf("Int.toByte() chain still errors: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"trunc i64",
		"icmp eq i8",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Byte.toChar() widens a Byte (i8) receiver to a Char (i32) code point.
// Supports the `b.toChar().toString()` pattern the self-host emitter uses
// to materialise a one-byte UTF-8 string for printable ASCII.
func TestByteToCharLowersAsZext(t *testing.T) {
	file := parseLLVMGenFile(t, `fn byteAsChar(b: Byte) -> Char {
    b.toChar()
}

fn main() {
    if byteAsChar(b'A') == 'A' {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/byte_to_char.osty"})
	if err != nil {
		t.Fatalf("Byte.toChar() still errors: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "zext i8") {
		t.Fatalf("generated IR missing zext i8 widening:\n%s", got)
	}
}

// Char.toString() calls the osty_rt_char_to_string runtime helper to
// materialise a UTF-8-encoded single-char String. The toolchain relies
// on this for `b.toChar().toString()` in `llvmCStringEscape`.
func TestCharToStringCallsRuntimeHelper(t *testing.T) {
	file := parseLLVMGenFile(t, `fn describe(c: Char) -> String {
    c.toString()
}

fn main() {
    println(describe('x'))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_to_string.osty"})
	if err != nil {
		t.Fatalf("Char.toString() still errors: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_char_to_string",
		"declare ptr @osty_rt_char_to_string(i32)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// hirLowerParseRadix-style loop: iterate `text.chars()` (List<Char>),
// pass each char to a helper, and match on char literals. This is the
// exact family of shapes that first-walled the merged AST probe when
// the helper still expected String.
func TestCharsLoopFeedsCharMatchHelper(t *testing.T) {
	file := parseLLVMGenFile(t, `fn charToDigit(ch: Char) -> Int {
    match ch {
        '0' -> 0,
        '1' -> 1,
        'a' -> 10, 'A' -> 10,
        _ -> -1,
    }
}

fn parseHead(text: String) -> Int {
    let mut acc = 0
    let chars = text.chars()
    for ch in chars {
        let d = charToDigit(ch)
        if d < 0 {
            return acc
        }
        acc = acc * 16 + d
    }
    acc
}

fn main() {
    println(parseHead("1A"))
}
`)
	_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/chars_digit_loop.osty"})
	if err != nil {
		t.Fatalf("chars loop + char match helper still errors: %v", err)
	}
}

// Byte.toString() calls the osty_rt_byte_to_string runtime helper to
// materialise a single-byte String. Useful for raw-byte display paths.
func TestByteToStringCallsRuntimeHelper(t *testing.T) {
	file := parseLLVMGenFile(t, `fn describeByte(b: Byte) -> String {
    b.toString()
}

fn main() {
    println(describeByte(b'M'))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/byte_to_string.osty"})
	if err != nil {
		t.Fatalf("Byte.toString() still errors: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_byte_to_string",
		"declare ptr @osty_rt_byte_to_string(i8)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
