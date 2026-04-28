package stdlib

import (
	"strings"
	"testing"
)

func TestTermTuiGridModulesLoad(t *testing.T) {
	reg := LoadCached()
	for _, name := range []string{"term", "tui", "grid"} {
		mod := reg.Modules[name]
		if mod == nil || mod.File == nil || mod.Package == nil {
			t.Fatalf("std.%s not loaded", name)
		}
		if pkg := reg.LookupPackage("std." + name); pkg == nil {
			t.Fatalf("LookupPackage(std.%s) = nil", name)
		}
	}
}

func TestTermModuleSurface(t *testing.T) {
	reg := LoadCached()
	src := string(reg.Modules["term"].Source)
	for _, want := range []string{
		"pub struct Terminal",
		"pub enum Key",
		"pub enum Event",
		"pub fn setRawMode(enabled: Bool) -> Result<(), Error>",
		"pub fn readKey() -> Result<Key, Error>",
		"pub fn clearScreenSeq() -> String",
		"pub fn styleSeq(style: Style) -> String",
		"\"\\u{1B}[2J\\u{1B}[H\"",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.term source missing %q", want)
		}
	}
	for _, name := range []string{"open", "write", "clearScreenSeq", "moveToSeq", "styleSeq"} {
		fn := reg.LookupFnDecl("term", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(term, %s) = nil", name)
		}
		if fn.Body == nil {
			t.Fatalf("std.term.%s body = nil, want pure helper body", name)
		}
	}
	for _, name := range []string{"size", "setRawMode", "readKey", "pollKey", "readEvent", "flush"} {
		fn := reg.LookupFnDecl("term", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(term, %s) = nil", name)
		}
		if fn.Body != nil {
			t.Fatalf("std.term.%s body != nil, want host-backed primitive stub", name)
		}
	}
	for _, method := range []string{"setRawMode", "enterAltScreen", "exitAltScreen", "clear", "moveTo", "hideCursor", "showCursor", "restore"} {
		if got := reg.LookupMethodDecl("term", "Terminal", method); got == nil {
			t.Fatalf("LookupMethodDecl(term, Terminal, %s) = nil", method)
		}
	}
}

func TestTuiModuleSurface(t *testing.T) {
	reg := LoadCached()
	src := string(reg.Modules["tui"].Source)
	for _, want := range []string{
		"pub struct Cell",
		"pub struct Frame",
		"pub struct Screen",
		"pub fn frame(size: grid.Size) -> Frame",
		"pub fn diffAnsi(self, previous: Frame?)",
		"term.clearScreenSeq()",
		"moveToSeq(point)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.tui source missing %q", want)
		}
	}
	for _, name := range []string{"style", "cell", "blankCell", "frame", "sized", "screen", "styleSeq"} {
		fn := reg.LookupFnDecl("tui", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(tui, %s) = nil", name)
		}
		if fn.Body == nil {
			t.Fatalf("std.tui.%s body = nil, want pure helper body", name)
		}
	}
	for _, method := range []string{"put", "drawText", "drawHLine", "drawVLine", "drawRect", "renderAnsi", "diffAnsi"} {
		if got := reg.LookupMethodDecl("tui", "Frame", method); got == nil {
			t.Fatalf("LookupMethodDecl(tui, Frame, %s) = nil", method)
		}
	}
	if got := reg.LookupMethodDecl("tui", "Screen", "present"); got == nil {
		t.Fatalf("LookupMethodDecl(tui, Screen, present) = nil")
	}
}

func TestGridModuleSurface(t *testing.T) {
	reg := LoadCached()
	src := string(reg.Modules["grid"].Source)
	for _, want := range []string{
		"pub struct Point",
		"pub struct Size",
		"pub struct Rect",
		"pub enum Direction",
		"pub struct Grid<T>",
		"pub fn grid<T>(width: Int, height: Int, fill: T) -> Grid<T>",
		"pub fn fromRows<T>(rows: List<List<T>>) -> Grid<T>?",
		"boundedNeighbors(self, point, directions8())",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.grid source missing %q", want)
		}
	}
	for _, name := range []string{"point", "size", "rect", "grid", "fromRows", "cardinals", "diagonals", "directions8", "directionFromDelta"} {
		fn := reg.LookupFnDecl("grid", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(grid, %s) = nil", name)
		}
		if fn.Body == nil {
			t.Fatalf("std.grid.%s body = nil, want pure helper body", name)
		}
	}
	for _, method := range []string{"size", "bounds", "index", "pointAt", "get", "set", "fill", "neighbors4", "neighbors8", "map"} {
		if got := reg.LookupMethodDecl("grid", "Grid", method); got == nil {
			t.Fatalf("LookupMethodDecl(grid, Grid, %s) = nil", method)
		}
	}
}
