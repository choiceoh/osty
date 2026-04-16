package lsp

import (
	"reflect"
	"testing"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

func semTypeIndex(t *testing.T, name string) uint32 {
	t.Helper()
	for i, typ := range semanticTokenTypes {
		if typ == name {
			return uint32(i)
		}
	}
	t.Fatalf("semantic token type %q not found", name)
	return 0
}

func TestClassifyTokenUsesSelfHostedPolicy(t *testing.T) {
	tests := []struct {
		name       string
		tok        token.Token
		symbolKind resolve.SymbolKind
		wantType   uint32
		wantOK     bool
	}{
		{
			name:     "keyword",
			tok:      token.Token{Kind: token.FN},
			wantType: semTypeIndex(t, "keyword"),
			wantOK:   true,
		},
		{
			name:     "operator",
			tok:      token.Token{Kind: token.PLUSEQ},
			wantType: semTypeIndex(t, "operator"),
			wantOK:   true,
		},
		{
			name:     "number",
			tok:      token.Token{Kind: token.INT},
			wantType: semTypeIndex(t, "number"),
			wantOK:   true,
		},
		{
			name:     "string",
			tok:      token.Token{Kind: token.RAWSTRING},
			wantType: semTypeIndex(t, "string"),
			wantOK:   true,
		},
		{
			name:       "resolved function ident",
			tok:        token.Token{Kind: token.IDENT, Pos: token.Pos{Offset: 10}},
			symbolKind: resolve.SymFn,
			wantType:   semTypeIndex(t, "function"),
			wantOK:     true,
		},
		{
			name:       "resolved parameter ident",
			tok:        token.Token{Kind: token.IDENT, Pos: token.Pos{Offset: 10}},
			symbolKind: resolve.SymParam,
			wantType:   semTypeIndex(t, "parameter"),
			wantOK:     true,
		},
		{
			name:     "unresolved ident defaults to variable",
			tok:      token.Token{Kind: token.IDENT, Pos: token.Pos{Offset: 10}},
			wantType: semTypeIndex(t, "variable"),
			wantOK:   true,
		},
		{
			name:   "punctuation skipped",
			tok:    token.Token{Kind: token.LPAREN},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := map[int]*resolve.Symbol{}
			if tt.symbolKind != resolve.SymUnknown {
				index[tt.tok.Pos.Offset] = &resolve.Symbol{Kind: tt.symbolKind}
			}
			got, ok := classifyToken(tt.tok, index)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got.ttype != tt.wantType {
				t.Fatalf("ttype = %d, want %d", got.ttype, tt.wantType)
			}
		})
	}
}

func TestEncodeSemTokensUsesSelfHostedDeltaEncoding(t *testing.T) {
	keyword := semTypeIndex(t, "keyword")
	function := semTypeIndex(t, "function")
	variable := semTypeIndex(t, "variable")
	got := encodeSemTokens([]semToken{
		{line: 2, col: 4, length: 6, ttype: keyword},
		{line: 0, col: 2, length: 3, ttype: function},
		{line: 0, col: 8, length: 1, ttype: variable},
	})
	want := []uint32{
		0, 2, 3, function, 0,
		0, 6, 1, variable, 0,
		2, 4, 6, keyword, 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encoded = %#v, want %#v", got, want)
	}
}
