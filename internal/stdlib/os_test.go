package stdlib

import "testing"

func TestOsModuleExposesOutputConstructors(t *testing.T) {
	reg := LoadCached()
	for _, name := range []string{"output", "execOutput"} {
		fn := reg.LookupFnDecl("os", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(os, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("os.%s body = nil, want source constructor body", name)
		}
	}
}
