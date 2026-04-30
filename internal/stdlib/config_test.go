package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestConfigModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["config"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.config not loaded")
	}
	for _, name := range []string{
		"schema", "field", "requiredField", "fieldDefault", "fieldAllowed", "numberField",
		"parse", "read", "readAuto", "loadText", "load", "loadAuto", "inferFormat",
		"parseToml", "parseYaml", "parseIni", "parseEnv", "fromEnv",
		"mergeDefaults", "mergeLayers", "defaultsFromSchema", "applySchemaDefaults", "coerceWithSchema",
		"validate", "validateOrError", "get", "getString", "getBool", "getNumber",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.config missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.config.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.config.%s not public", name)
		}
	}
	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"ConfigValue", resolve.SymTypeAlias},
		{"ConfigFormat", resolve.SymEnum},
		{"ConfigType", resolve.SymEnum},
		{"ConfigField", resolve.SymStruct},
		{"ConfigSchema", resolve.SymStruct},
		{"ValidationIssue", resolve.SymStruct},
		{"ValidationResult", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.config missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.config.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.config.%s not public", tc.name)
		}
	}
}

func TestConfigModuleSourcePinsPracticalSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["config"]
	if mod == nil {
		t.Fatal("std.config module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn parseToml(text: String) -> Result<json.Json, Error>`,
		`pub fn parseYaml(text: String) -> Result<json.Json, Error>`,
		`pub fn parseIni(text: String) -> Result<json.Json, Error>`,
		`pub fn parseEnv(text: String) -> Result<json.Json, Error>`,
		`pub fn loadText(text: String, format: ConfigFormat, s: ConfigSchema) -> Result<json.Json, Error>`,
		`pub fn coerceWithSchema(value: json.Json, s: ConfigSchema) -> Result<json.Json, Error>`,
		`pub fn mergeDefaults(value: json.Json, defaults: json.Json) -> json.Json`,
		`pub fn validate(value: json.Json, s: ConfigSchema) -> ValidationResult`,
		`appendObjectArrayPath(root, section, lineNo)`,
		`setLastArrayObjectPathStrict(root, section, keyParts, value, lineNo)`,
		`levelIsArray(arrayLevels, level)`,
		`coerceBoolString(trimmed, f.path)`,
		`json.object(root)`,
		`parseEnvPath(name)`,
		`if !s.allowUnknown`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.config source missing %q", want)
		}
	}
}
