package lsp

import (
	"reflect"
	"testing"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

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
			wantType: semTypeKeyword,
			wantOK:   true,
		},
		{
			name:     "operator",
			tok:      token.Token{Kind: token.PLUSEQ},
			wantType: semTypeOperator,
			wantOK:   true,
		},
		{
			name:     "number",
			tok:      token.Token{Kind: token.INT},
			wantType: semTypeNumber,
			wantOK:   true,
		},
		{
			name:     "string",
			tok:      token.Token{Kind: token.RAWSTRING},
			wantType: semTypeString,
			wantOK:   true,
		},
		{
			name:       "resolved function ident",
			tok:        token.Token{Kind: token.IDENT, Pos: token.Pos{Offset: 10}},
			symbolKind: resolve.SymFn,
			wantType:   semTypeFunction,
			wantOK:     true,
		},
		{
			name:       "resolved parameter ident",
			tok:        token.Token{Kind: token.IDENT, Pos: token.Pos{Offset: 10}},
			symbolKind: resolve.SymParam,
			wantType:   semTypeParameter,
			wantOK:     true,
		},
		{
			name:     "unresolved ident defaults to variable",
			tok:      token.Token{Kind: token.IDENT, Pos: token.Pos{Offset: 10}},
			wantType: semTypeVariable,
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
	got := encodeSemTokens([]semToken{
		{line: 2, col: 4, length: 6, ttype: semTypeKeyword},
		{line: 0, col: 2, length: 3, ttype: semTypeFunction},
		{line: 0, col: 8, length: 1, ttype: semTypeVariable},
	})
	want := []uint32{
		0, 2, 3, semTypeFunction, 0,
		0, 6, 1, semTypeVariable, 0,
		2, 4, 6, semTypeKeyword, 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encoded = %#v, want %#v", got, want)
	}
}
