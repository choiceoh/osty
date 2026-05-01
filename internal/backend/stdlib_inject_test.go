package backend

import (
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/stdlib"
)

func TestReachableStdlibFnsEmpty(t *testing.T) {
	if got := ReachableStdlibFns(nil, nil); got != nil {
		t.Fatalf("both nil = %v, want nil", got)
	}
	reg := stdlib.LoadCached()
	if got := ReachableStdlibFns(nil, reg); got != nil {
		t.Fatalf("nil module = %v, want nil", got)
	}
	mod := &ir.Module{Package: "main"}
	if got := ReachableStdlibFns(mod, nil); got != nil {
		t.Fatalf("nil registry = %v, want nil", got)
	}
	if got := ReachableStdlibFns(mod, reg); len(got) != 0 {
		t.Fatalf("empty module = %v, want empty", got)
	}
}

func TestReachableStdlibFnsFindsStringsCompare(t *testing.T) {
	reg := stdlib.LoadCached()
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.CallExpr{
				Callee: &ir.FieldExpr{X: &ir.Ident{Name: "strings"}, Name: "compare"},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "a"}},
					{Value: &ir.Ident{Name: "b"}},
				},
			}},
		},
	}
	got := ReachableStdlibFns(mod, reg)
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1", len(got), got)
	}
	if got[0].Module != "strings" || got[0].Fn.Name != "compare" {
		t.Fatalf("got[0] = {%s, %s}, want {strings, compare}", got[0].Module, got[0].Fn.Name)
	}
	if got[0].Fn.Body == nil {
		t.Fatalf("got[0].Fn.Body = nil, want body of stdlib fn")
	}
}

func TestReachableStdlibFnsSkipsUnknownQualifier(t *testing.T) {
	reg := stdlib.LoadCached()
	// `userAlias.helper(x)` is not a stdlib call.
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.CallExpr{
				Callee: &ir.FieldExpr{X: &ir.Ident{Name: "userAlias"}, Name: "helper"},
				Args:   []ir.Arg{{Value: &ir.Ident{Name: "x"}}},
			}},
		},
	}
	if got := ReachableStdlibFns(mod, reg); len(got) != 0 {
		t.Fatalf("got %v, want no stdlib hits for a user-alias call", got)
	}
}

func TestReachableStdlibMethodsEmpty(t *testing.T) {
	if got := ReachableStdlibMethods(nil, nil); got != nil {
		t.Fatalf("both nil = %v, want nil", got)
	}
	reg := stdlib.LoadCached()
	if got := ReachableStdlibMethods(nil, reg); got != nil {
		t.Fatalf("nil module = %v, want nil", got)
	}
	mod := &ir.Module{Package: "main"}
	if got := ReachableStdlibMethods(mod, nil); got != nil {
		t.Fatalf("nil registry = %v, want nil", got)
	}
	if got := ReachableStdlibMethods(mod, reg); len(got) != 0 {
		t.Fatalf("empty module = %v, want empty", got)
	}
}

func TestReachableStdlibMethodsFindsHexEncode(t *testing.T) {
	reg := stdlib.LoadCached()
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "h", T: hexT},
				Name:     "encode",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "data"}}},
			}},
		},
	}
	got := ReachableStdlibMethods(mod, reg)
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1", len(got), got)
	}
	if got[0].Module != "encoding" || got[0].Type != "Hex" || got[0].Method != "encode" {
		t.Fatalf("got[0] = {%s, %s, %s}, want {encoding, Hex, encode}",
			got[0].Module, got[0].Type, got[0].Method)
	}
	if got[0].Fn == nil || got[0].Fn.Name != "encode" {
		t.Fatalf("got[0].Fn = %v, want encode method AST", got[0].Fn)
	}
}

func TestReachableStdlibMethodsFindsEncodingSingletonMethod(t *testing.T) {
	reg := stdlib.LoadCached()
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.FieldExpr{X: &ir.Ident{Name: "encoding"}, Name: "base64"},
				Name:     "encode",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "data"}}},
			}},
			&ir.ExprStmt{X: &ir.CallExpr{
				Callee: &ir.FieldExpr{
					X: &ir.FieldExpr{
						X:    &ir.FieldExpr{X: &ir.Ident{Name: "encoding"}, Name: "base64"},
						Name: "url",
					},
					Name: "decode",
				},
				Args: []ir.Arg{{Value: &ir.Ident{Name: "text"}}},
			}},
		},
	}
	got := ReachableStdlibMethods(mod, reg)
	if len(got) != 2 {
		t.Fatalf("got %d entries (%v), want 2", len(got), got)
	}
	if got[0].Module != "encoding" || got[0].Type != "Base64" || got[0].Method != "encode" || got[0].ValuePath != "base64" {
		t.Fatalf("got[0] = {%s, %s, %s, %s}, want {encoding, Base64, encode, base64}",
			got[0].Module, got[0].Type, got[0].Method, got[0].ValuePath)
	}
	if got[1].Module != "encoding" || got[1].Type != "Base64Url" || got[1].Method != "decode" || got[1].ValuePath != "base64.url" {
		t.Fatalf("got[1] = {%s, %s, %s, %s}, want {encoding, Base64Url, decode, base64.url}",
			got[1].Module, got[1].Type, got[1].Method, got[1].ValuePath)
	}
}

func TestReachableStdlibValuePathReceiversMatchBackendSpecialCases(t *testing.T) {
	reg := stdlib.LoadCached()
	cases := []struct {
		name      string
		module    string
		path      []string
		valuePath string
		method    string
		typeName  string
		check     func(*testing.T, *ir.StructLit)
	}{
		{
			name:      "encoding base64",
			module:    "encoding",
			path:      []string{"base64"},
			valuePath: "base64",
			method:    "encode",
			typeName:  "Base64",
			check: func(t *testing.T, st *ir.StructLit) {
				url := requireStructField(t, st, "url")
				requireStructReceiver(t, url, "encoding", "Base64Url")
			},
		},
		{name: "encoding base64 url", module: "encoding", path: []string{"base64", "url"}, valuePath: "base64.url", method: "encode", typeName: "Base64Url"},
		{name: "encoding hex", module: "encoding", path: []string{"hex"}, valuePath: "hex", method: "encode", typeName: "Hex"},
		{name: "encoding url", module: "encoding", path: []string{"url"}, valuePath: "url", method: "encode", typeName: "UrlEncoding"},
		{name: "compress gzip", module: "compress", path: []string{"gzip"}, valuePath: "gzip", method: "encode", typeName: "Gzip"},
		{name: "crypto hmac", module: "crypto", path: []string{"hmac"}, valuePath: "hmac", method: "sha256", typeName: "Hmac"},
		{
			name:      "net localhost v4",
			module:    "net",
			path:      []string{"LOCALHOST_V4"},
			valuePath: "LOCALHOST_V4",
			method:    "toString",
			typeName:  "Ipv4Addr",
			check: func(t *testing.T, st *ir.StructLit) {
				requireIntField(t, st, "a", "127")
				requireIntField(t, st, "b", "0")
				requireIntField(t, st, "c", "0")
				requireIntField(t, st, "d", "1")
			},
		},
		{
			name:      "net unspecified v6",
			module:    "net",
			path:      []string{"UNSPECIFIED_V6"},
			valuePath: "UNSPECIFIED_V6",
			method:    "toString",
			typeName:  "Ipv6Addr",
			check: func(t *testing.T, st *ir.StructLit) {
				requireIntListField(t, st, "groups", []string{"0", "0", "0", "0", "0", "0", "0", "0"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt := &ir.ExprStmt{X: &ir.MethodCall{
				Receiver: stdlibPathExprForTest(tc.module, tc.path),
				Name:     tc.method,
			}}
			mod := &ir.Module{Package: "main", Script: []ir.Stmt{stmt}}
			reached := ReachableStdlibMethods(mod, reg)
			if len(reached) != 1 {
				t.Fatalf("ReachableStdlibMethods = %d entries (%v), want 1", len(reached), reached)
			}
			if reached[0].Module != tc.module || reached[0].Type != tc.typeName ||
				reached[0].Method != tc.method || reached[0].ValuePath != tc.valuePath {
				t.Fatalf("reached[0] = {%s, %s, %s, %s}, want {%s, %s, %s, %s}",
					reached[0].Module, reached[0].Type, reached[0].Method, reached[0].ValuePath,
					tc.module, tc.typeName, tc.method, tc.valuePath)
			}
			if got := RewriteStdlibMethodCallsites(mod, reached); got != 1 {
				t.Fatalf("RewriteStdlibMethodCallsites rewrote %d callsites, want 1", got)
			}
			call, ok := stmt.X.(*ir.CallExpr)
			if !ok {
				t.Fatalf("rewritten stmt.X = %T, want *ir.CallExpr", stmt.X)
			}
			if len(call.Args) == 0 || call.Args[0].Value == nil {
				t.Fatalf("rewritten call args = %#v, want receiver prepended", call.Args)
			}
			receiver := requireStructReceiver(t, call.Args[0].Value, tc.module, tc.typeName)
			if tc.check != nil {
				tc.check(t, receiver)
			}
		})
	}
}

func TestReachableStdlibMethodsKeepsSingletonPathWhenTypedReceiverAlsoReached(t *testing.T) {
	reg := stdlib.LoadCached()
	b64T := &ir.NamedType{Package: "encoding", Name: "Base64"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "b", T: b64T},
				Name:     "encode",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "data"}}},
			}},
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.FieldExpr{X: &ir.Ident{Name: "encoding"}, Name: "base64"},
				Name:     "encode",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "data"}}},
			}},
		},
	}
	got := ReachableStdlibMethods(mod, reg)
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1 deduped Base64.encode entry", len(got), got)
	}
	if got[0].Module != "encoding" || got[0].Type != "Base64" || got[0].Method != "encode" {
		t.Fatalf("got[0] = {%s, %s, %s}, want {encoding, Base64, encode}",
			got[0].Module, got[0].Type, got[0].Method)
	}
	if got[0].ValuePath != "base64" {
		t.Fatalf("got[0].ValuePath = %q, want base64", got[0].ValuePath)
	}
}

func stdlibPathExprForTest(module string, path []string) ir.Expr {
	var expr ir.Expr = &ir.Ident{Name: module}
	for _, part := range path {
		expr = &ir.FieldExpr{X: expr, Name: part}
	}
	return expr
}

func requireStructReceiver(t *testing.T, expr ir.Expr, module, typeName string) *ir.StructLit {
	t.Helper()
	st, ok := expr.(*ir.StructLit)
	if !ok {
		t.Fatalf("receiver = %T, want *ir.StructLit", expr)
	}
	if st.TypeName != typeName {
		t.Fatalf("receiver TypeName = %q, want %q", st.TypeName, typeName)
	}
	named, ok := st.T.(*ir.NamedType)
	if !ok || named.Package != module || named.Name != typeName {
		t.Fatalf("receiver type = %#v, want %s.%s", st.T, module, typeName)
	}
	return st
}

func requireStructField(t *testing.T, st *ir.StructLit, name string) ir.Expr {
	t.Helper()
	for _, field := range st.Fields {
		if field.Name == name {
			if field.Value == nil {
				t.Fatalf("field %s has nil value", name)
			}
			return field.Value
		}
	}
	t.Fatalf("field %s missing from %#v", name, st.Fields)
	return nil
}

func requireIntField(t *testing.T, st *ir.StructLit, name, text string) {
	t.Helper()
	value := requireStructField(t, st, name)
	lit, ok := value.(*ir.IntLit)
	if !ok || lit.Text != text || lit.T != ir.TInt {
		t.Fatalf("field %s = %#v, want IntLit(%s)", name, value, text)
	}
}

func requireIntListField(t *testing.T, st *ir.StructLit, name string, want []string) {
	t.Helper()
	value := requireStructField(t, st, name)
	list, ok := value.(*ir.ListLit)
	if !ok || list.Elem != ir.TInt {
		t.Fatalf("field %s = %#v, want List<Int>", name, value)
	}
	if len(list.Elems) != len(want) {
		t.Fatalf("field %s len = %d, want %d", name, len(list.Elems), len(want))
	}
	for i, elem := range list.Elems {
		lit, ok := elem.(*ir.IntLit)
		if !ok || lit.Text != want[i] || lit.T != ir.TInt {
			t.Fatalf("field %s[%d] = %#v, want IntLit(%s)", name, i, elem, want[i])
		}
	}
}

func TestReachableStdlibMethodsSkipsUnknownMethod(t *testing.T) {
	reg := stdlib.LoadCached()
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "h", T: hexT},
				Name:     "noSuchMethodOnHex",
			}},
		},
	}
	if got := ReachableStdlibMethods(mod, reg); len(got) != 0 {
		t.Fatalf("got %v, want no hit for unknown method", got)
	}
}

func TestReachableStdlibMethodsSkipsUserType(t *testing.T) {
	// ReachMethods already filters empty Package; this verifies that
	// ReachableStdlibMethods preserves that guarantee end-to-end.
	reg := stdlib.LoadCached()
	userT := &ir.NamedType{Package: "", Name: "MyStruct"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "v", T: userT},
				Name:     "encode",
			}},
		},
	}
	if got := ReachableStdlibMethods(mod, reg); len(got) != 0 {
		t.Fatalf("got %v, want no hit for user type method", got)
	}
}

func TestReachableStdlibMethodsDeterministicOrder(t *testing.T) {
	reg := stdlib.LoadCached()
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	b64T := &ir.NamedType{Package: "encoding", Name: "Base64"}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "b", T: b64T},
				Name:     "encode",
			}},
			&ir.ExprStmt{X: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "h", T: hexT},
				Name:     "encode",
			}},
		},
	}
	got := ReachableStdlibMethods(mod, reg)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
	// Sort order: (module, type, method). Both in encoding, so types
	// decide: Base64 < Hex.
	if got[0].Type != "Base64" || got[1].Type != "Hex" {
		t.Fatalf("got order %q, %q; want Base64 before Hex", got[0].Type, got[1].Type)
	}
}

func TestReachableStdlibFnsDeterministicOrder(t *testing.T) {
	reg := stdlib.LoadCached()
	mk := func(mod, name string) ir.Stmt {
		return &ir.ExprStmt{X: &ir.CallExpr{
			Callee: &ir.FieldExpr{X: &ir.Ident{Name: mod}, Name: name},
		}}
	}
	mod := &ir.Module{
		Package: "main",
		Script: []ir.Stmt{
			mk("strings", "compare"),
			mk("strings", "compareCI"),
		},
	}
	got := ReachableStdlibFns(mod, reg)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
	if got[0].Fn.Name > got[1].Fn.Name {
		t.Fatalf("got out of order: %q before %q", got[0].Fn.Name, got[1].Fn.Name)
	}
}
