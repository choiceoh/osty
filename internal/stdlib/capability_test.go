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
		"pub struct MigrationRule",
		"pub struct HostNet",
		"pub struct HostProcess",
		"pub fn canonicalNames() -> List<String>",
		"pub fn defaultAmbientNames() -> List<String>",
		"pub fn hostFactoryNames() -> List<String>",
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
		"hostFactoryNames",
		"bridgeFactoryNames",
		"deterministicCapabilityNames",
		"nondeterministicCapabilityNames",
		"filesystemEffectFns",
		"legacyGlobalModules",
		"legacyGlobalRewriteRules",
		"hostNet",
		"hostProcess",
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

func TestCapabilityHostAdaptersExposeInterfaceMethods(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		module   string
		typeName string
		methods  []string
	}{
		{"time", "HostClock", []string{"now", "monotonic", "sleep"}},
		{"env", "HostEnv", []string{"args", "get", "require", "set", "unset", "vars", "currentDir", "setCurrentDir"}},
		{"fs", "HostFs", []string{"read", "readToString", "write", "writeString", "exists", "walk", "glob", "create", "remove", "rename", "copy", "mkdir", "mkdirAll"}},
		{"net", "HostNet", []string{"resolve", "resolveAll", "lookup", "lookupAddr", "connect", "connectTimeout", "listen"}},
		{"process", "HostProcess", []string{"pid", "hostname", "command", "commandArgs", "shell", "run", "exec"}},
		{"io", "HostConsole", []string{"print", "println", "eprint", "eprintln", "readLine", "writer"}},
		{"io", "ConsoleWriter", []string{"write", "flush"}},
		{"capability", "HostNet", []string{"resolve", "resolveAll", "lookup", "lookupAddr", "connect", "connectTimeout", "listen"}},
		{"capability", "HostProcess", []string{"pid", "hostname", "command", "commandArgs", "shell", "run", "exec"}},
	} {
		for _, method := range tc.methods {
			if got := reg.LookupMethodDecl(tc.module, tc.typeName, method); got == nil {
				t.Fatalf("LookupMethodDecl(%s, %s, %s) = nil", tc.module, tc.typeName, method)
			}
		}
	}

	for _, tc := range []struct {
		module string
		name   string
	}{
		{"time", "systemClock"},
		{"random", "host"},
		{"random", "seededCapability"},
		{"env", "host"},
		{"fs", "host"},
		{"net", "host"},
		{"process", "host"},
		{"io", "console"},
		{"io", "stdoutWriter"},
		{"io", "stderrWriter"},
		{"capability", "hostNet"},
		{"capability", "hostProcess"},
	} {
		if got := reg.LookupFnDecl(tc.module, tc.name); got == nil {
			t.Fatalf("LookupFnDecl(%s, %s) = nil", tc.module, tc.name)
		}
	}
}
