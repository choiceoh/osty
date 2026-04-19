package check

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

func selfhostPackageCheckInput(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider, layout selfhostCheckedSource) selfhost.PackageCheckInput {
	input := selfhost.PackageCheckInput{
		Files:   make([]selfhost.PackageCheckFile, 0, len(layout.files)),
		Imports: selfhostPackageImportSurfaces(pkg, ws, stdlib),
	}
	segmentIdx := 0
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		src := pf.CheckerSource()
		if len(src) == 0 {
			continue
		}
		base := 0
		if segmentIdx < len(layout.files) {
			base = layout.files[segmentIdx].base
		}
		name := ""
		if pf.Path != "" {
			name = filepath.Base(pf.Path)
		}
		input.Files = append(input.Files, selfhost.PackageCheckFile{
			Source:    append([]byte(nil), src...),
			File:      pf.File,
			SourceMap: pf.CanonicalMap,
			Base:      base,
			Name:      name,
		})
		segmentIdx++
	}
	return input
}

func selfhostPackageImportSurfaces(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) []selfhost.PackageCheckImport {
	if pkg == nil {
		return nil
	}
	seen := map[string]string{}
	var out []selfhost.PackageCheckImport
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, use := range pf.File.Uses {
			target := selfhostLookupPackageImport(use, ws, stdlib)
			if target == nil {
				continue
			}
			alias := selfhostUseAlias(use)
			if alias == "" {
				continue
			}
			key := strings.Join(use.Path, ".")
			if prev, ok := seen[alias]; ok {
				if prev == key {
					continue
				}
				continue
			}
			seen[alias] = key
			out = append(out, selfhostBuildImportSurface(alias, target))
		}
	}
	return out
}

func selfhostLookupPackageImport(use *ast.UseDecl, ws *resolve.Workspace, stdlib resolve.StdlibProvider) *resolve.Package {
	if use == nil {
		return nil
	}
	dotPath := strings.Join(use.Path, ".")
	if dotPath == "" {
		return nil
	}
	if ws != nil {
		if target := ws.Packages[dotPath]; target != nil {
			return target
		}
		if ws.Stdlib != nil {
			if target := ws.Stdlib.LookupPackage(dotPath); target != nil {
				return target
			}
		}
	}
	if stdlib != nil {
		return stdlib.LookupPackage(dotPath)
	}
	return nil
}

func selfhostUseAlias(use *ast.UseDecl) string {
	if use == nil {
		return ""
	}
	if use.Alias != "" {
		return use.Alias
	}
	if len(use.Path) == 0 {
		return ""
	}
	return use.Path[len(use.Path)-1]
}

func selfhostBuildImportSurface(alias string, pkg *resolve.Package) selfhost.PackageCheckImport {
	surface := selfhost.PackageCheckImport{Alias: alias}
	localTypes := selfhostImportedTypeNames(alias, pkg)
	for _, qualified := range localTypes {
		surface.Fields = append(surface.Fields, selfhost.PackageCheckField{
			Owner:      alias,
			Name:       selfhostLocalTypeName(qualified),
			TypeName:   qualified,
			HasDefault: true,
		})
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			switch d := decl.(type) {
			case *ast.FnDecl:
				if d.Pub && d.Recv == nil {
					surface.Functions = append(surface.Functions, selfhostBuildImportedFn(alias, localTypes, "", nil, nil, d))
					surface.Fields = append(surface.Fields, selfhost.PackageCheckField{
						Owner:      alias,
						Name:       d.Name,
						TypeName:   selfhostFnTypeSource(alias, localTypes, nil, d.Params, d.ReturnType),
						HasDefault: true,
					})
				}
			case *ast.StructDecl:
				if d.Pub {
					selfhostAppendImportedStruct(&surface, alias, localTypes, d)
				}
			case *ast.EnumDecl:
				if d.Pub {
					selfhostAppendImportedEnum(&surface, alias, localTypes, d)
				}
			case *ast.InterfaceDecl:
				if d.Pub {
					selfhostAppendImportedInterface(&surface, alias, localTypes, d)
				}
			case *ast.TypeAliasDecl:
				if d.Pub {
					selfhostAppendImportedAlias(&surface, alias, localTypes, d)
				}
			case *ast.LetDecl:
				if d.Pub {
					typeName := ""
					if d.Type != nil {
						typeName = selfhostImportedTypeSource(localTypes, nil, d.Type)
					}
					surface.Fields = append(surface.Fields, selfhost.PackageCheckField{
						Owner:      alias,
						Name:       d.Name,
						TypeName:   typeName,
						HasDefault: true,
					})
				}
			}
		}
	}
	return surface
}

func selfhostAppendImportedStruct(surface *selfhost.PackageCheckImport, alias string, localTypes map[string]string, decl *ast.StructDecl) {
	name := alias + "." + decl.Name
	generics := selfhostGenericNames(decl.Generics)
	bounds := selfhostGenericBounds(localTypes, generics, decl.Generics)
	surface.TypeDecls = append(surface.TypeDecls, selfhost.PackageCheckType{
		Name:          name,
		Kind:          "struct",
		Generics:      generics,
		GenericBounds: bounds,
	})
	scopeGenerics := selfhostGenericSet(generics)
	for _, field := range decl.Fields {
		if field == nil {
			continue
		}
		surface.Fields = append(surface.Fields, selfhost.PackageCheckField{
			Owner:      name,
			Name:       field.Name,
			TypeName:   selfhostImportedTypeSource(localTypes, scopeGenerics, field.Type),
			HasDefault: field.Default != nil,
		})
	}
	for _, method := range decl.Methods {
		if method != nil {
			surface.Functions = append(surface.Functions, selfhostBuildImportedFn(alias, localTypes, name, generics, bounds, method))
		}
	}
}

func selfhostAppendImportedEnum(surface *selfhost.PackageCheckImport, alias string, localTypes map[string]string, decl *ast.EnumDecl) {
	name := alias + "." + decl.Name
	generics := selfhostGenericNames(decl.Generics)
	bounds := selfhostGenericBounds(localTypes, generics, decl.Generics)
	surface.TypeDecls = append(surface.TypeDecls, selfhost.PackageCheckType{
		Name:          name,
		Kind:          "enum",
		Generics:      generics,
		GenericBounds: bounds,
	})
	scopeGenerics := selfhostGenericSet(generics)
	for _, variant := range decl.Variants {
		if variant == nil {
			continue
		}
		fields := make([]string, 0, len(variant.Fields))
		for _, fieldTy := range variant.Fields {
			fields = append(fields, selfhostImportedTypeSource(localTypes, scopeGenerics, fieldTy))
		}
		surface.Variants = append(surface.Variants, selfhost.PackageCheckVariant{
			Owner:      name,
			Name:       variant.Name,
			FieldTypes: fields,
			Generics:   append([]string(nil), generics...),
		})
	}
	for _, method := range decl.Methods {
		if method != nil {
			surface.Functions = append(surface.Functions, selfhostBuildImportedFn(alias, localTypes, name, generics, bounds, method))
		}
	}
}

func selfhostAppendImportedInterface(surface *selfhost.PackageCheckImport, alias string, localTypes map[string]string, decl *ast.InterfaceDecl) {
	name := alias + "." + decl.Name
	generics := selfhostGenericNames(decl.Generics)
	bounds := selfhostGenericBounds(localTypes, generics, decl.Generics)
	surface.TypeDecls = append(surface.TypeDecls, selfhost.PackageCheckType{
		Name:          name,
		Kind:          "interface",
		Generics:      generics,
		GenericBounds: bounds,
	})
	surface.RegisterAsIface = append(surface.RegisterAsIface, name)
	scopeGenerics := selfhostGenericSet(generics)
	for _, ext := range decl.Extends {
		surface.InterfaceExts = append(surface.InterfaceExts, selfhost.PackageCheckInterfaceExt{
			Owner:         name,
			InterfaceType: selfhostImportedTypeSource(localTypes, scopeGenerics, ext),
		})
	}
	for _, method := range decl.Methods {
		if method != nil {
			surface.Functions = append(surface.Functions, selfhostBuildImportedFn(alias, localTypes, name, generics, bounds, method))
		}
	}
}

func selfhostAppendImportedAlias(surface *selfhost.PackageCheckImport, alias string, localTypes map[string]string, decl *ast.TypeAliasDecl) {
	name := alias + "." + decl.Name
	generics := selfhostGenericNames(decl.Generics)
	surface.TypeDecls = append(surface.TypeDecls, selfhost.PackageCheckType{
		Name:          name,
		Kind:          "alias",
		Generics:      generics,
		GenericBounds: selfhostGenericBounds(localTypes, generics, decl.Generics),
	})
	surface.Aliases = append(surface.Aliases, selfhost.PackageCheckAlias{
		Name:     name,
		Target:   selfhostImportedTypeSource(localTypes, selfhostGenericSet(generics), decl.Target),
		Generics: generics,
	})
}

func selfhostBuildImportedFn(
	alias string,
	localTypes map[string]string,
	owner string,
	ownerGenerics []string,
	ownerBounds []selfhost.PackageCheckGenericBound,
	fn *ast.FnDecl,
) selfhost.PackageCheckFn {
	fnGenerics := selfhostGenericNames(fn.Generics)
	combinedGenerics := append(append([]string(nil), ownerGenerics...), fnGenerics...)
	scopeGenerics := selfhostGenericSet(combinedGenerics)
	paramNames := make([]string, 0, len(fn.Params))
	paramTypes := make([]string, 0, len(fn.Params))
	for i, param := range fn.Params {
		if param == nil {
			continue
		}
		name := param.Name
		if name == "" {
			name = fmt.Sprintf("arg%d", i)
		}
		paramNames = append(paramNames, name)
		paramTypes = append(paramTypes, selfhostImportedTypeSource(localTypes, scopeGenerics, param.Type))
	}
	bounds := append([]selfhost.PackageCheckGenericBound(nil), ownerBounds...)
	bounds = append(bounds, selfhostGenericBounds(localTypes, combinedGenerics, fn.Generics)...)
	receiverType := ""
	if owner != "" {
		receiverType = selfhostNamedTypeSource(owner, ownerGenerics)
	}
	return selfhost.PackageCheckFn{
		Name:          fn.Name,
		Owner:         owner,
		ReceiverType:  receiverType,
		ReturnType:    selfhostImportedTypeSource(localTypes, scopeGenerics, fn.ReturnType),
		ParamNames:    paramNames,
		ParamTypes:    paramTypes,
		Generics:      combinedGenerics,
		GenericBounds: bounds,
	}
}

func selfhostImportedTypeNames(alias string, pkg *resolve.Package) map[string]string {
	out := map[string]string{}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			switch d := decl.(type) {
			case *ast.StructDecl:
				out[d.Name] = alias + "." + d.Name
			case *ast.EnumDecl:
				out[d.Name] = alias + "." + d.Name
			case *ast.InterfaceDecl:
				out[d.Name] = alias + "." + d.Name
			case *ast.TypeAliasDecl:
				out[d.Name] = alias + "." + d.Name
			}
		}
	}
	return out
}

func selfhostLocalTypeName(qualified string) string {
	if idx := strings.LastIndexByte(qualified, '.'); idx >= 0 && idx+1 < len(qualified) {
		return qualified[idx+1:]
	}
	return qualified
}

func selfhostGenericNames(gps []*ast.GenericParam) []string {
	out := make([]string, 0, len(gps))
	for _, gp := range gps {
		if gp == nil || gp.Name == "" {
			continue
		}
		out = append(out, gp.Name)
	}
	return out
}

func selfhostGenericSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func selfhostGenericBounds(localTypes map[string]string, scopeGenerics []string, gps []*ast.GenericParam) []selfhost.PackageCheckGenericBound {
	out := make([]selfhost.PackageCheckGenericBound, 0)
	generics := selfhostGenericSet(scopeGenerics)
	for _, gp := range gps {
		if gp == nil || gp.Name == "" {
			continue
		}
		for _, constraint := range gp.Constraints {
			out = append(out, selfhost.PackageCheckGenericBound{
				TyParam:       gp.Name,
				InterfaceType: selfhostImportedTypeSource(localTypes, generics, constraint),
			})
		}
	}
	return out
}

func selfhostImportedTypeSource(localTypes map[string]string, generics map[string]struct{}, t ast.Type) string {
	switch x := t.(type) {
	case nil:
		return "()"
	case *ast.NamedType:
		name := strings.Join(x.Path, ".")
		if name == "" {
			return "Invalid"
		}
		if len(x.Path) == 1 {
			if _, ok := generics[name]; ok {
				// keep generic names bare
			} else if qualified := localTypes[name]; qualified != "" {
				name = qualified
			}
		}
		if len(x.Args) == 0 {
			return name
		}
		args := make([]string, 0, len(x.Args))
		for _, arg := range x.Args {
			args = append(args, selfhostImportedTypeSource(localTypes, generics, arg))
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *ast.OptionalType:
		return selfhostImportedTypeSource(localTypes, generics, x.Inner) + "?"
	case *ast.TupleType:
		elems := make([]string, 0, len(x.Elems))
		for _, elem := range x.Elems {
			elems = append(elems, selfhostImportedTypeSource(localTypes, generics, elem))
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case *ast.FnType:
		params := make([]string, 0, len(x.Params))
		for _, param := range x.Params {
			params = append(params, selfhostImportedTypeSource(localTypes, generics, param))
		}
		out := "fn(" + strings.Join(params, ", ") + ")"
		if x.ReturnType != nil {
			out += " -> " + selfhostImportedTypeSource(localTypes, generics, x.ReturnType)
		}
		return out
	default:
		return selfhostTypeSource(t)
	}
}

func selfhostNamedTypeSource(name string, generics []string) string {
	if len(generics) == 0 {
		return name
	}
	return name + "<" + strings.Join(generics, ", ") + ">"
}

func selfhostFnTypeSource(alias string, localTypes map[string]string, generics map[string]struct{}, params []*ast.Param, ret ast.Type) string {
	paramTypes := make([]string, 0, len(params))
	for _, param := range params {
		if param == nil {
			continue
		}
		paramTypes = append(paramTypes, selfhostImportedTypeSource(localTypes, generics, param.Type))
	}
	out := "fn(" + strings.Join(paramTypes, ", ") + ")"
	if ret != nil {
		out += " -> " + selfhostImportedTypeSource(localTypes, generics, ret)
	}
	return out
}
