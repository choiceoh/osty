package stdlib

import (
	"strings"
	"testing"
)

func TestCapabilityTestingModuleExposesFakes(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["capability.testing"]
	if mod == nil {
		t.Fatal("stdlib capability.testing module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"pub struct FakeClock",
		"pub struct FakeRng",
		"pub struct FakeEnv",
		"pub struct FakeFs",
		"pub struct FakeNet",
		"pub struct FakeProcess",
		"pub struct FakeConsole",
		"pub struct FakeRuntime",
		"pub fn runtime() -> FakeRuntime",
		"pub fn fakeNames() -> List<String>",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("capability.testing source missing %q", want)
		}
	}
}

func TestCapabilityTestingFakesExposeInterfaceMethods(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"FakeClock", []string{"now", "monotonic", "sleep", "set", "advance", "withElapsed", "rejectSleep"}},
		{"FakeRng", []string{"next", "nextBytes", "int", "intInclusive", "float", "bool", "bytes", "withBytes", "withFloat", "withBool"}},
		{"FakeEnv", []string{"args", "get", "require", "set", "unset", "vars", "currentDir", "setCurrentDir", "withArg", "withVar", "withCwd"}},
		{"FakeFs", []string{"read", "readToString", "write", "writeString", "exists", "walk", "glob", "create", "remove", "rename", "copy", "mkdir", "mkdirAll", "withFile", "withBytes", "withDir"}},
		{"FakeNet", []string{"resolve", "resolveAll", "lookup", "lookupAddr", "connect", "connectTimeout", "listen", "withAddr", "withLookup", "withReverse"}},
		{"FakeProcess", []string{"pid", "hostname", "command", "commandArgs", "shell", "run", "exec", "withPid", "withHostname", "withOutput", "withStdout", "withTimeout"}},
		{"FakeConsole", []string{"print", "println", "eprint", "eprintln", "readLine", "writer", "withInput", "stdoutText", "stderrText"}},
	} {
		for _, method := range tc.methods {
			if got := reg.LookupMethodDecl("capability.testing", tc.typeName, method); got == nil {
				t.Fatalf("LookupMethodDecl(capability.testing, %s, %s) = nil", tc.typeName, method)
			}
		}
	}
}

func TestCapabilityTestingFactoriesAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, name := range []string{
		"instant",
		"duration",
		"clock",
		"clockAt",
		"rng",
		"defaultRng",
		"env",
		"fs",
		"network",
		"process",
		"console",
		"runtime",
		"fakeNames",
	} {
		fn := reg.LookupFnDecl("capability.testing", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(capability.testing, %s) = nil", name)
		}
		if fn.Body == nil {
			t.Fatalf("capability.testing.%s body = nil, want Osty helper body", name)
		}
	}
}
