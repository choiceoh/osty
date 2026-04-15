package gen

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
)

type jsonAnnotationOptions struct {
	name     string
	skip     bool
	optional bool
}

func (g *gen) emitStructJSONMarshal(s *ast.StructDecl) {
	if !g.needJSON || hasMethodNamed(s.Methods, "MarshalJSON") {
		return
	}
	recv := genericReceiverName(s.Name, s.Generics)
	g.body.nl()
	g.body.writef("func (self %s) MarshalJSON() ([]byte, error) {\n", recv)
	g.body.indent()
	if m := receiverMethodNamed(s.Methods, "toJson"); m != nil {
		call := "self.toJson()"
		if m.Recv != nil && m.Recv.Mut {
			call = "(&self).toJson()"
		}
		g.body.writef("return jsonMarshalAny(%s)\n", call)
		g.body.dedent()
		g.body.writeln("}")
		return
	}
	g.body.writeln("return jsonMarshalObject([]jsonField{")
	g.body.indent()
	for _, f := range s.Fields {
		opts := jsonOptions(f.Annotations, f.Name)
		if opts.skip {
			continue
		}
		g.body.writef("{Name: %q, Value: self.%s", opts.name, mangleIdent(f.Name))
		if opts.optional {
			g.body.write(", OmitNil: true")
		}
		g.body.writeln("},")
	}
	g.body.dedent()
	g.body.writeln("})")
	g.body.dedent()
	g.body.writeln("}")
}

func (g *gen) emitStructJSONUnmarshal(s *ast.StructDecl) {
	if !g.needJSON || hasMethodNamed(s.Methods, "UnmarshalJSON") {
		return
	}
	recv := genericReceiverName(s.Name, s.Generics)
	g.body.nl()
	g.body.writef("func (self *%s) UnmarshalJSON(data []byte) error {\n", recv)
	g.body.indent()
	if len(s.Generics) == 0 && associatedMethodNamed(s.Methods, "fromJson") != nil {
		g.body.writeln("value, err := jsonParseValue(data)")
		g.body.writeln("if err != nil {")
		g.body.indent()
		g.body.writeln("return err")
		g.body.dedent()
		g.body.writeln("}")
		g.body.writef("result := %s_fromJson(value)\n", s.Name)
		g.body.writeln("if !result.IsOk {")
		g.body.indent()
		g.body.writeln("return jsonAsError(result.Error)")
		g.body.dedent()
		g.body.writeln("}")
		g.body.writeln("*self = result.Value")
		g.body.writeln("return nil")
		g.body.dedent()
		g.body.writeln("}")
		return
	}
	g.body.writeln("var raw map[string]stdjson.RawMessage")
	g.body.writeln("if err := stdjson.Unmarshal(data, &raw); err != nil {")
	g.body.indent()
	g.body.writeln("return err")
	g.body.dedent()
	g.body.writeln("}")
	g.body.writef("if raw == nil && jsonRawIsNull(data) { return fmt.Errorf(%q) }\n",
		fmt.Sprintf("std.json: %s expects object, got null", s.Name))
	for _, f := range s.Fields {
		opts := jsonOptions(f.Annotations, f.Name)
		if opts.skip {
			continue
		}
		g.body.writef("if value, ok := raw[%q]; ok {\n", opts.name)
		g.body.indent()
		if !g.jsonTypeAllowsNull(f.Type) {
			msg := fmt.Sprintf("std.json: %s.%s is null", s.Name, opts.name)
			g.body.writef("if jsonRawIsNull(value) { return fmt.Errorf(%q) }\n", msg)
		}
		msg := fmt.Sprintf("std.json: %s.%s: %%w", s.Name, opts.name)
		g.body.writef("if err := jsonUnmarshalInto(value, &self.%s); err != nil { return fmt.Errorf(%q, err) }\n",
			mangleIdent(f.Name), msg)
		g.body.dedent()
		g.body.writeln("}")
	}
	g.body.writeln("return nil")
	g.body.dedent()
	g.body.writeln("}")
}

func (g *gen) emitVariantJSONMarshal(e *ast.EnumDecl, v *ast.Variant, goName string) {
	if !g.needJSON {
		return
	}
	if receiverMethodNamed(e.Methods, "toJson") != nil {
		g.body.writef("func (self %s) MarshalJSON() ([]byte, error) { ", goName)
		g.body.writef("return jsonMarshalAny(%s_toJson(%s(self)))", e.Name, e.Name)
		g.body.writeln(" }")
		return
	}
	opts := jsonOptions(v.Annotations, v.Name)
	g.body.writef("func (self %s) MarshalJSON() ([]byte, error) { ", goName)
	if opts.skip {
		g.body.writef("return jsonSkippedVariant(%q, %q)", e.Name, v.Name)
		g.body.writeln(" }")
		return
	}
	g.body.writef("return jsonMarshalEnum(%q", opts.name)
	for i := range v.Fields {
		g.body.writef(", self.F%d", i)
	}
	g.body.writeln(") }")
}

func (g *gen) emitEnumJSONDecoder(e *ast.EnumDecl) {
	if !g.needJSON {
		return
	}
	if associatedMethodNamed(e.Methods, "fromJson") != nil {
		g.emitEnumJSONCustomDecoder(e)
		return
	}
	g.body.nl()
	g.body.writeln("func init() {")
	g.body.indent()
	g.body.writef("jsonRegisterDecoder(reflect.TypeOf((*%s)(nil)).Elem(), func(data []byte) (any, error) { return jsonUnmarshal%s(data) })\n", e.Name, e.Name)
	g.body.dedent()
	g.body.writeln("}")
	g.body.nl()
	g.body.writef("func jsonUnmarshal%s(data []byte) (%s, error) {\n", e.Name, e.Name)
	g.body.indent()
	g.body.writeln("var head struct {")
	g.body.indent()
	g.body.writeln("Tag string `json:\"tag\"`")
	g.body.writeln("Value stdjson.RawMessage `json:\"value\"`")
	g.body.dedent()
	g.body.writeln("}")
	g.body.writeln("if err := stdjson.Unmarshal(data, &head); err != nil {")
	g.body.indent()
	g.body.writeln("return nil, err")
	g.body.dedent()
	g.body.writeln("}")
	g.body.writeln("switch head.Tag {")
	g.body.indent()
	for _, v := range e.Variants {
		opts := jsonOptions(v.Annotations, v.Name)
		if opts.skip {
			continue
		}
		goName := e.Name + "_" + v.Name
		label := e.Name + "." + v.Name
		g.body.writef("case %q:\n", opts.name)
		g.body.indent()
		switch len(v.Fields) {
		case 0:
			g.body.writef("return %s(%s{}), nil\n", e.Name, goName)
		case 1:
			fieldType := g.goTypeExpr(v.Fields[0])
			missing := fmt.Sprintf("std.json: %s missing value", label)
			msg := fmt.Sprintf("std.json: %s value: %%w", label)
			g.body.writef("if len(head.Value) == 0 { return nil, fmt.Errorf(%q) }\n", missing)
			g.body.writef("var f0 %s\n", fieldType)
			g.body.writef("if err := jsonUnmarshalInto(head.Value, &f0); err != nil { return nil, fmt.Errorf(%q, err) }\n", msg)
			g.body.writef("return %s(%s{F0: f0}), nil\n", e.Name, goName)
		default:
			g.body.writef("values, err := jsonUnmarshalArray(head.Value, %d, %q)\n", len(v.Fields), label)
			g.body.writeln("if err != nil {")
			g.body.indent()
			g.body.writeln("return nil, err")
			g.body.dedent()
			g.body.writeln("}")
			for i, f := range v.Fields {
				msg := fmt.Sprintf("std.json: %s value[%d]: %%w", label, i)
				g.body.writef("var f%d %s\n", i, g.goTypeExpr(f))
				g.body.writef("if err := jsonUnmarshalInto(values[%d], &f%d); err != nil { return nil, fmt.Errorf(%q, err) }\n", i, i, msg)
			}
			g.body.writef("return %s(%s{", e.Name, goName)
			for i := range v.Fields {
				if i > 0 {
					g.body.write(", ")
				}
				g.body.writef("F%d: f%d", i, i)
			}
			g.body.writeln("}), nil")
		}
		g.body.dedent()
	}
	g.body.dedent()
	g.body.writeln("}")
	g.body.writef("return nil, fmt.Errorf(\"std.json: unknown %s tag %%q\", head.Tag)\n", e.Name)
	g.body.dedent()
	g.body.writeln("}")
}

func (g *gen) emitEnumJSONCustomDecoder(e *ast.EnumDecl) {
	g.body.nl()
	g.body.writeln("func init() {")
	g.body.indent()
	g.body.writef("jsonRegisterDecoder(reflect.TypeOf((*%s)(nil)).Elem(), func(data []byte) (any, error) {\n", e.Name)
	g.body.indent()
	g.body.writeln("value, err := jsonParseValue(data)")
	g.body.writeln("if err != nil {")
	g.body.indent()
	g.body.writeln("return nil, err")
	g.body.dedent()
	g.body.writeln("}")
	g.body.writef("result := %s_fromJson(value)\n", e.Name)
	g.body.writeln("if !result.IsOk {")
	g.body.indent()
	g.body.writeln("return nil, jsonAsError(result.Error)")
	g.body.dedent()
	g.body.writeln("}")
	g.body.writeln("return result.Value, nil")
	g.body.dedent()
	g.body.writeln("})")
	g.body.dedent()
	g.body.writeln("}")
}

func hasMethodNamed(methods []*ast.FnDecl, name string) bool {
	return methodNamed(methods, name) != nil
}

func methodNamed(methods []*ast.FnDecl, name string) *ast.FnDecl {
	for _, m := range methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

func receiverMethodNamed(methods []*ast.FnDecl, name string) *ast.FnDecl {
	for _, m := range methods {
		if m.Name == name && m.Recv != nil {
			return m
		}
	}
	return nil
}

func associatedMethodNamed(methods []*ast.FnDecl, name string) *ast.FnDecl {
	for _, m := range methods {
		if m.Name == name && m.Recv == nil {
			return m
		}
	}
	return nil
}

func genericReceiverName(name string, generics []*ast.GenericParam) string {
	if len(generics) == 0 {
		return name
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('[')
	for i, p := range generics {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
	}
	b.WriteByte(']')
	return b.String()
}

func (g *gen) jsonTypeAllowsNull(t ast.Type) bool {
	switch t := t.(type) {
	case *ast.OptionalType:
		return true
	case *ast.NamedType:
		if len(t.Path) == 1 {
			switch t.Path[0] {
			case "Option", "Json":
				return true
			}
		}
		if len(t.Path) == 2 && t.Path[1] == "Json" && g.isStdlibAliasName(t.Path[0], "json") {
			return true
		}
	}
	return false
}

func jsonOptions(annots []*ast.Annotation, fallback string) jsonAnnotationOptions {
	opts := jsonAnnotationOptions{name: fallback}
	for _, ann := range annots {
		if ann.Name != "json" {
			continue
		}
		for _, arg := range ann.Args {
			switch arg.Key {
			case "key":
				if s, ok := annotationString(arg.Value); ok {
					opts.name = s
				}
			case "skip":
				opts.skip = true
			case "optional":
				opts.optional = true
			}
		}
	}
	return opts
}

func annotationString(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, p := range lit.Parts {
		if !p.IsLit {
			return "", false
		}
		b.WriteString(p.Lit)
	}
	return b.String(), true
}
