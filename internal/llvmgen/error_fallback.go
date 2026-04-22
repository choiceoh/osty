package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

var errorSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Error"},
}

var optionalErrorSourceTypeSingleton ast.Type = &ast.OptionalType{
	Inner: errorSourceTypeSingleton,
}

func (g *generator) usesPtrBackedErrorFallback() bool {
	return g.interfacesByName["Error"] == nil
}

func isErrorConstructorReceiver(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e != nil && e.Name == "Error"
	case *ast.FieldExpr:
		return e != nil && e.Name == "Error"
	default:
		return false
	}
}

func (g *generator) staticPtrBackedErrorCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	if !g.usesPtrBackedErrorFallback() {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field == nil || field.IsOptional {
		return nil, false
	}
	switch field.Name {
	case "new":
		if isErrorConstructorReceiver(field.X) {
			return errorSourceTypeSingleton, true
		}
	case "message":
		src, ok := g.staticExprSourceType(field.X)
		if ok && namedTypeSingleSegment(src) == "Error" {
			return &ast.NamedType{Path: []string{"String"}}, true
		}
	case "source":
		src, ok := g.staticExprSourceType(field.X)
		if ok && namedTypeSingleSegment(src) == "Error" {
			return optionalErrorSourceTypeSingleton, true
		}
	}
	return nil, false
}

func (g *generator) emitPtrBackedErrorCall(call *ast.CallExpr) (value, bool, error) {
	if !g.usesPtrBackedErrorFallback() {
		return value{}, false, nil
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field == nil || field.IsOptional {
		return value{}, false, nil
	}
	switch field.Name {
	case "new":
		if !isErrorConstructorReceiver(field.X) {
			return value{}, false, nil
		}
		return g.emitPtrBackedErrorNewCall(call)
	case "message":
		src, ok := g.staticExprSourceType(field.X)
		if !ok || namedTypeSingleSegment(src) != "Error" {
			return value{}, false, nil
		}
		return g.emitPtrBackedErrorMessageCall(call)
	case "source":
		src, ok := g.staticExprSourceType(field.X)
		if !ok || namedTypeSingleSegment(src) != "Error" {
			return value{}, false, nil
		}
		return g.emitPtrBackedErrorSourceCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) emitPtrBackedErrorNewCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupportedf("call", "Error.new requires one positional String argument")
	}
	msg, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, true, err
	}
	msg = g.protectManagedTemporary("error.new.message", msg)
	msg, err = g.loadIfPointer(msg)
	if err != nil {
		return value{}, true, err
	}
	if msg.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Error.new arg 1 type %s, want String", msg.typ)
	}
	msg.sourceType = errorSourceTypeSingleton
	msg.gcManaged = true
	msg.rootPaths = g.rootPathsForType(msg.typ)
	return msg, true, nil
}

func (g *generator) emitPtrBackedErrorMessageCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "Error.message takes no arguments, got %d", len(call.Args))
	}
	field := call.Fn.(*ast.FieldExpr)
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	recv = g.protectManagedTemporary("error.message.self", recv)
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, true, err
	}
	if recv.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Error.message receiver type %s, want Error", recv.typ)
	}
	recv.sourceType = &ast.NamedType{Path: []string{"String"}}
	recv.gcManaged = true
	recv.rootPaths = g.rootPathsForType(recv.typ)
	return recv, true, nil
}

func (g *generator) emitPtrBackedErrorSourceCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "Error.source takes no arguments, got %d", len(call.Args))
	}
	return value{
		typ:        "ptr",
		ref:        "null",
		sourceType: optionalErrorSourceTypeSingleton,
	}, true, nil
}
