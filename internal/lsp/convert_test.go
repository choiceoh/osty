package lsp

import (
	"reflect"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

func TestLineIndexUsesSelfHostedUTF16Policy(t *testing.T) {
	src := []byte("a😀\r\n값\nend")
	li := newLineIndex(src)
	if want := []int{0, len("a😀\r\n"), len("a😀\r\n값\n")}; !reflect.DeepEqual(li.lines, want) {
		t.Fatalf("line starts = %#v, want %#v", li.lines, want)
	}
	if got := utf16UnitsInPrefix([]byte("a😀")); got != 3 {
		t.Fatalf("utf16 units = %d, want 3", got)
	}

	got := li.ostyToLSP(token.Pos{Offset: len("a😀"), Line: 1, Column: 3})
	if got != (Position{Line: 0, Character: 3}) {
		t.Fatalf("ostyToLSP = %#v", got)
	}

	back := li.lspToOsty(Position{Line: 0, Character: 3})
	if back != (token.Pos{Offset: len("a😀"), Line: 1, Column: 3}) {
		t.Fatalf("lspToOsty = %#v", back)
	}

	insideSurrogate := li.lspToOsty(Position{Line: 0, Character: 2})
	if insideSurrogate != (token.Pos{Offset: len("a"), Line: 1, Column: 2}) {
		t.Fatalf("inside surrogate = %#v", insideSurrogate)
	}

	offsetPos := li.offsetToLSP(len("a😀\r\n값"))
	if offsetPos != (Position{Line: 1, Character: 1}) {
		t.Fatalf("offsetToLSP = %#v", offsetPos)
	}

	visible := li.ostyRange(diag.Span{Start: token.Pos{Offset: len("a😀\r\n"), Line: 2, Column: 1}})
	if visible != (Range{
		Start: Position{Line: 1, Character: 0},
		End:   Position{Line: 1, Character: 1},
	}) {
		t.Fatalf("visible range = %#v", visible)
	}
}
