package llvmgen

import "github.com/osty/osty/internal/ast"

func (g *generator) emitPtrBackedResultFromRuntimeCall(prefix string, sourceType ast.Type, valueSymbol, errorSymbol string, params []paramInfo, args []*LlvmValue) (value, bool, error) {
	return g.emitStdFsPtrResultFromRuntimeCall(prefix, sourceType, valueSymbol, errorSymbol, params, args)
}
