package check

import "github.com/osty/osty/internal/ast"

// BuilderDeriveInfo mirrors the shared Osty-side policy in
// `toolchain/builder_policy.osty::checkClassifyBuilderDerive`.
// The selfhost checker owns builder typing/diagnostics; this Go mirror
// is the shared adapter for host-side IR metadata and lowering.
type BuilderDeriveInfo struct {
	Derivable bool
	Required  []string
}

func ClassifyBuilderDerive(sd *ast.StructDecl) BuilderDeriveInfo {
	if sd == nil {
		return BuilderDeriveInfo{}
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
		if m == nil || m.Recv != nil {
			continue
		}
		methodNames = append(methodNames, m.Name)
	}
	return ClassifyBuilderDeriveLists(fieldNames, fieldExported, fieldHasDefaults, methodNames)
}

func ClassifyBuilderDeriveLists(
	fieldNames []string,
	fieldExported []bool,
	fieldHasDefaults []bool,
	methodNames []string,
) BuilderDeriveInfo {
	if len(fieldNames) != len(fieldExported) || len(fieldNames) != len(fieldHasDefaults) {
		return BuilderDeriveInfo{}
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
	return BuilderDeriveInfo{
		Derivable: derivable,
		Required:  required,
	}
}
