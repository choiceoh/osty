package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateOptionalFieldAccess(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Profile {
    name: String
}

fn maybeName(profile: Profile?) -> String? {
    profile?.name
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/optional_field.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @maybeName(ptr %profile)",
		"icmp eq ptr",
		"load %Profile, ptr %profile",
		"extractvalue %Profile",
		"phi ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOptionalMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Profile {
    name: String,

    pub fn display(self) -> String {
        self.name
    }
}

fn maybeDisplay(profile: Profile?) -> String? {
    profile?.display()
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/optional_method.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @maybeDisplay(ptr %profile)",
		"call ptr @Profile__display(%Profile",
		"phi ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOptionalRuntimeFFIAliasCall(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn parts() -> List<String> {
    strings?.Split("osty,llvm", ",")
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/optional_runtime_ffi.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"call ptr @osty_rt_strings_Split",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOptionalQuestionExpr(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Profile {
    name: String
}

fn requireName(profile: Profile?) -> String? {
    let value = profile?
    value.name
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/optional_question.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @requireName(ptr %profile)",
		"ret ptr null",
		"load %Profile, ptr %profile",
		"extractvalue %Profile",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateMethodSelfFieldMapGetCoalesceKeepsSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `struct RuntimeDecls {
    signatures: Map<String, String>

    pub fn signature(self, name: String) -> String {
        self.signatures.get(name) ?? ""
    }
}

fn read(decls: RuntimeDecls) -> String {
    decls.signature("osty")
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/self_field_map_get_coalesce.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @RuntimeDecls__signature",
		"call i1 @osty_rt_map_get_string",
		"phi ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOptionUnwrapOrStringIntAndStruct(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Profile {
    name: String
}

fn text(name: String?) -> String {
    name.unwrapOr("")
}

fn score(n: Int?) -> Int {
    n.unwrapOr(7)
}

fn profileName(profile: Profile?) -> String {
    profile.unwrapOr(Profile { name: "anon" }).name
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/option_unwrap_or.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @text(ptr %name)",
		"define i64 @score(ptr %n)",
		"define ptr @profileName(ptr %profile)",
		"icmp eq ptr",
		"phi ptr",
		"phi i64",
		"phi %Profile",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateResultUnwrapOrStringAndInt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn decodeText() -> String {
    "ab".toBytes().toString().unwrapOr("")
}

fn score(ok: Result<Int, String>) -> Int {
    ok.unwrapOr(7)
}

fn check(ok: Result<Int, String>) -> Bool {
    ok.isOk() && !ok.isErr()
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/result_unwrap_or.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @decodeText()",
		"define i64 @score(%Result.i64.ptr %ok)",
		"define i1 @check(%Result.i64.ptr %ok)",
		"@osty_rt_bytes_to_string",
		"extractvalue %Result.ptr.ptr",
		"phi ptr",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateResultQuestionExprInt covers `?` on Result<T, E> where the
// enclosing function returns Result<_, E> with the same error type.
// Expected shape: extract tag → icmp eq against 1 (Err) → branch →
// Err branch repackages err into the return Result and returns; Ok
// branch extracts field 1 and continues.
func TestGenerateResultQuestionExprInt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn halve(n: Int) -> Result<Int, String> {
    if n == 0 {
        Err("zero")
    } else {
        Ok(n / 2)
    }
}

fn chain(n: Int) -> Result<Int, String> {
    let a = halve(n)?
    Ok(a)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/result_question.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define %Result.i64.ptr @chain(i64 %n)",
		"extractvalue %Result.i64.ptr",
		"icmp eq i64",
		"result.err",
		"result.ok",
		// Err repackage: tag=1, ok=zero, err=forwarded
		"insertvalue %Result.i64.ptr undef, i64 1, 0",
		"ret %Result.i64.ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateResultMatchExprInt covers `match r { Ok(v) -> ..., Err(_) -> ... }`
// on a Result<Int, String> value — the Osty source pattern used by
// semver_parse.osty's top-level consumer of `?`.
func TestGenerateResultMatchExprInt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn halve(n: Int) -> Result<Int, String> {
    if n == 0 {
        Err("zero")
    } else {
        Ok(n / 2)
    }
}

fn main() {
    println(match halve(84) {
        Ok(v) -> v,
        Err(_) -> 0,
    })
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/result_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Result.i64.ptr = type { i64, i64, ptr }",
		"call %Result.i64.ptr @halve",
		"extractvalue %Result.i64.ptr",
		"icmp eq i64",
		// phi merges arm values back into i64
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateResultMatchFnValuePayloadIndirectCall(t *testing.T) {
	file := parseLLVMGenFile(t, `type Callback = fn(Int) -> Int

fn inc(n: Int) -> Int {
    n + 1
}

fn fallback(n: Int) -> Int {
    n - 1
}

fn choose(flag: Int) -> Result<Callback, String> {
    if flag == 1 {
        Ok(inc)
    } else {
        Err("missing")
    }
}

fn main() {
    let out = match choose(1) {
        Ok(f) -> f(41),
        Err(_) -> fallback(41),
    }
    println(out)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/result_match_fn_value.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Result.ptr.ptr = type { i64, ptr, ptr }",
		"define private i64 @__osty_closure_thunk_inc(ptr %env, i64 %arg0)",
		"= extractvalue %Result.ptr.ptr",
		"= call i64 (ptr, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "LLVM015") {
		t.Fatalf("result-match fn payload regressed to direct-call fallback:\n%s", got)
	}
}
