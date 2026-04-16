package gen_test

import (
	"strings"
	"testing"
)

func TestPrimitiveMethodsCompileAndRun(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `fn main() {
    let n: Int = -7
    let narrowed = n.abs().toInt8().unwrap()
    let checked = n.checkedAdd(10).unwrap()
    let missing = n.checkedDiv(0).isNone()
    println("{n.abs()} {n.min(3)} {n.max(3)} {n.clamp(-5, 5)} {n.signum()} {n.pow(2)} {narrowed} {checked} {missing} {n.toString()}")

    let f: Float = 9.25
    println("{f.sqrt().round().toString()} {f.floor().toInt()} {f.fract().toString()} {f.isFinite()}")
    let i8: Int8 = 5
    let f32: Float32 = 4.0
    println("{i8.checkedAdd(1).unwrap().toInt()} {i8.min(3).toInt()} {i8.clamp(0, 4).toInt()} {f32.max(5.0).toFloat().toString()}")

    let c: Char = 'a'
    println("{c.toUpper().toString()} {c.isAlpha()} {c.isDigit()} {c.toInt()}")

    let s = " osty lang "
    println("{s.trim().toUpper()} {s.trim().contains("sty")} {s.trim().startsWith("os")} {s.trim().endsWith("ng")}")
    let parts = "a,b,c".split(",")
    println("{parts.len()} {"-".join(parts)} {"hello".replace("l", "L")} {"ha".repeat(3)}")

    let b = "AZ".toBytes()
    println("{b.len()} {b.get(0).unwrap().toInt()} {b.toString().unwrap()} {b.concat("!".toBytes()).toString().unwrap()}")

    match "123".toInt() {
        Ok(v) -> println(v + 1),
        Err(_) -> println("bad int"),
    }
    match "2.5".toFloat() {
        Ok(v) -> println(v.toInt()),
        Err(_) -> println("bad float"),
    }
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"7 -7 3 -5 -1 49 7 3 true -7",
		"3 9 0.25 true",
		"6 3 4 5",
		"A true false 97",
		"OSTY LANG true true true",
		"3 a-b-c heLlo hahaha",
		"2 65 AZ AZ!",
		"124",
		"2",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
}

func TestPrimitiveMethodsDeepRuntimeEdges(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.bytes

fn main() {
    let max: Int8 = 127
    let min: Int8 = -128
    let ten: Int8 = 10
    let u: UInt8 = 250

    println("{max.checkedAdd(1).isNone()} {max.saturatingAdd(1).toInt()} {min.checkedSub(1).isNone()} {min.saturatingSub(1).toInt()}")
    println("{min.checkedAbs().isNone()} {min.wrappingAbs().toInt()} {min.checkedNeg().isNone()}")
    println("{min.checkedDiv(-1).isNone()} {min.saturatingDiv(-1).toInt()} {min.wrappingDiv(-1).toInt()} {min.wrappingMod(-1).toInt()}")
    println("{ten.checkedMul(13).isNone()} {ten.saturatingMul(13).toInt()} {ten.checkedShl(8).isNone()} {ten.wrappingShl(8).toInt()}")
    println("{u.checkedAdd(10).isNone()} {u.saturatingAdd(10).toInt()} {u.checkedSub(251).isNone()} {u.saturatingSub(251).toInt()} {u.checkedMul(2).isNone()} {u.saturatingMul(2).toInt()}")

    let accent = "e\u{0301}"
    let family = "\u{1F468}\u{200D}\u{1F469}\u{200D}\u{1F467}"
    let flag = "\u{1F1FA}\u{1F1F8}"
    println("{accent.chars().len()} {accent.graphemes().len()} {family.graphemes().len()} {flag.graphemes().len()}")

    let bad = bytes.fromHex("ff").unwrap()
    println("{bad.toString().isErr()} {bytes.toString(bad).isErr()}")
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"true 127 true -128",
		"true -128 true",
		"true 127 -128 0",
		"true 127 true 10",
		"true 255 true 0 true 255",
		"2 1 1 1",
		"true true",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
}
