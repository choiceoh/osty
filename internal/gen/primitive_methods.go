package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

func (g *gen) emitPrimitiveMethodCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	p, ok := g.typeOf(f.X).(*types.Primitive)
	if !ok {
		return false
	}
	switch p.Kind {
	case types.PString:
		return g.emitStringMethod(c, f)
	case types.PChar:
		return g.emitCharMethod(c, f)
	case types.PInt, types.PInt8, types.PInt16, types.PInt32, types.PInt64,
		types.PUInt8, types.PUInt16, types.PUInt32, types.PUInt64, types.PByte:
		return g.emitIntegerMethod(c, f)
	case types.PFloat, types.PFloat32, types.PFloat64:
		return g.emitFloatMethod(c, f)
	case types.PBool:
		return g.emitBoolMethod(c, f)
	}
	return false
}

func (g *gen) emitStringMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	switch f.Name {
	case "len":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("len(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isEmpty":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("(len(")
		g.emitExpr(f.X)
		g.body.write(") == 0)")
	case "charCount":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("len([]rune(")
		g.emitExpr(f.X)
		g.body.write("))")
	case "get":
		if len(c.Args) != 1 {
			return false
		}
		g.emitStringGet(f.X, c.Args[0].Value)
	case "chars":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("[]rune(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "bytes", "toBytes":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("[]byte(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "graphemes":
		if len(c.Args) != 0 {
			return false
		}
		g.emitStringGraphemes(f.X)
	case "toString":
		if len(c.Args) != 0 {
			return false
		}
		g.emitExpr(f.X)
	case "toInt":
		if len(c.Args) != 0 {
			return false
		}
		g.needResult = true
		g.use("strconv")
		g.body.write("func() Result[int, any] { v, err := strconv.Atoi(")
		g.emitExpr(f.X)
		g.body.write("); if err != nil { return resultErr[int, any](err) }; return resultOk[int, any](v) }()")
	case "toFloat":
		if len(c.Args) != 0 {
			return false
		}
		g.needResult = true
		g.use("strconv")
		g.body.write("func() Result[float64, any] { v, err := strconv.ParseFloat(")
		g.emitExpr(f.X)
		g.body.write(", 64); if err != nil { return resultErr[float64, any](err) }; return resultOk[float64, any](v) }()")
	default:
		return false
	}
	return true
}

func (g *gen) emitStringGet(recv ast.Expr, index ast.Expr) {
	g.body.write("func() *byte { s := ")
	g.emitExpr(recv)
	g.body.write("; i := ")
	g.emitExpr(index)
	g.body.write("; if i < 0 || i >= len(s) { return nil }; v := s[i]; return &v }()")
}

func (g *gen) emitStringGraphemes(recv ast.Expr) {
	g.requestStdlibOsty("strings")
	g.body.write(stdlibOstyFuncName("strings", "graphemes"))
	g.body.write("(")
	g.emitExpr(recv)
	g.body.write(")")
}

func (g *gen) emitCharMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if len(c.Args) != 0 {
		return false
	}
	switch f.Name {
	case "toInt":
		g.body.write("int(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toString":
		g.body.write("string(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isDigit":
		g.use("unicode")
		g.body.write("unicode.IsDigit(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isAlpha":
		g.use("unicode")
		g.body.write("unicode.IsLetter(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isAlphanumeric":
		g.use("unicode")
		g.body.write("func() bool { r := ")
		g.emitExpr(f.X)
		g.body.write("; return unicode.IsLetter(r) || unicode.IsDigit(r) }()")
	case "isWhitespace":
		g.use("unicode")
		g.body.write("unicode.IsSpace(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isUpper":
		g.use("unicode")
		g.body.write("unicode.IsUpper(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isLower":
		g.use("unicode")
		g.body.write("unicode.IsLower(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toUpper":
		g.use("unicode")
		g.body.write("unicode.ToUpper(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toLower":
		g.use("unicode")
		g.body.write("unicode.ToLower(")
		g.emitExpr(f.X)
		g.body.write(")")
	default:
		return false
	}
	return true
}

func (g *gen) emitIntegerMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if len(c.Args) != 0 {
		return false
	}
	switch f.Name {
	case "toString":
		g.use("strconv")
		g.body.write("strconv.Itoa(int(")
		g.emitExpr(f.X)
		g.body.write("))")
	case "toFloat":
		g.body.write("float64(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toChar":
		g.body.write("rune(")
		g.emitExpr(f.X)
		g.body.write(")")
	default:
		return false
	}
	return true
}

func (g *gen) emitFloatMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if len(c.Args) != 0 {
		return false
	}
	switch f.Name {
	case "toString":
		g.use("strconv")
		g.body.write("strconv.FormatFloat(float64(")
		g.emitExpr(f.X)
		g.body.write("), 'g', -1, 64)")
	case "toInt":
		g.body.write("int(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "abs":
		g.use("math")
		g.body.write("math.Abs(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "round":
		g.use("math")
		g.body.write("math.Round(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "floor":
		g.use("math")
		g.body.write("math.Floor(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "ceil":
		g.use("math")
		g.body.write("math.Ceil(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "trunc":
		g.use("math")
		g.body.write("math.Trunc(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isNaN":
		g.use("math")
		g.body.write("math.IsNaN(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isInfinite":
		g.use("math")
		g.body.write("math.IsInf(")
		g.emitExpr(f.X)
		g.body.write(", 0)")
	default:
		return false
	}
	return true
}

func (g *gen) emitBoolMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if len(c.Args) != 0 || f.Name != "toString" {
		return false
	}
	g.use("strconv")
	g.body.write("strconv.FormatBool(")
	g.emitExpr(f.X)
	g.body.write(")")
	return true
}
