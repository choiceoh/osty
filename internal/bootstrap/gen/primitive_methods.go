package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

func (g *gen) emitPrimitiveMethodCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if f.IsOptional {
		return false
	}
	p, ok := g.primitiveReceiver(f.X)
	if !ok {
		return false
	}
	switch {
	case p.Kind == types.PBool:
		return g.emitBoolMethod(c, f)
	case p.Kind == types.PChar:
		return g.emitCharMethod(c, f)
	case p.Kind == types.PString:
		return g.emitStringMethod(c, f)
	case p.Kind == types.PBytes:
		return g.emitBytesMethod(c, f)
	case p.Kind.IsInteger():
		return g.emitIntMethod(c, f, p.Kind)
	case p.Kind.IsFloat():
		return g.emitFloatMethod(c, f, p.Kind)
	}
	return false
}

func (g *gen) primitiveReceiver(e ast.Expr) (*types.Primitive, bool) {
	switch t := g.typeOf(e).(type) {
	case *types.Primitive:
		return t, true
	case *types.Untyped:
		if p, ok := t.Default().(*types.Primitive); ok {
			return p, true
		}
	}
	return nil, false
}

func (g *gen) emitBoolMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	switch f.Name {
	case "toString":
		if len(c.Args) != 0 {
			return false
		}
		g.use("strconv")
		g.body.write("strconv.FormatBool(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toInt":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("func() int { if ")
		g.emitExpr(f.X)
		g.body.write(" { return 1 }; return 0 }()")
	default:
		return false
	}
	return true
}

func (g *gen) emitCharMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	switch f.Name {
	case "toInt":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("int(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toString":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("string(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isDigit", "isAlpha", "isAlphanumeric", "isWhitespace", "isUpper", "isLower":
		if len(c.Args) != 0 {
			return false
		}
		g.use("unicode")
		switch f.Name {
		case "isAlpha":
			g.body.write("unicode.IsLetter(")
			g.emitExpr(f.X)
			g.body.write(")")
		case "isAlphanumeric":
			g.body.write("func() bool { c := ")
			g.emitExpr(f.X)
			g.body.write("; return unicode.IsLetter(c) || unicode.IsDigit(c) }()")
		case "isWhitespace":
			g.body.write("unicode.IsSpace(")
			g.emitExpr(f.X)
			g.body.write(")")
		default:
			fn := map[string]string{
				"isDigit": "IsDigit",
				"isUpper": "IsUpper",
				"isLower": "IsLower",
			}[f.Name]
			g.body.write("unicode.")
			g.body.write(fn)
			g.body.write("(")
			g.emitExpr(f.X)
			g.body.write(")")
		}
	case "toUpper", "toLower":
		if len(c.Args) != 0 {
			return false
		}
		g.use("unicode")
		if f.Name == "toUpper" {
			g.body.write("unicode.ToUpper(")
		} else {
			g.body.write("unicode.ToLower(")
		}
		g.emitExpr(f.X)
		g.body.write(")")
	default:
		return false
	}
	return true
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
		g.emitPrimitiveIIFE("*byte", f.X, func(s string) {
			i := g.freshVar("_idx")
			g.body.writef("%s := ", i)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("if %s < 0 || %s >= len(%s) { return nil }\n", i, i, s)
			g.body.writef("v := %s[%s]\n", s, i)
			g.body.writeln("return &v")
		})
	case "contains", "startsWith", "endsWith":
		if len(c.Args) != 1 {
			return false
		}
		g.use("strings")
		fn := map[string]string{
			"contains":   "Contains",
			"startsWith": "HasPrefix",
			"endsWith":   "HasSuffix",
		}[f.Name]
		g.body.write("strings.")
		g.body.write(fn)
		g.body.write("(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
	case "indexOf":
		if len(c.Args) != 1 {
			return false
		}
		g.use("strings")
		g.emitPrimitiveIIFE("*int", f.X, func(s string) {
			g.body.write("needle := ")
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("i := strings.Index(%s, needle)\n", s)
			g.body.writeln("if i < 0 { return nil }")
			g.body.writeln("return &i")
		})
	case "split":
		if len(c.Args) != 1 {
			return false
		}
		g.use("strings")
		g.body.write("strings.Split(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
	case "lines":
		if len(c.Args) != 0 {
			return false
		}
		g.use("strings")
		g.body.write(`strings.Split(strings.TrimSuffix(`)
		g.emitExpr(f.X)
		g.body.write(`, "\n"), "\n")`)
	case "join":
		if len(c.Args) != 1 {
			return false
		}
		g.use("strings")
		g.body.write("strings.Join(")
		if !g.emitExprWithExpectedListElem(c.Args[0].Value, "string") {
			g.emitExpr(c.Args[0].Value)
		}
		g.body.write(", ")
		g.emitExpr(f.X)
		g.body.write(")")
	case "trim", "trimStart", "trimEnd":
		if len(c.Args) != 0 {
			return false
		}
		g.use("strings")
		switch f.Name {
		case "trim":
			g.body.write("strings.TrimSpace(")
		case "trimStart":
			g.body.write("strings.TrimLeftFunc(")
		case "trimEnd":
			g.body.write("strings.TrimRightFunc(")
		}
		g.emitExpr(f.X)
		if f.Name == "trim" {
			g.body.write(")")
		} else {
			g.use("unicode")
			g.body.write(", unicode.IsSpace)")
		}
	case "toUpper", "toLower":
		if len(c.Args) != 0 {
			return false
		}
		g.use("strings")
		if f.Name == "toUpper" {
			g.body.write("strings.ToUpper(")
		} else {
			g.body.write("strings.ToLower(")
		}
		g.emitExpr(f.X)
		g.body.write(")")
	case "replace":
		if len(c.Args) != 2 {
			return false
		}
		g.use("strings")
		g.body.write("strings.Replace(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitExpr(c.Args[0].Value)
		g.body.write(", ")
		g.emitExpr(c.Args[1].Value)
		g.body.write(", 1)")
	case "repeat":
		if len(c.Args) != 1 {
			return false
		}
		g.use("strings")
		g.body.write("strings.Repeat(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
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
		g.use("strconv")
		g.needResult = true
		g.emitPrimitiveIIFE("Result[int, any]", f.X, func(s string) {
			g.body.writef("v, err := strconv.Atoi(%s)\n", s)
			g.body.writeln("if err != nil { return resultErr[int, any](err) }")
			g.body.writeln("return resultOk[int, any](v)")
		})
	case "toFloat":
		if len(c.Args) != 0 {
			return false
		}
		g.use("strconv")
		g.needResult = true
		g.emitPrimitiveIIFE("Result[float64, any]", f.X, func(s string) {
			g.body.writef("v, err := strconv.ParseFloat(%s, 64)\n", s)
			g.body.writeln("if err != nil { return resultErr[float64, any](err) }")
			g.body.writeln("return resultOk[float64, any](v)")
		})
	default:
		return false
	}
	return true
}

func (g *gen) emitStringGraphemes(recv ast.Expr) {
	g.requestStdlibOsty("strings")
	g.body.write(stdlibOstyFuncName("strings", "graphemes"))
	g.body.write("(")
	g.emitExpr(recv)
	g.body.write(")")
}

func (g *gen) emitBytesMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
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
	case "get":
		if len(c.Args) != 1 {
			return false
		}
		g.emitPrimitiveIIFE("*byte", f.X, func(b string) {
			i := g.freshVar("_idx")
			g.body.writef("%s := ", i)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("if %s < 0 || %s >= len(%s) { return nil }\n", i, i, b)
			g.body.writef("v := %s[%s]\n", b, i)
			g.body.writeln("return &v")
		})
	case "concat":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, "[]byte")
		g.emitPrimitiveIIFE(retGo, f.X, func(b string) {
			other := g.freshVar("_other")
			g.body.writef("var %s []byte = ", other)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("out := make(%s, 0, len(%s)+len(%s))\n", retGo, b, other)
			g.body.writef("out = append(out, %s...)\n", b)
			g.body.writef("out = append(out, %s...)\n", other)
			g.body.writeln("return out")
		})
	case "toString":
		if len(c.Args) != 0 {
			return false
		}
		g.needResult = true
		g.useAs("unicode/utf8", "_ostybytesutf8")
		g.emitPrimitiveIIFE("Result[string, any]", f.X, func(b string) {
			g.body.writef("if !_ostybytesutf8.Valid(%s) { return resultErr[string, any](fmt.Errorf(%q)) }\n", b, "invalid UTF-8")
			g.body.writef("return resultOk[string, any](string(%s))\n", b)
		})
	default:
		return false
	}
	return true
}

func (g *gen) emitIntMethod(c *ast.CallExpr, f *ast.FieldExpr, recv types.PrimitiveKind) bool {
	goT := goPrimitive(recv)
	switch f.Name {
	case "abs", "wrappingAbs":
		if len(c.Args) != 0 {
			return false
		}
		g.emitIntAbs(f, recv, goT, f.Name == "wrappingAbs")
	case "min", "max":
		if len(c.Args) != 1 {
			return false
		}
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			other := g.freshVar("_other")
			g.body.writef("var %s %s = ", other, goT)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			op := "<"
			if f.Name == "max" {
				op = ">"
			}
			g.body.writef("if %s %s %s { return %s }\n", v, op, other, v)
			g.body.writef("return %s\n", other)
		})
	case "clamp":
		if len(c.Args) != 2 {
			return false
		}
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			lo := g.freshVar("_lo")
			hi := g.freshVar("_hi")
			g.body.writef("var %s %s = ", lo, goT)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("var %s %s = ", hi, goT)
			g.emitExpr(c.Args[1].Value)
			g.body.nl()
			g.body.writef("if %s < %s { return %s }\n", v, lo, lo)
			g.body.writef("if %s > %s { return %s }\n", v, hi, hi)
			g.body.writef("return %s\n", v)
		})
	case "signum":
		if len(c.Args) != 0 {
			return false
		}
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			if recv.IsSignedInt() {
				g.body.writef("if %s < 0 { return %s(-1) }\n", v, goT)
			}
			g.body.writef("if %s > 0 { return %s(1) }\n", v, goT)
			g.body.writef("return %s(0)\n", goT)
		})
	case "wrappingAdd", "wrappingSub", "wrappingMul", "wrappingDiv", "wrappingMod":
		if len(c.Args) != 1 {
			return false
		}
		g.emitWrappingIntBinary(f.Name, f, c.Args[0].Value, recv, goT)
	case "wrappingShl", "wrappingShr":
		if len(c.Args) != 1 {
			return false
		}
		g.emitWrappingIntShift(f.Name, f, c.Args[0].Value, recv, goT)
	case "wrappingNeg":
		if len(c.Args) != 0 {
			return false
		}
		if recv.IsUnsignedInt() {
			g.body.write("(")
			g.body.write(goT)
			g.body.write("(0) - ")
			g.emitExpr(f.X)
			g.body.write(")")
		} else {
			g.body.write("(-")
			g.emitExpr(f.X)
			g.body.write(")")
		}
	case "checkedAdd", "checkedSub", "checkedMul", "checkedDiv", "checkedMod":
		if len(c.Args) != 1 {
			return false
		}
		g.emitCheckedIntBinary(f.Name, f, c.Args[0].Value, recv, goT)
	case "checkedShl", "checkedShr":
		if len(c.Args) != 1 {
			return false
		}
		g.emitCheckedIntShift(f.Name, f, c.Args[0].Value, recv, goT)
	case "checkedAbs":
		if len(c.Args) != 0 {
			return false
		}
		g.emitCheckedIntAbs(f, recv, goT)
	case "checkedNeg":
		if len(c.Args) != 0 {
			return false
		}
		g.emitCheckedIntNeg(f, recv, goT)
	case "saturatingAdd", "saturatingSub", "saturatingMul", "saturatingDiv":
		if len(c.Args) != 1 {
			return false
		}
		g.emitSaturatingIntBinary(f.Name, f, c.Args[0].Value, recv, goT)
	case "pow":
		if len(c.Args) != 1 {
			return false
		}
		g.emitIntPow(f, c.Args[0].Value, recv, goT)
	case "toInt", "toInt64", "toFloat", "toFloat32", "toFloat64", "toChar":
		if len(c.Args) != 0 {
			return false
		}
		if f.Name == "toChar" {
			g.emitPrimitiveIIFE("rune", f.X, func(v string) {
				g.body.writef("if %s < 0 || %s > 0x10ffff || (%s >= 0xd800 && %s <= 0xdfff) { panic(%q) }\n",
					v, v, v, v, "invalid Char code point")
				g.body.writef("return rune(%s)\n", v)
			})
			return true
		}
		target := map[string]string{
			"toInt":     "int",
			"toInt64":   "int64",
			"toFloat":   "float64",
			"toFloat32": "float32",
			"toFloat64": "float64",
		}[f.Name]
		g.body.write(target)
		g.body.write("(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toInt8", "toInt16", "toInt32", "toUInt8", "toByte", "toUInt16", "toUInt32", "toUInt64":
		if len(c.Args) != 0 {
			return false
		}
		return g.emitIntConversionResult(f, recv)
	case "toString":
		if len(c.Args) != 0 {
			return false
		}
		g.use("strconv")
		if recv.IsUnsignedInt() {
			g.body.write("strconv.FormatUint(uint64(")
			g.emitExpr(f.X)
			g.body.write("), 10)")
		} else {
			g.body.write("strconv.FormatInt(int64(")
			g.emitExpr(f.X)
			g.body.write("), 10)")
		}
	default:
		return false
	}
	return true
}

type intRuntimeInfo struct {
	min    string
	max    string
	bits   string
	signed bool
}

func runtimeIntInfo(k types.PrimitiveKind) intRuntimeInfo {
	switch k {
	case types.PInt:
		return intRuntimeInfo{min: "math.MinInt", max: "math.MaxInt", bits: "strconv.IntSize", signed: true}
	case types.PInt8:
		return intRuntimeInfo{min: "math.MinInt8", max: "math.MaxInt8", bits: "8", signed: true}
	case types.PInt16:
		return intRuntimeInfo{min: "math.MinInt16", max: "math.MaxInt16", bits: "16", signed: true}
	case types.PInt32:
		return intRuntimeInfo{min: "math.MinInt32", max: "math.MaxInt32", bits: "32", signed: true}
	case types.PInt64:
		return intRuntimeInfo{min: "math.MinInt64", max: "math.MaxInt64", bits: "64", signed: true}
	case types.PUInt8, types.PByte:
		return intRuntimeInfo{min: "0", max: "math.MaxUint8", bits: "8"}
	case types.PUInt16:
		return intRuntimeInfo{min: "0", max: "math.MaxUint16", bits: "16"}
	case types.PUInt32:
		return intRuntimeInfo{min: "0", max: "math.MaxUint32", bits: "32"}
	case types.PUInt64:
		return intRuntimeInfo{min: "0", max: "math.MaxUint64", bits: "64"}
	default:
		return intRuntimeInfo{min: "0", max: "0", bits: "0"}
	}
}

func (g *gen) emitIntAbs(f *ast.FieldExpr, recv types.PrimitiveKind, goT string, wrapping bool) {
	if recv.IsUnsignedInt() {
		g.emitExpr(f.X)
		return
	}
	g.use("math")
	info := runtimeIntInfo(recv)
	g.emitPrimitiveIIFE(goT, f.X, func(v string) {
		if wrapping {
			g.body.writef("if %s == %s { return %s }\n", v, info.min, v)
		} else {
			g.body.writef("if %s == %s { panic(%q) }\n", v, info.min, "integer overflow")
		}
		g.body.writef("if %s < 0 { return -%s }\n", v, v)
		g.body.writef("return %s\n", v)
	})
}

func (g *gen) emitWrappingIntBinary(name string, f *ast.FieldExpr, arg ast.Expr, recv types.PrimitiveKind, goT string) {
	op := map[string]string{
		"wrappingAdd": "+",
		"wrappingSub": "-",
		"wrappingMul": "*",
		"wrappingDiv": "/",
		"wrappingMod": "%",
	}[name]
	if recv.IsSignedInt() && (name == "wrappingDiv" || name == "wrappingMod") {
		g.use("math")
		info := runtimeIntInfo(recv)
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			rhs := g.freshVar("_rhs")
			g.body.writef("var %s %s = ", rhs, goT)
			g.emitExpr(arg)
			g.body.nl()
			g.body.writef("if %s == %s && %s == %s(-1) {\n", v, info.min, rhs, goT)
			g.body.indent()
			if name == "wrappingDiv" {
				g.body.writef("return %s\n", v)
			} else {
				g.body.writef("return %s(0)\n", goT)
			}
			g.body.dedent()
			g.body.writeln("}")
			g.body.writef("return %s %s %s\n", v, op, rhs)
		})
		return
	}
	g.body.write("(")
	g.emitExpr(f.X)
	g.body.write(" ")
	g.body.write(op)
	g.body.write(" ")
	g.emitExpr(arg)
	g.body.write(")")
}

func (g *gen) emitWrappingIntShift(name string, f *ast.FieldExpr, arg ast.Expr, recv types.PrimitiveKind, goT string) {
	if recv == types.PInt {
		g.use("strconv")
	}
	info := runtimeIntInfo(recv)
	op := "<<"
	if name == "wrappingShr" {
		op = ">>"
	}
	g.emitPrimitiveIIFE(goT, f.X, func(v string) {
		rhs := g.freshVar("_rhs")
		g.body.writef("var %s int = ", rhs)
		g.emitExpr(arg)
		g.body.nl()
		g.body.writef("bitWidth := int(%s)\n", info.bits)
		g.body.writef("shift := %s %% bitWidth\n", rhs)
		g.body.writeln("if shift < 0 { shift += bitWidth }")
		g.body.writef("return %s %s uint(shift)\n", v, op)
	})
}

func (g *gen) emitCheckedIntBinary(name string, f *ast.FieldExpr, arg ast.Expr, recv types.PrimitiveKind, goT string) {
	info := runtimeIntInfo(recv)
	if name == "checkedAdd" || name == "checkedSub" || name == "checkedMul" || recv.IsSignedInt() {
		g.use("math")
	}
	op := map[string]string{
		"checkedAdd": "+",
		"checkedSub": "-",
		"checkedMul": "*",
		"checkedDiv": "/",
		"checkedMod": "%",
	}[name]
	g.emitPrimitiveIIFE("*"+goT, f.X, func(v string) {
		rhs := g.freshVar("_rhs")
		g.body.writef("var %s %s = ", rhs, goT)
		g.emitExpr(arg)
		g.body.nl()
		switch name {
		case "checkedAdd":
			g.emitIntAddOverflowGuard(info, v, rhs, "return nil", "return nil")
		case "checkedSub":
			g.emitIntSubOverflowGuard(info, v, rhs, "return nil", "return nil")
		case "checkedMul":
			g.emitIntMulOverflowGuard(info, goT, v, rhs, "return nil", "return nil")
		case "checkedDiv", "checkedMod":
			g.body.writef("if %s == 0 { return nil }\n", rhs)
			if info.signed {
				g.body.writef("if %s == %s && %s == %s(-1) { return nil }\n", v, info.min, rhs, goT)
			}
		}
		g.body.writef("out := %s %s %s\n", v, op, rhs)
		g.body.writeln("return &out")
	})
}

func (g *gen) emitCheckedIntShift(name string, f *ast.FieldExpr, arg ast.Expr, recv types.PrimitiveKind, goT string) {
	if recv == types.PInt {
		g.use("strconv")
	}
	info := runtimeIntInfo(recv)
	op := "<<"
	if name == "checkedShr" {
		op = ">>"
	}
	g.emitPrimitiveIIFE("*"+goT, f.X, func(v string) {
		rhs := g.freshVar("_rhs")
		g.body.writef("var %s int = ", rhs)
		g.emitExpr(arg)
		g.body.nl()
		g.body.writef("bitWidth := int(%s)\n", info.bits)
		g.body.writef("if %s < 0 || %s >= bitWidth { return nil }\n", rhs, rhs)
		g.body.writef("out := %s %s uint(%s)\n", v, op, rhs)
		g.body.writeln("return &out")
	})
}

func (g *gen) emitCheckedIntAbs(f *ast.FieldExpr, recv types.PrimitiveKind, goT string) {
	if recv.IsSignedInt() {
		g.use("math")
	}
	info := runtimeIntInfo(recv)
	g.emitPrimitiveIIFE("*"+goT, f.X, func(v string) {
		if info.signed {
			g.body.writef("if %s == %s { return nil }\n", v, info.min)
			g.body.writef("if %s < 0 { out := -%s; return &out }\n", v, v)
		}
		g.body.writef("out := %s\n", v)
		g.body.writeln("return &out")
	})
}

func (g *gen) emitCheckedIntNeg(f *ast.FieldExpr, recv types.PrimitiveKind, goT string) {
	if recv.IsSignedInt() {
		g.use("math")
	}
	info := runtimeIntInfo(recv)
	g.emitPrimitiveIIFE("*"+goT, f.X, func(v string) {
		if info.signed {
			g.body.writef("if %s == %s { return nil }\n", v, info.min)
			g.body.writef("out := -%s\n", v)
		} else {
			g.body.writef("if %s != 0 { return nil }\n", v)
			g.body.writef("out := %s(0)\n", goT)
		}
		g.body.writeln("return &out")
	})
}

func (g *gen) emitSaturatingIntBinary(name string, f *ast.FieldExpr, arg ast.Expr, recv types.PrimitiveKind, goT string) {
	info := runtimeIntInfo(recv)
	if name == "saturatingAdd" || name == "saturatingSub" || name == "saturatingMul" || recv.IsSignedInt() {
		g.use("math")
	}
	op := map[string]string{
		"saturatingAdd": "+",
		"saturatingSub": "-",
		"saturatingMul": "*",
		"saturatingDiv": "/",
	}[name]
	g.emitPrimitiveIIFE(goT, f.X, func(v string) {
		rhs := g.freshVar("_rhs")
		g.body.writef("var %s %s = ", rhs, goT)
		g.emitExpr(arg)
		g.body.nl()
		switch name {
		case "saturatingAdd":
			g.emitIntAddOverflowGuard(info, v, rhs, "return "+info.min, "return "+info.max)
		case "saturatingSub":
			g.emitIntSubOverflowGuard(info, v, rhs, "return "+info.min, "return "+info.max)
		case "saturatingMul":
			g.emitIntMulOverflowGuard(info, goT, v, rhs, "return "+info.min, "return "+info.max)
		case "saturatingDiv":
			if info.signed {
				g.body.writef("if %s == %s && %s == %s(-1) { return %s }\n", v, info.min, rhs, goT, info.max)
			}
		}
		g.body.writef("return %s %s %s\n", v, op, rhs)
	})
}

func (g *gen) emitIntPow(f *ast.FieldExpr, exp ast.Expr, recv types.PrimitiveKind, goT string) {
	g.use("math")
	info := runtimeIntInfo(recv)
	g.emitPrimitiveIIFE(goT, f.X, func(v string) {
		expVar := g.freshVar("_exp")
		g.body.writef("var %s int = ", expVar)
		g.emitExpr(exp)
		g.body.nl()
		g.body.writef("if %s < 0 { panic(%q) }\n", expVar, "negative integer exponent")
		g.body.writef("out := %s(1)\n", goT)
		g.body.writef("for i := 0; i < %s; i++ {\n", expVar)
		g.body.indent()
		g.emitIntMulOverflowGuard(info, goT, "out", v, "panic(\"integer overflow\")", "panic(\"integer overflow\")")
		g.body.writef("out *= %s\n", v)
		g.body.dedent()
		g.body.writeln("}")
		g.body.writeln("return out")
	})
}

func (g *gen) emitIntAddOverflowGuard(info intRuntimeInfo, v, rhs, onLow, onHigh string) {
	if info.signed {
		g.body.writef("if %s > 0 && %s > %s-%s { %s }\n", rhs, v, info.max, rhs, onHigh)
		g.body.writef("if %s < 0 && %s < %s-%s { %s }\n", rhs, v, info.min, rhs, onLow)
		return
	}
	g.body.writef("if %s > %s-%s { %s }\n", v, info.max, rhs, onHigh)
}

func (g *gen) emitIntSubOverflowGuard(info intRuntimeInfo, v, rhs, onLow, onHigh string) {
	if info.signed {
		g.body.writef("if %s < 0 && %s > %s+%s { %s }\n", rhs, v, info.max, rhs, onHigh)
		g.body.writef("if %s > 0 && %s < %s+%s { %s }\n", rhs, v, info.min, rhs, onLow)
		return
	}
	g.body.writef("if %s < %s { %s }\n", v, rhs, onLow)
}

func (g *gen) emitIntMulOverflowGuard(info intRuntimeInfo, goT, v, rhs, onLow, onHigh string) {
	if !info.signed {
		g.body.writef("if %s != 0 && %s > %s/%s { %s }\n", rhs, v, info.max, rhs, onHigh)
		return
	}
	g.body.writef("if %s != 0 && %s != 0 {\n", v, rhs)
	g.body.indent()
	g.body.writef("if %s == %s(-1) && %s == %s { %s }\n", v, goT, rhs, info.min, onHigh)
	g.body.writef("if %s == %s(-1) && %s == %s { %s }\n", rhs, goT, v, info.min, onHigh)
	g.body.writef("if %s > 0 {\n", v)
	g.body.indent()
	g.body.writef("if %s > 0 && %s > %s/%s { %s }\n", rhs, v, info.max, rhs, onHigh)
	g.body.writef("if %s < 0 && %s < %s/%s { %s }\n", rhs, rhs, info.min, v, onLow)
	g.body.dedent()
	g.body.writeln("} else {")
	g.body.indent()
	g.body.writef("if %s > 0 && %s < %s/%s { %s }\n", rhs, v, info.min, rhs, onLow)
	g.body.writef("if %s < 0 && %s < %s/%s { %s }\n", rhs, v, info.max, rhs, onHigh)
	g.body.dedent()
	g.body.writeln("}")
	g.body.dedent()
	g.body.writeln("}")
}

func (g *gen) emitFloatMethod(c *ast.CallExpr, f *ast.FieldExpr, recv types.PrimitiveKind) bool {
	goT := goPrimitive(recv)
	bits := "64"
	if recv == types.PFloat32 {
		bits = "32"
	}
	switch f.Name {
	case "abs", "floor", "ceil", "round", "trunc", "sqrt", "cbrt", "ln", "log2", "log10",
		"exp", "sin", "cos", "tan", "asin", "acos", "atan":
		if len(c.Args) != 0 {
			return false
		}
		fn := map[string]string{
			"abs":   "Abs",
			"floor": "Floor",
			"ceil":  "Ceil",
			"round": "RoundToEven",
			"trunc": "Trunc",
			"sqrt":  "Sqrt",
			"cbrt":  "Cbrt",
			"ln":    "Log",
			"log2":  "Log2",
			"log10": "Log10",
			"exp":   "Exp",
			"sin":   "Sin",
			"cos":   "Cos",
			"tan":   "Tan",
			"asin":  "Asin",
			"acos":  "Acos",
			"atan":  "Atan",
		}[f.Name]
		g.use("math")
		g.body.write(goT)
		g.body.write("(math.")
		g.body.write(fn)
		g.body.write("(float64(")
		g.emitExpr(f.X)
		g.body.write(")))")
	case "min", "max":
		if len(c.Args) != 1 {
			return false
		}
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			other := g.freshVar("_other")
			g.body.writef("var %s %s = ", other, goT)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			op := "<"
			if f.Name == "max" {
				op = ">"
			}
			g.body.writef("if %s %s %s { return %s }\n", v, op, other, v)
			g.body.writef("return %s\n", other)
		})
	case "clamp":
		if len(c.Args) != 2 {
			return false
		}
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			lo := g.freshVar("_lo")
			hi := g.freshVar("_hi")
			g.body.writef("var %s %s = ", lo, goT)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("var %s %s = ", hi, goT)
			g.emitExpr(c.Args[1].Value)
			g.body.nl()
			g.body.writef("if %s < %s { return %s }\n", v, lo, lo)
			g.body.writef("if %s > %s { return %s }\n", v, hi, hi)
			g.body.writef("return %s\n", v)
		})
	case "signum":
		if len(c.Args) != 0 {
			return false
		}
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			g.body.writef("if %s < 0 { return %s(-1) }\n", v, goT)
			g.body.writef("if %s > 0 { return %s(1) }\n", v, goT)
			g.body.writef("return %s(0)\n", goT)
		})
	case "fract":
		if len(c.Args) != 0 {
			return false
		}
		g.use("math")
		g.emitPrimitiveIIFE(goT, f.X, func(v string) {
			g.body.writef("return %s - %s(math.Trunc(float64(%s)))\n", v, goT, v)
		})
	case "atan2", "pow":
		if len(c.Args) != 1 {
			return false
		}
		g.use("math")
		fn := "Atan2"
		if f.Name == "pow" {
			fn = "Pow"
		}
		g.body.write(goT)
		g.body.write("(math.")
		g.body.write(fn)
		g.body.write("(float64(")
		g.emitExpr(f.X)
		g.body.write("), float64(")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")))")
	case "isNaN", "isInfinite", "isFinite":
		if len(c.Args) != 0 {
			return false
		}
		g.use("math")
		switch f.Name {
		case "isNaN":
			g.body.write("math.IsNaN(float64(")
			g.emitExpr(f.X)
			g.body.write("))")
		case "isInfinite":
			g.body.write("math.IsInf(float64(")
			g.emitExpr(f.X)
			g.body.write("), 0)")
		case "isFinite":
			g.body.write("func() bool { v := float64(")
			g.emitExpr(f.X)
			g.body.write("); return !math.IsNaN(v) && !math.IsInf(v, 0) }()")
		}
	case "toBits":
		if len(c.Args) != 0 {
			return false
		}
		g.use("math")
		if recv == types.PFloat32 {
			g.body.write("uint64(math.Float32bits(")
			g.emitExpr(f.X)
			g.body.write("))")
		} else {
			g.body.write("math.Float64bits(float64(")
			g.emitExpr(f.X)
			g.body.write("))")
		}
	case "toFixed":
		if len(c.Args) != 1 {
			return false
		}
		g.use("strconv")
		g.emitPrimitiveIIFE("string", f.X, func(v string) {
			precision := g.freshVar("_precision")
			g.body.writef("var %s int = ", precision)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("if %s < 0 { %s = 0 }\n", precision, precision)
			g.body.writef("return strconv.FormatFloat(float64(%s), 'f', %s, %s)\n", v, precision, bits)
		})
	case "toIntTrunc", "toIntRound", "toIntFloor", "toIntCeil":
		if len(c.Args) != 0 {
			return false
		}
		g.emitFloatToIntResult(f)
	case "toInt", "toInt32", "toInt64", "toFloat", "toFloat32", "toFloat64":
		if len(c.Args) != 0 {
			return false
		}
		target := map[string]string{
			"toInt":     "int",
			"toInt32":   "int32",
			"toInt64":   "int64",
			"toFloat":   "float64",
			"toFloat32": "float32",
			"toFloat64": "float64",
		}[f.Name]
		g.body.write(target)
		g.body.write("(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "toString":
		if len(c.Args) != 0 {
			return false
		}
		g.use("strconv")
		g.body.write("strconv.FormatFloat(float64(")
		g.emitExpr(f.X)
		g.body.write("), 'g', -1, ")
		g.body.write(bits)
		g.body.write(")")
	default:
		return false
	}
	return true
}

func (g *gen) emitFloatToIntResult(f *ast.FieldExpr) {
	g.needResult = true
	g.use("math")
	g.use("strconv")
	fn := map[string]string{
		"toIntTrunc": "Trunc",
		"toIntRound": "RoundToEven",
		"toIntFloor": "Floor",
		"toIntCeil":  "Ceil",
	}[f.Name]
	g.emitPrimitiveIIFE("Result[int, any]", f.X, func(v string) {
		rounded := g.freshVar("_rounded")
		g.body.writef("%s := math.%s(float64(%s))\n", rounded, fn, v)
		g.body.writeln("lo := -9223372036854775808.0")
		g.body.writeln("hi := 9223372036854775808.0")
		g.body.writeln("if strconv.IntSize == 32 { lo = -2147483648.0; hi = 2147483648.0 }")
		g.body.writef("if math.IsNaN(%s) || math.IsInf(%s, 0) || %s < lo || %s >= hi { return resultErr[int, any](%q) }\n",
			rounded, rounded, rounded, rounded, f.Name+" out of Int range")
		g.body.writef("return resultOk[int, any](int(%s))\n", rounded)
	})
}

func (g *gen) emitIntConversionResult(f *ast.FieldExpr, recv types.PrimitiveKind) bool {
	info, ok := intConversionTarget(f.Name)
	if !ok {
		return false
	}
	g.needResult = true
	retGo := "Result[" + info.goType + ", any]"
	g.emitPrimitiveIIFE(retGo, f.X, func(v string) {
		for _, cond := range intConversionFailureConds(v, recv, info) {
			g.body.writef("if %s { return resultErr[%s, any](%q) }\n",
				cond, info.goType, f.Name+" overflow")
		}
		g.body.writef("return resultOk[%s, any](%s(%s))\n", info.goType, info.goType, v)
	})
	return true
}

type intConvTarget struct {
	goType string
	min    string
	max    string
	signed bool
}

func intConversionTarget(name string) (intConvTarget, bool) {
	switch name {
	case "toInt8":
		return intConvTarget{goType: "int8", min: "-128", max: "127", signed: true}, true
	case "toInt16":
		return intConvTarget{goType: "int16", min: "-32768", max: "32767", signed: true}, true
	case "toInt32":
		return intConvTarget{goType: "int32", min: "-2147483648", max: "2147483647", signed: true}, true
	case "toUInt8", "toByte":
		if name == "toByte" {
			return intConvTarget{goType: "byte", min: "0", max: "255"}, true
		}
		return intConvTarget{goType: "uint8", min: "0", max: "255"}, true
	case "toUInt16":
		return intConvTarget{goType: "uint16", min: "0", max: "65535"}, true
	case "toUInt32":
		return intConvTarget{goType: "uint32", min: "0", max: "4294967295"}, true
	case "toUInt64":
		return intConvTarget{goType: "uint64", min: "0", max: "18446744073709551615"}, true
	}
	return intConvTarget{}, false
}

func intConversionFailureConds(v string, recv types.PrimitiveKind, target intConvTarget) []string {
	var conds []string
	if target.signed {
		if recv.IsSignedInt() {
			conds = append(conds, v+" < "+target.min, v+" > "+target.max)
		} else {
			conds = append(conds, v+" > "+target.max)
		}
		return conds
	}
	if recv.IsSignedInt() {
		conds = append(conds, v+" < 0")
	}
	if target.goType != "uint64" {
		conds = append(conds, v+" > "+target.max)
	}
	return conds
}

func (g *gen) emitPrimitiveIIFE(retGo string, recv ast.Expr, body func(recvVar string)) {
	recvVar := g.freshVar("_p")
	g.body.write("func()")
	if retGo != "" {
		g.body.write(" ")
		g.body.write(retGo)
	}
	g.body.writeln(" {")
	g.body.indent()
	g.body.writef("%s := ", recvVar)
	g.emitExpr(recv)
	g.body.nl()
	body(recvVar)
	g.body.dedent()
	g.body.write("}()")
}

func (g *gen) emitPrimitiveTypedIIFE(retGo, recvGo string, recv ast.Expr, body func(recvVar string)) {
	recvVar := g.freshVar("_p")
	g.body.write("func()")
	if retGo != "" {
		g.body.write(" ")
		g.body.write(retGo)
	}
	g.body.writeln(" {")
	g.body.indent()
	g.body.writef("var %s %s = ", recvVar, recvGo)
	g.emitExpr(recv)
	g.body.nl()
	body(recvVar)
	g.body.dedent()
	g.body.write("}()")
}
