package stdlib

import (
	"strings"
	"testing"
)

func TestCapabilityModuleExposesV06Protocols(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["capability"]
	if mod == nil {
		t.Fatal("stdlib capability module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"pub interface Clock",
		"pub interface Rng",
		"pub interface Env",
		"pub interface Fs",
		"pub interface Net",
		"pub interface Process",
		"pub interface Console",
		"pub fn canonicalNames() -> List<String>",
		"pub fn defaultAmbientNames() -> List<String>",
		"pub fn legacyGlobalModules() -> List<String>",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("capability module source missing %q", want)
		}
	}

	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"Clock", []string{"now", "monotonic", "sleep"}},
		{"Rng", []string{"next", "nextBytes", "int", "intInclusive", "float", "bool", "bytes"}},
		{"Env", []string{"args", "get", "require", "set", "unset", "vars", "currentDir", "setCurrentDir"}},
		{"Fs", []string{"read", "readToString", "write", "writeString", "exists", "walk", "glob", "create", "remove", "rename", "copy", "mkdir", "mkdirAll"}},
		{"Net", []string{"resolve", "resolveAll", "lookup", "lookupAddr", "connect", "connectTimeout", "listen"}},
		{"Process", []string{"pid", "hostname", "command", "commandArgs", "shell", "run", "exec"}},
		{"Console", []string{"print", "println", "eprint", "eprintln", "readLine", "writer"}},
	} {
		for _, method := range tc.methods {
			if got := reg.LookupMethodDecl("capability", tc.typeName, method); got == nil {
				t.Fatalf("LookupMethodDecl(capability, %s, %s) = nil", tc.typeName, method)
			}
		}
	}
}

func TestCapabilityModuleHelperNamesAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, name := range []string{
		"canonicalNames",
		"isCanonical",
		"defaultAmbientNames",
		"mainAmbientNames",
		"testAmbientNames",
		"filesystemEffectFns",
		"legacyGlobalModules",
	} {
		fn := reg.LookupFnDecl("capability", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(capability, %s) = nil", name)
		}
		if fn.Body == nil {
			t.Fatalf("capability.%s body = nil, want Osty helper body", name)
		}
	}
}
