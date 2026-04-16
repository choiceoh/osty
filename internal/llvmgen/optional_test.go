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

	ir, err := Generate(file, Options{
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

	ir, err := Generate(file, Options{
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

	ir, err := Generate(file, Options{
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

	ir, err := Generate(file, Options{
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
