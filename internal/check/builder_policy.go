package check

import "github.com/osty/osty/internal/ast"

// builderDeriveInfo mirrors the shared Osty-side policy in
// `toolchain/builder_policy.osty::checkClassifyBuilderDerive`.
// Keep the field/default/method override rules in lockstep until the
// full builder chain rewrite moves behind a generated selfhost bridge.
type builderDeriveInfo struct {
	Derivable bool
	Required  []string
}

func classifyBuilderDerive(sd *ast.StructDecl) builderDeriveInfo {
	if sd == nil {
		return builderDeriveInfo{}
	}
	fieldNames := make([]string, 0, len(sd.Fields))
	fieldExported := make([]bool, 0, len(sd.Fields))
	fieldHasDefaults := make([]bool, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		if f == nil {
			continue
		}
		fieldNames = append(fieldNames, f.Name)
		fieldExported = append(fieldExported, f.Pub)
		fieldHasDefaults = append(fieldHasDefaults, f.Default != nil)
	}
	methodNames := make([]string, 0, len(sd.Methods))
	for _, m := range sd.Methods {
		if m == nil {
			continue
		}
		methodNames = append(methodNames, m.Name)
	}
	return classifyBuilderDeriveLists(fieldNames, fieldExported, fieldHasDefaults, methodNames)
}

func classifyBuilderDeriveLists(
	fieldNames []string,
	fieldExported []bool,
	fieldHasDefaults []bool,
	methodNames []string,
) builderDeriveInfo {
	if len(fieldNames) != len(fieldExported) || len(fieldNames) != len(fieldHasDefaults) {
		return builderDeriveInfo{}
	}
	required := make([]string, 0, len(fieldNames))
	derivable := true
	for i, name := range fieldNames {
		isPub := fieldExported[i]
		hasDefault := fieldHasDefaults[i]
		if !isPub && !hasDefault {
			derivable = false
		}
		if isPub && !hasDefault {
			required = append(required, name)
		}
	}
	for _, name := range methodNames {
		if name == "builder" {
			derivable = false
			break
		}
	}
	return builderDeriveInfo{
		Derivable: derivable,
		Required:  required,
	}
}
