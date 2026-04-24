package selfhost

import (
	"fmt"
	"sort"
	"strings"
)

// PackageUseRef captures the information needed to resolve a `use` decl
// across the workspace without exposing the self-host arena to callers.
// Alias is either the explicit `as`-name or, when none was given, the
// trailing segment of Path; empty for stanzas that have no natural alias
// (e.g. `use go ...` / `use c ...` foreign bodies).
type PackageUseRef struct {
	Path  string
	Alias string
	IsGo  bool
}

// PackageUsesFromRun walks run's AstArena and returns one PackageUseRef
// per top-level `use` decl (expanding `use a::{b, c}` groups). When run
// or its arena is nil the result is empty.
func PackageUsesFromRun(run *FrontendRun) []PackageUseRef {
	if run == nil || run.parser == nil || run.parser.arena == nil {
		return nil
	}
	arena := run.parser.arena
	var out []PackageUseRef
	for _, declIdx := range arena.decls {
		if declIdx < 0 || declIdx >= len(arena.nodes) {
			continue
		}
		n := arena.nodes[declIdx]
		if n == nil {
			continue
		}
		if _, ok := n.kind.(*AstNodeKind_AstNUseDecl); !ok {
			continue
		}
		out = appendArenaUseRefs(out, arena, n)
	}
	return out
}

func appendArenaUseRefs(out []PackageUseRef, arena *AstArena, n *AstNode) []PackageUseRef {
	// A scoped `use a::{b, c}` group flags its node with
	// astUseDeclGroupMarker (flags bit beyond the go/pub bits) and holds
	// the child use decls in `children`. Every other shape is a regular
	// single-path use.
	if arenaUseDeclIsGroup(n) {
		for _, childIdx := range n.children {
			if childIdx < 0 || childIdx >= len(arena.nodes) {
				continue
			}
			child := arena.nodes[childIdx]
			if child == nil {
				continue
			}
			if _, ok := child.kind.(*AstNodeKind_AstNUseDecl); !ok {
				continue
			}
			out = appendArenaUseRefs(out, arena, child)
		}
		return out
	}
	ref := PackageUseRef{
		Path:  arenaStringUnquote(n.text),
		IsGo:  arenaUseDeclIsGo(n),
		Alias: arenaUseDeclAlias(arena, n),
	}
	if ref.Path == "" {
		return out
	}
	if ref.Alias == "" {
		ref.Alias = arenaUsePathLastSegment(ref.Path)
	}
	out = append(out, ref)
	return out
}

func arenaUseDeclIsGo(n *AstNode) bool {
	if n == nil {
		return false
	}
	return n.flags&1 != 0
}

// arenaUseDeclIsGroup mirrors astUseDeclIsGroup: a synthetic wrapper use
// decl parsed from `use a::{b, c}` stamps the group marker into `extra`
// (not flags — those are the go / pub bits) so the children list holds
// the individual child uses. See toolchain/parser.osty:147-151.
func arenaUseDeclIsGroup(n *AstNode) bool {
	if n == nil {
		return false
	}
	return n.extra == 1
}

func arenaUseDeclAlias(arena *AstArena, n *AstNode) string {
	if n == nil || len(n.children2) == 0 {
		return ""
	}
	idx := n.children2[0]
	if idx < 0 || idx >= len(arena.nodes) {
		return ""
	}
	aliasNode := arena.nodes[idx]
	if aliasNode == nil {
		return ""
	}
	return aliasNode.text
}

func arenaUsePathLastSegment(path string) string {
	if path == "" {
		return ""
	}
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 && idx+1 < len(path) {
		return path[idx+1:]
	}
	if idx := strings.LastIndexByte(path, '.'); idx >= 0 && idx+1 < len(path) {
		return path[idx+1:]
	}
	return path
}

// arenaStringUnquote mirrors astLowerUnquoteMaybe: the parser stores raw
// string tokens verbatim (including surrounding quotes) for `use go` /
// `use c` stanzas. Dotted use paths are already unquoted.
func arenaStringUnquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// PackageImportSurface builds a cross-package import surface by walking
// the AstArena of each run. The result is equivalent to the *ast.File-
// based selfhostBuildImportSurface in internal/check but reads arenas
// directly so no astbridge lowering is triggered.
//
// runs should be one *FrontendRun per source file in the imported
// package. Nil entries are skipped. Callers outside this package
// typically obtain runs from resolve.PackageFile.Run (populated by
// LoadPackageForNative) or by calling selfhost.Run(pf.Source) when Run
// is not yet available.
func PackageImportSurface(alias string, runs []*FrontendRun) PackageCheckImport {
	surface := PackageCheckImport{Alias: alias}
	if alias == "" || len(runs) == 0 {
		return surface
	}
	localTypes := arenaLocalTypeNames(alias, runs)
	for _, qualified := range arenaSortedMapValues(localTypes) {
		surface.Fields = append(surface.Fields, PackageCheckField{
			Owner:      alias,
			Name:       arenaLocalTypeName(qualified),
			TypeName:   qualified,
			HasDefault: true,
		})
	}
	for _, run := range runs {
		if run == nil || run.parser == nil || run.parser.arena == nil {
			continue
		}
		arena := run.parser.arena
		for _, declIdx := range arena.decls {
			if declIdx < 0 || declIdx >= len(arena.nodes) {
				continue
			}
			n := arena.nodes[declIdx]
			if n == nil {
				continue
			}
			switch n.kind.(type) {
			case *AstNodeKind_AstNFnDecl:
				if n.flags != 1 {
					continue
				}
				if arenaFnDeclHasReceiver(arena, n) {
					continue
				}
				// Register twice: once as a free function (owner="") and
				// once as an alias-method (owner=alias) so `core.badge(sig)`
				// dispatches through checkLookupMethod with receiver type
				// `core`. The alias-method form has no receiver parameter.
				free := arenaBuildImportedFn(arena, localTypes, alias, "", nil, nil, n)
				surface.Functions = append(surface.Functions, free)
				aliasFn := free
				aliasFn.Owner = alias
				surface.Functions = append(surface.Functions, aliasFn)
				surface.Fields = append(surface.Fields, PackageCheckField{
					Owner:      alias,
					Name:       n.text,
					TypeName:   arenaFnTypeSource(arena, localTypes, nil, n),
					HasDefault: true,
				})
			case *AstNodeKind_AstNStructDecl:
				if n.flags == 1 {
					arenaAppendImportedStruct(&surface, arena, alias, localTypes, n)
				}
			case *AstNodeKind_AstNEnumDecl:
				if n.flags == 1 {
					arenaAppendImportedEnum(&surface, arena, alias, localTypes, n)
				}
			case *AstNodeKind_AstNInterfaceDecl:
				if n.flags == 1 {
					arenaAppendImportedInterface(&surface, arena, alias, localTypes, n)
				}
			case *AstNodeKind_AstNTypeAlias:
				if n.flags == 1 {
					arenaAppendImportedAlias(&surface, arena, alias, localTypes, n)
				}
			case *AstNodeKind_AstNLet:
				if !arenaIsPubLet(run, n) {
					continue
				}
				typeName := ""
				if len(n.children) > 0 && n.children[0] >= 0 {
					typeName = arenaImportedTypeSource(arena, localTypes, nil, n.children[0])
				}
				surface.Fields = append(surface.Fields, PackageCheckField{
					Owner:      alias,
					Name:       arenaLetDeclName(arena, n),
					TypeName:   typeName,
					HasDefault: true,
				})
			}
		}
	}
	return surface
}

// arenaSortedMapValues returns map values keyed by sorted key order so
// the surface we emit is deterministic across runs — the checker keys
// several downstream maps off field insertion order and a churning
// surface would destabilise telemetry / golden snapshots.
func arenaSortedMapValues(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}

func arenaFnDeclHasReceiver(arena *AstArena, n *AstNode) bool {
	if n == nil || len(n.children) == 0 {
		return false
	}
	idx := n.children[0]
	if idx < 0 || idx >= len(arena.nodes) {
		return false
	}
	child := arena.nodes[idx]
	if child == nil {
		return false
	}
	if _, ok := child.kind.(*AstNodeKind_AstNParam); !ok {
		return false
	}
	return child.text == "self"
}

func arenaLocalTypeNames(alias string, runs []*FrontendRun) map[string]string {
	out := map[string]string{}
	for _, run := range runs {
		if run == nil || run.parser == nil || run.parser.arena == nil {
			continue
		}
		arena := run.parser.arena
		for _, declIdx := range arena.decls {
			if declIdx < 0 || declIdx >= len(arena.nodes) {
				continue
			}
			n := arena.nodes[declIdx]
			if n == nil {
				continue
			}
			switch n.kind.(type) {
			case *AstNodeKind_AstNStructDecl, *AstNodeKind_AstNEnumDecl,
				*AstNodeKind_AstNInterfaceDecl, *AstNodeKind_AstNTypeAlias:
				if n.text == "" {
					continue
				}
				out[n.text] = alias + "." + n.text
			}
		}
	}
	return out
}

func arenaLocalTypeName(qualified string) string {
	if idx := strings.LastIndexByte(qualified, '.'); idx >= 0 && idx+1 < len(qualified) {
		return qualified[idx+1:]
	}
	return qualified
}

func arenaAppendImportedStruct(surface *PackageCheckImport, arena *AstArena, alias string, localTypes map[string]string, n *AstNode) {
	name := alias + "." + n.text
	generics := arenaGenericNames(arena, n.children2)
	bounds := arenaGenericBounds(arena, localTypes, generics, n.children2)
	surface.TypeDecls = append(surface.TypeDecls, PackageCheckType{
		Name:          name,
		Kind:          "struct",
		Generics:      generics,
		GenericBounds: bounds,
	})
	scopeGenerics := arenaGenericSet(generics)
	for _, childIdx := range n.children {
		if childIdx < 0 || childIdx >= len(arena.nodes) {
			continue
		}
		child := arena.nodes[childIdx]
		if child == nil {
			continue
		}
		switch child.kind.(type) {
		case *AstNodeKind_AstNField_:
			fieldType := ""
			if child.right >= 0 {
				fieldType = arenaImportedTypeSource(arena, localTypes, scopeGenerics, child.right)
			}
			surface.Fields = append(surface.Fields, PackageCheckField{
				Owner:      name,
				Name:       child.text,
				TypeName:   fieldType,
				Exported:   child.flags == 1,
				HasDefault: child.left >= 0,
			})
		case *AstNodeKind_AstNFnDecl:
			surface.Functions = append(surface.Functions, arenaBuildImportedFn(arena, localTypes, alias, name, generics, bounds, child))
		}
	}
}

func arenaAppendImportedEnum(surface *PackageCheckImport, arena *AstArena, alias string, localTypes map[string]string, n *AstNode) {
	name := alias + "." + n.text
	generics := arenaGenericNames(arena, n.children2)
	bounds := arenaGenericBounds(arena, localTypes, generics, n.children2)
	surface.TypeDecls = append(surface.TypeDecls, PackageCheckType{
		Name:          name,
		Kind:          "enum",
		Generics:      generics,
		GenericBounds: bounds,
	})
	scopeGenerics := arenaGenericSet(generics)
	for _, childIdx := range n.children {
		if childIdx < 0 || childIdx >= len(arena.nodes) {
			continue
		}
		child := arena.nodes[childIdx]
		if child == nil {
			continue
		}
		switch child.kind.(type) {
		case *AstNodeKind_AstNVariant:
			fields := make([]string, 0, len(child.children))
			for _, typeIdx := range child.children {
				fields = append(fields, arenaImportedTypeSource(arena, localTypes, scopeGenerics, typeIdx))
			}
			surface.Variants = append(surface.Variants, PackageCheckVariant{
				Owner:      name,
				Name:       child.text,
				FieldTypes: fields,
				Generics:   append([]string(nil), generics...),
			})
		case *AstNodeKind_AstNFnDecl:
			surface.Functions = append(surface.Functions, arenaBuildImportedFn(arena, localTypes, alias, name, generics, bounds, child))
		}
	}
}

func arenaAppendImportedInterface(surface *PackageCheckImport, arena *AstArena, alias string, localTypes map[string]string, n *AstNode) {
	name := alias + "." + n.text
	generics := arenaGenericNames(arena, n.children2)
	bounds := arenaGenericBounds(arena, localTypes, generics, n.children2)
	surface.TypeDecls = append(surface.TypeDecls, PackageCheckType{
		Name:          name,
		Kind:          "interface",
		Generics:      generics,
		GenericBounds: bounds,
	})
	surface.RegisterAsIface = append(surface.RegisterAsIface, name)
	scopeGenerics := arenaGenericSet(generics)
	for _, childIdx := range n.children {
		if childIdx < 0 || childIdx >= len(arena.nodes) {
			continue
		}
		child := arena.nodes[childIdx]
		if child == nil {
			continue
		}
		if _, isFn := child.kind.(*AstNodeKind_AstNFnDecl); isFn {
			surface.Functions = append(surface.Functions, arenaBuildImportedFn(arena, localTypes, alias, name, generics, bounds, child))
			continue
		}
		// Non-fn members on an interface are `extends` type references.
		surface.InterfaceExts = append(surface.InterfaceExts, PackageCheckInterfaceExt{
			Owner:         name,
			InterfaceType: arenaImportedTypeSource(arena, localTypes, scopeGenerics, childIdx),
		})
	}
}

func arenaAppendImportedAlias(surface *PackageCheckImport, arena *AstArena, alias string, localTypes map[string]string, n *AstNode) {
	name := alias + "." + n.text
	// TypeAliasDecl holds its generic params in `children` (NOT children2 —
	// see opParseTypeAliasDecl in toolchain/parser.osty:2240).
	generics := arenaGenericNames(arena, n.children)
	surface.TypeDecls = append(surface.TypeDecls, PackageCheckType{
		Name:          name,
		Kind:          "alias",
		Generics:      generics,
		GenericBounds: arenaGenericBounds(arena, localTypes, generics, n.children),
	})
	target := ""
	if n.left >= 0 {
		target = arenaImportedTypeSource(arena, localTypes, arenaGenericSet(generics), n.left)
	}
	surface.Aliases = append(surface.Aliases, PackageCheckAlias{
		Name:     name,
		Target:   target,
		Generics: generics,
	})
}

func arenaBuildImportedFn(arena *AstArena, localTypes map[string]string, alias, owner string, ownerGenerics []string, ownerBounds []PackageCheckGenericBound, n *AstNode) PackageCheckFn {
	fnGenerics := arenaGenericNames(arena, n.children2)
	combinedGenerics := append(append([]string(nil), ownerGenerics...), fnGenerics...)
	scopeGenerics := arenaGenericSet(combinedGenerics)
	paramNames := make([]string, 0, len(n.children))
	paramTypes := make([]string, 0, len(n.children))
	paramDefaults := make([]bool, 0, len(n.children))
	argIdx := 0
	for i, childIdx := range n.children {
		if childIdx < 0 || childIdx >= len(arena.nodes) {
			continue
		}
		child := arena.nodes[childIdx]
		if child == nil {
			continue
		}
		if _, ok := child.kind.(*AstNodeKind_AstNParam); !ok {
			continue
		}
		// Receiver (leading `self`) is not part of the import surface's
		// ParamNames/Types — methods register a receiver type via
		// PackageCheckFn.ReceiverType below.
		if i == 0 && child.text == "self" {
			continue
		}
		name := child.text
		if name == "" {
			name = fmt.Sprintf("arg%d", argIdx)
		}
		argIdx++
		hasDefault := arenaParamHasDefault(child)
		// Encode default-availability into the name with a leading "?"
		// so the checker's arity check can treat missing trailing args as
		// satisfied by defaults without threading a parallel List<Bool>
		// through every CheckFnSig constructor. (See the *ast.File-based
		// selfhostBuildImportedFn for the rationale — the checker peeks
		// at the "?" prefix directly.)
		if hasDefault {
			name = "?" + name
		}
		paramNames = append(paramNames, name)
		typeSrc := ""
		if child.right >= 0 {
			typeSrc = arenaImportedTypeSource(arena, localTypes, scopeGenerics, child.right)
		}
		paramTypes = append(paramTypes, typeSrc)
		paramDefaults = append(paramDefaults, hasDefault)
	}
	bounds := append([]PackageCheckGenericBound(nil), ownerBounds...)
	bounds = append(bounds, arenaGenericBounds(arena, localTypes, combinedGenerics, n.children2)...)
	receiverType := ""
	if owner != "" {
		receiverType = arenaNamedTypeSource(owner, ownerGenerics)
	}
	returnType := ""
	if n.left >= 0 {
		returnType = arenaImportedTypeSource(arena, localTypes, scopeGenerics, n.left)
	}
	return PackageCheckFn{
		Name:          n.text,
		Owner:         owner,
		ReceiverType:  receiverType,
		ReturnType:    returnType,
		HasBody:       n.right >= 0,
		ParamNames:    paramNames,
		ParamTypes:    paramTypes,
		ParamDefaults: paramDefaults,
		Generics:      combinedGenerics,
		GenericBounds: bounds,
	}
}

// arenaParamHasDefault returns true when the param node carries a default
// expression. Per toolchain/parser.osty:2072, AstNParam encodes the
// default in `left` when `text != ""` (the non-empty name distinguishes a
// default expression from a destructuring pattern stored in the same
// slot).
func arenaParamHasDefault(n *AstNode) bool {
	if n == nil || n.text == "" {
		return false
	}
	return n.left >= 0
}

func arenaGenericNames(arena *AstArena, idxs []int) []string {
	out := make([]string, 0, len(idxs))
	for _, idx := range idxs {
		if idx < 0 || idx >= len(arena.nodes) {
			continue
		}
		n := arena.nodes[idx]
		if n == nil || n.text == "" {
			continue
		}
		if _, ok := n.kind.(*AstNodeKind_AstNGenericParam); !ok {
			continue
		}
		out = append(out, n.text)
	}
	return out
}

func arenaGenericSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func arenaGenericBounds(arena *AstArena, localTypes map[string]string, scopeGenerics []string, idxs []int) []PackageCheckGenericBound {
	out := make([]PackageCheckGenericBound, 0)
	generics := arenaGenericSet(scopeGenerics)
	for _, idx := range idxs {
		if idx < 0 || idx >= len(arena.nodes) {
			continue
		}
		n := arena.nodes[idx]
		if n == nil || n.text == "" {
			continue
		}
		if _, ok := n.kind.(*AstNodeKind_AstNGenericParam); !ok {
			continue
		}
		for _, constraintIdx := range n.children {
			out = append(out, PackageCheckGenericBound{
				TyParam:       n.text,
				InterfaceType: arenaImportedTypeSource(arena, localTypes, generics, constraintIdx),
			})
		}
	}
	return out
}

// arenaImportedTypeSource renders the type node at idx back into Osty
// source text, qualifying bare type names through localTypes when they
// are neither generic-scope names nor already qualified. This mirrors
// selfhostImportedTypeSource's branching on *ast.Type variants.
//
// Type nodes are discriminated by `text`:
//   - "optional" → OptionalType (inner at `left`)
//   - "tuple"    → TupleType    (elements in `children`)
//   - "fn"       → FnType       (param types in `children`, return in `right`)
//   - else       → NamedType    (dotted path in `text`, type args in `children`)
func arenaImportedTypeSource(arena *AstArena, localTypes map[string]string, generics map[string]struct{}, idx int) string {
	if idx < 0 || idx >= len(arena.nodes) {
		return "()"
	}
	n := arena.nodes[idx]
	if n == nil {
		return "()"
	}
	switch n.text {
	case "optional":
		return arenaImportedTypeSource(arena, localTypes, generics, n.left) + "?"
	case "tuple":
		elems := make([]string, 0, len(n.children))
		for _, childIdx := range n.children {
			elems = append(elems, arenaImportedTypeSource(arena, localTypes, generics, childIdx))
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case "fn":
		params := make([]string, 0, len(n.children))
		for _, childIdx := range n.children {
			params = append(params, arenaImportedTypeSource(arena, localTypes, generics, childIdx))
		}
		out := "fn(" + strings.Join(params, ", ") + ")"
		if n.right >= 0 {
			out += " -> " + arenaImportedTypeSource(arena, localTypes, generics, n.right)
		}
		return out
	}
	name := n.text
	if name == "" {
		return "Invalid"
	}
	// Only requalify bare (non-dotted) type names — qualified `std.fs.File`
	// style references already point past the alias layer.
	if !strings.ContainsRune(name, '.') {
		if _, isGeneric := generics[name]; !isGeneric {
			if qualified := localTypes[name]; qualified != "" {
				name = qualified
			}
		}
	}
	if len(n.children) == 0 {
		return name
	}
	args := make([]string, 0, len(n.children))
	for _, childIdx := range n.children {
		args = append(args, arenaImportedTypeSource(arena, localTypes, generics, childIdx))
	}
	return name + "<" + strings.Join(args, ", ") + ">"
}

func arenaNamedTypeSource(name string, generics []string) string {
	if len(generics) == 0 {
		return name
	}
	return name + "<" + strings.Join(generics, ", ") + ">"
}

// arenaFnTypeSource renders the synthetic `fn(T1, T2) -> R` string for a
// bare free function registered as an alias field (e.g. `core.badge` as
// a first-class value). Mirrors selfhostFnTypeSource: walks the fn
// decl's param + return types without applying any local generic scope.
func arenaFnTypeSource(arena *AstArena, localTypes map[string]string, generics map[string]struct{}, fnNode *AstNode) string {
	paramTypes := make([]string, 0, len(fnNode.children))
	for i, childIdx := range fnNode.children {
		if childIdx < 0 || childIdx >= len(arena.nodes) {
			continue
		}
		child := arena.nodes[childIdx]
		if child == nil {
			continue
		}
		if _, ok := child.kind.(*AstNodeKind_AstNParam); !ok {
			continue
		}
		if i == 0 && child.text == "self" {
			continue
		}
		if child.right < 0 {
			paramTypes = append(paramTypes, "()")
			continue
		}
		paramTypes = append(paramTypes, arenaImportedTypeSource(arena, localTypes, generics, child.right))
	}
	out := "fn(" + strings.Join(paramTypes, ", ") + ")"
	if fnNode.left >= 0 {
		out += " -> " + arenaImportedTypeSource(arena, localTypes, generics, fnNode.left)
	}
	return out
}

// arenaIsPubLet reports whether the AstNLet top-level decl at `n` was
// introduced with the `pub` keyword. AstNLet does not store pub in flags
// (flags bit 0 is `mut`); the parser records it implicitly by leaving
// the `pub` token in the stream immediately before the decl's start
// token. See astLowerLetDecl in internal/selfhost/ast_lower.osty:1070.
func arenaIsPubLet(run *FrontendRun, n *AstNode) bool {
	if run == nil || run.stream == nil || n == nil {
		return false
	}
	idx := n.start - 1
	if idx < 0 || idx >= len(run.stream.tokens) {
		return false
	}
	tok := run.stream.tokens[idx]
	if tok == nil {
		return false
	}
	_, ok := tok.kind.(*FrontTokenKind_FrontPub)
	return ok
}

// arenaLetDeclName extracts the binding name from an AstNLet decl at
// top-level. Irrefutable let patterns that land in the decl stream are
// either a bare AstNIdent or an AstNPattern with ident kind; the parser
// records the name in `text` in both cases.
func arenaLetDeclName(arena *AstArena, n *AstNode) string {
	if n == nil || n.left < 0 || n.left >= len(arena.nodes) {
		return ""
	}
	pat := arena.nodes[n.left]
	if pat == nil {
		return ""
	}
	return pat.text
}
