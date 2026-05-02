package stdlib

import (
	"strings"
	"testing"
)

func TestLogModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["log"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.log not loaded")
	}

	for _, name := range []string{
		"debug", "info", "warn", "error",
		"setLevel", "setHandler",
		"newLogger", "newDefaultLogger",
		"fields",
		"debugLevel", "infoLevel", "warnLevel", "errorLevel",
		"nullValue", "boolValue", "intValue", "floatValue",
		"stringValue", "bytesValue", "listValue", "mapValue",
		"timeValue", "durationValue",
	} {
		requirePublicFn(t, mod, "log", name)
	}

	for _, name := range []string{
		"Level", "LogValue", "Record",
		"Handler", "ToLogValue",
		"TextHandler", "JsonHandler", "LevelHandler",
		"Logger",
		"Fields", "LogValueList",
	} {
		requirePublicType(t, mod, "log", name)
	}

	for _, method := range []string{"handle"} {
		if got := reg.LookupMethodDecl("log", "TextHandler", method); got == nil {
			t.Fatalf("LookupMethodDecl(log, TextHandler, %s) = nil", method)
		}
		if got := reg.LookupMethodDecl("log", "JsonHandler", method); got == nil {
			t.Fatalf("LookupMethodDecl(log, JsonHandler, %s) = nil", method)
		}
		if got := reg.LookupMethodDecl("log", "LevelHandler", method); got == nil {
			t.Fatalf("LookupMethodDecl(log, LevelHandler, %s) = nil", method)
		}
	}

	for _, method := range []string{"debug", "info", "warn", "error", "with", "withFields"} {
		if got := reg.LookupMethodDecl("log", "Logger", method); got == nil {
			t.Fatalf("LookupMethodDecl(log, Logger, %s) = nil", method)
		}
	}
}

func TestLogModuleRedundantAliasesRemoved(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["log"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.log not loaded")
	}

	for _, name := range []string{"LogBool", "LogInt", "LogFloat", "LogString", "LogBytes"} {
		if got := mod.Package.PkgScope.Lookup(name); got != nil {
			t.Errorf("std.log still exposes redundant alias %q (1:1 with primitive)", name)
		}
	}
}

func TestLogModuleRecognizesTimeTypes(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["log"]
	if mod == nil {
		t.Fatalf("std.log not loaded")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"Time(time.Instant)",
		"Duration(time.Duration)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("std.log enum LogValue missing %q variant\nsource:\n%s", want, src)
		}
	}
}
