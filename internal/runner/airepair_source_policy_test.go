package runner

import (
	"reflect"
	"testing"
)

func TestSplitSourceLinesEmpty(t *testing.T) {
	if got := SplitSourceLines(nil); got != nil {
		t.Errorf("SplitSourceLines(nil) = %#v, want nil", got)
	}
	if got := SplitSourceLines([]byte("")); got != nil {
		t.Errorf("SplitSourceLines(empty) = %#v, want nil", got)
	}
}

func TestSplitSourceLinesSingleNoNewline(t *testing.T) {
	got := SplitSourceLines([]byte("hello"))
	want := []SourceLine{
		{Start: 0, Text: "hello", Raw: "hello", Indent: "", Trimmed: "hello", HasNewline: false, LineNo: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitSourceLines(\"hello\") = %#v, want %#v", got, want)
	}
}

func TestSplitSourceLinesTwoLines(t *testing.T) {
	got := SplitSourceLines([]byte("first\nsecond\n"))
	want := []SourceLine{
		{Start: 0, Text: "first", Raw: "first\n", Indent: "", Trimmed: "first", HasNewline: true, LineNo: 1},
		{Start: 6, Text: "second", Raw: "second\n", Indent: "", Trimmed: "second", HasNewline: true, LineNo: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitSourceLines mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestSplitSourceLinesNoTrailingNewline(t *testing.T) {
	got := SplitSourceLines([]byte("a\nb"))
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if !got[0].HasNewline {
		t.Errorf("line 0 should have newline")
	}
	if got[1].HasNewline {
		t.Errorf("line 1 should not have newline")
	}
	if got[1].Raw != "b" {
		t.Errorf("line 1 raw = %q, want %q", got[1].Raw, "b")
	}
}

func TestSplitSourceLinesPreservesIndent(t *testing.T) {
	got := SplitSourceLines([]byte("    indented\n\tboth\n  no_tab"))
	if got[0].Indent != "    " {
		t.Errorf("line 0 indent = %q, want 4 spaces", got[0].Indent)
	}
	if got[0].Trimmed != "indented" {
		t.Errorf("line 0 trimmed = %q", got[0].Trimmed)
	}
	if got[1].Indent != "\t" {
		t.Errorf("line 1 indent = %q, want tab", got[1].Indent)
	}
	if got[2].Indent != "  " {
		t.Errorf("line 2 indent = %q, want 2 spaces", got[2].Indent)
	}
}

func TestSplitSourceLinesBlankLine(t *testing.T) {
	got := SplitSourceLines([]byte("a\n\nb\n"))
	if len(got) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(got))
	}
	if got[1].Text != "" || got[1].Trimmed != "" {
		t.Errorf("middle blank line not blank: %#v", got[1])
	}
	if !got[1].HasNewline {
		t.Errorf("middle blank line should still have newline")
	}
}

func TestIsIgnorablePythonLine(t *testing.T) {
	yes := []string{"// just a comment", "//", "#!/usr/bin/env python", "# coding: utf-8"}
	for _, in := range yes {
		if !IsIgnorablePythonLine(in) {
			t.Errorf("IsIgnorablePythonLine(%q) = false, want true", in)
		}
	}
	no := []string{"let x = 1", "for i in xs:", "", "x // y"}
	for _, in := range no {
		if IsIgnorablePythonLine(in) {
			t.Errorf("IsIgnorablePythonLine(%q) = true, want false", in)
		}
	}
}

func TestSrcWantsForeignLoopRepair(t *testing.T) {
	yes := []string{"for (item of items) {", "for i in range(10) {", "for i, v in enumerate(xs) {"}
	for _, in := range yes {
		if !SrcWantsForeignLoopRepair([]byte(in)) {
			t.Errorf("SrcWantsForeignLoopRepair(%q) = false, want true", in)
		}
	}
	no := []string{"for i in xs {", "range(10)", ""}
	for _, in := range no {
		if SrcWantsForeignLoopRepair([]byte(in)) {
			t.Errorf("SrcWantsForeignLoopRepair(%q) = true, want false", in)
		}
	}
}

func TestSrcWantsSemanticRepair(t *testing.T) {
	yes := []string{"xs.enumerate()", "append(xs, 1)", "len(xs)", "xs.length"}
	for _, in := range yes {
		if !SrcWantsSemanticRepair([]byte(in)) {
			t.Errorf("SrcWantsSemanticRepair(%q) = false, want true", in)
		}
	}
	no := []string{"let x = 1", "xs.push(1)"}
	for _, in := range no {
		if SrcWantsSemanticRepair([]byte(in)) {
			t.Errorf("SrcWantsSemanticRepair(%q) = true, want false", in)
		}
	}
}

func TestSrcWantsTupleLoopRepair(t *testing.T) {
	if !SrcWantsTupleLoopRepair([]byte("for k, v in m {")) {
		t.Errorf("SrcWantsTupleLoopRepair on bare tuple = false")
	}
	no := []string{"for x in xs {", "for k, v", "k, v in m", ""}
	for _, in := range no {
		if SrcWantsTupleLoopRepair([]byte(in)) {
			t.Errorf("SrcWantsTupleLoopRepair(%q) = true, want false", in)
		}
	}
}

func TestSrcWantsPythonColonBlockRepair(t *testing.T) {
	yes := []string{"if x:\n    y", "for i in xs:\n    pass"}
	for _, in := range yes {
		if !SrcWantsPythonColonBlockRepair([]byte(in)) {
			t.Errorf("SrcWantsPythonColonBlockRepair(%q) = false, want true", in)
		}
	}
	no := []string{"if x: y", "type T: Int", ""}
	for _, in := range no {
		if SrcWantsPythonColonBlockRepair([]byte(in)) {
			t.Errorf("SrcWantsPythonColonBlockRepair(%q) = true, want false", in)
		}
	}
}
