package llvmgen

import (
	"github.com/osty/osty/internal/mir"
)

func (g *mirGen) emitPtrResultFromNullableRuntimeCall(c *mir.CallInstr, symbol string, valueT mir.Type, args []mirRuntimeArg, errReg string) error {
	g.declareRuntime(symbol, mirRuntimeDeclareLine("ptr", symbol, mirRuntimeParamList(args)))
	out := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(out, "ptr", symbol, mirRuntimeArgList(args)))
	return g.emitPtrResultFromNullable(c, out, valueT, errReg)
}
