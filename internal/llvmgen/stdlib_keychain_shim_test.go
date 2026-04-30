package llvmgen

import (
	"strings"
	"testing"
)

func TestStdKeychainRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.keychain as kc

fn main() {
    println(kc.backend())
    println(kc.isAvailable())
    match kc.get("svc", "acct") {
        Ok(secret) -> println(secret),
        Err(err) -> println(err.message()),
    }
    match kc.set("svc", "acct", "secret") {
        Ok(_) -> println("stored"),
        Err(err) -> println(err.message()),
    }
    match kc.delete("svc", "acct") {
        Ok(_) -> println("deleted"),
        Err(err) -> println(err.message()),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_keychain.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_keychain_backend()",
		"declare i1 @osty_rt_keychain_is_available()",
		"declare ptr @osty_rt_keychain_get(ptr, ptr)",
		"declare ptr @osty_rt_keychain_set(ptr, ptr, ptr)",
		"declare ptr @osty_rt_keychain_delete(ptr, ptr)",
		"declare void @osty_rt_os_string_result_free(ptr)",
		"call ptr @osty_rt_keychain_get(ptr",
		"call ptr @osty_rt_keychain_set(ptr",
		"call ptr @osty_rt_keychain_delete(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}
