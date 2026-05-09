package backend

import (
	"strings"
	"testing"
)

// TestStage0P23ScanBoolReturn — `checkHasImportAlias` shape:
// for-in-list where the body tests each element and immediately returns
// true on first match; returns false after the loop.
func TestStage0P23ScanBoolReturn(t *testing.T) {
	src := `fn contains(xs: List<Int>, target: Int) -> Bool {
    for x in xs {
        if x == target {
            return true
        }
    }
    false
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"define i1 @contains(ptr %xs, i64 %target)",
		"call i64 @osty_rt_list_len(",
		"alloca i64",
		"@osty_rt_list_get_i64(",
		"icmp eq i64",
		// early-exit arm: ret i1 true
		"ret i1 true",
		// loop-exit arm: ret i1 false
		"ret i1 false",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P23ScanIntReturn — `coreCloneMapLookup` shape:
// accumulator `i` bumped in the false-prep block; early-exit returns i
// when element matches; loop-exit also returns i.
func TestStage0P23ScanIntReturn(t *testing.T) {
	src := `fn indexOf(xs: List<Int>, target: Int) -> Int {
    let mut i = 0
    for x in xs {
        if x == target {
            return i
        }
        i = i + 1
    }
    i
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"define i64 @indexOf(ptr %xs, i64 %target)",
		"call i64 @osty_rt_list_len(",
		"alloca i64",
		"@osty_rt_list_get_i64(",
		"icmp eq i64",
		// two ret paths
		"ret i64",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
	// Expect two ret i64 instructions (early-exit + loop-exit).
	count := strings.Count(got, "ret i64")
	if count < 2 {
		t.Errorf("expected at least 2 `ret i64` paths, got %d\n--- output ---\n%s", count, got)
	}
}

// TestStage0P23ScanStringContains — String-equality scan.
func TestStage0P23ScanStringContains(t *testing.T) {
	src := `fn hasString(xs: List<String>, target: String) -> Bool {
    for s in xs {
        if s == target {
            return true
        }
    }
    false
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"define i1 @hasString(ptr %xs, ptr %target)",
		"@osty_rt_list_get_string(",
		"@osty_rt_strings_Equal(",
		"ret i1 true",
		"ret i1 false",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}
