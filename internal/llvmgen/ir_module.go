package llvmgen

import (
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/token"
)

// GenerateModule is the sole public entry point of the LLVM backend.
// It consumes the backend-neutral IR (internal/ir) and emits textual
// LLVM IR.
//
// The implementation now first projects a primitive/control-flow slice
// into the native-owned entrypoint mirrored from toolchain/llvmgen.osty.
// Remaining shapes still reify the module back into a legacy AST shape
// through legacyFileFromModule and then hand off to the long-standing
// AST-driven emitter. This is a transitional detail: external callers
// route through IR only, and the in-package test helper generateFromAST
// is unexported. Once the emitter consumes IR directly end-to-end, the
// fallback bridge and the AST helper both go away.
func GenerateModule(mod *ostyir.Module, opts Options) ([]byte, error) {
	mod = ostyir.Optimize(mod, ostyir.OptimizeOptions{})
	if out, ok, err := TryGenerateNativeOwnedModule(mod, opts); err != nil {
		return nil, err
	} else if ok {
		return out, nil
	}
	file, err := legacyFileFromModule(mod)
	if err != nil {
		// legacyFileFromModule populated the specialized-builtin
		// side channel but we're bailing early; clear it so a
		// subsequent unrelated GenerateModule call doesn't see
		// stale state.
		currentSpecializedBuiltinSurfaces = nil
		currentSpecializedBuiltinMeta = nil
		currentLiftedClosures = nil
		currentLiftedClosuresByName = nil
		currentLiftedClosuresByMaker = nil
		return nil, err
	}
	// Defer cleanup until AFTER generateASTFile consumes the side
	// channel. Nulling it at legacyFileFromModule return would clear
	// the map before signatureOf and staticExprInfo (called from
	// generateASTFile) can read it.
	defer func() {
		currentSpecializedBuiltinSurfaces = nil
		currentSpecializedBuiltinMeta = nil
		currentLiftedClosures = nil
		currentLiftedClosuresByName = nil
		currentLiftedClosuresByMaker = nil
	}()
	out, err := generateASTFile(file, opts)
	if err != nil {
		return nil, err
	}
	return finalizeLegacyFFISurface(out, mod), nil
}

func prepareModuleGeneration(mod *ostyir.Module) error {
	if mod == nil {
		return unsupported("source-layout", "nil module")
	}
	if err := validateLegacyFFISurface(mod); err != nil {
		return err
	}
	if diag, ok := moduleUnsupportedDiagnostic(mod); ok {
		return &UnsupportedError{Diagnostic: diag}
	}
	return nil
}

// validateLegacyFFISurface rejects FFI-only contracts the legacy
// AST-based emitter cannot represent faithfully. `#[intrinsic]` bodies
// must never become ordinary LLVM definitions, so we fail early with the
// same explicit message the MIR emitter uses instead of letting the
// bridge drift into a generic "has no body" source-layout error.
func validateLegacyFFISurface(mod *ostyir.Module) error {
	if mod == nil {
		return nil
	}
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case *ostyir.FnDecl:
			if d != nil && d.IsIntrinsic {
				return unsupported("mir-mvp", "intrinsic function declaration "+d.Name)
			}
		case *ostyir.StructDecl:
			for _, method := range d.Methods {
				if method != nil && method.IsIntrinsic {
					return unsupported("mir-mvp", "intrinsic function declaration "+llvmMethodIRName(d.Name, method.Name))
				}
			}
		case *ostyir.EnumDecl:
			for _, method := range d.Methods {
				if method != nil && method.IsIntrinsic {
					return unsupported("mir-mvp", "intrinsic function declaration "+llvmMethodIRName(d.Name, method.Name))
				}
			}
		}
	}
	return nil
}

// finalizeLegacyFFISurface re-applies the runtime-sublanguage contracts
// that the IR->AST bridge cannot encode directly today.
func finalizeLegacyFFISurface(out []byte, mod *ostyir.Module) []byte {
	out = applyLegacyCABICallingConvention(out, mod)
	return appendExportAliases(out, mod)
}

// applyLegacyCABICallingConvention patches legacy-emitted `define`
// lines for `#[c_abi]` functions so MIR fallback keeps the
// calling-convention marker visible in the textual LLVM IR. The legacy
// bridge emits user functions as definitions only, so constraining the
// rewrite to `define` avoids touching unrelated runtime declarations
// that might happen to share a symbol name.
func applyLegacyCABICallingConvention(out []byte, mod *ostyir.Module) []byte {
	if mod == nil || len(out) == 0 {
		return out
	}
	cabiSymbols := legacyCABISymbols(mod)
	if len(cabiSymbols) == 0 {
		return out
	}
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "define ") {
			continue
		}
		for symbol := range cabiSymbols {
			if !strings.Contains(line, "@"+symbol+"(") {
				continue
			}
			if !strings.HasPrefix(line, "define ccc ") {
				lines[i] = strings.Replace(line, "define ", "define ccc ", 1)
			}
			break
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func legacyCABISymbols(mod *ostyir.Module) map[string]struct{} {
	if mod == nil {
		return nil
	}
	out := map[string]struct{}{}
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case *ostyir.FnDecl:
			if d != nil && d.CABI {
				out[d.Name] = struct{}{}
			}
		case *ostyir.StructDecl:
			for _, method := range d.Methods {
				if method != nil && method.CABI {
					out[llvmMethodIRName(d.Name, method.Name)] = struct{}{}
				}
			}
		case *ostyir.EnumDecl:
			for _, method := range d.Methods {
				if method != nil && method.CABI {
					out[llvmMethodIRName(d.Name, method.Name)] = struct{}{}
				}
			}
		}
	}
	return out
}

// appendExportAliases scans the IR module for `*ostyir.FnDecl` with
// non-empty `ExportSymbol` (set from `#[export("name")]` per
// LANG_SPEC §19.6) and appends an LLVM IR `alias` line per fn:
//
//	@<symbol> = dso_local alias ptr, ptr @<fn.Name>
//
// This makes the export symbol resolvable at link time without
// renaming the function (which would break in-module call sites).
// The MIR pipeline (`GenerateFromMIR`, opt-in via Options.UseMIR)
// uses a different mechanism — it overrides the emitted name
// directly because MIR has no internal callers in the v0.4
// surface today. Both paths converge at link-time on the same
// exported symbol name.
//
// For C ABI purposes the LLVM-IR-side alias type need only be
// `ptr` — the linker resolves the symbol by name and the C
// consumer's header carries the actual function signature.
func appendExportAliases(out []byte, mod *ostyir.Module) []byte {
	if mod == nil {
		return out
	}
	var aliases []byte
	for _, decl := range mod.Decls {
		fn, ok := decl.(*ostyir.FnDecl)
		if !ok || fn == nil {
			continue
		}
		if fn.ExportSymbol == "" || fn.ExportSymbol == fn.Name {
			continue
		}
		aliases = append(aliases, "@"...)
		aliases = append(aliases, fn.ExportSymbol...)
		aliases = append(aliases, " = dso_local alias ptr, ptr @"...)
		aliases = append(aliases, fn.Name...)
		aliases = append(aliases, '\n')
	}
	if len(aliases) == 0 {
		return out
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, aliases...)
	return out
}

func moduleUnsupportedDiagnostic(mod *ostyir.Module) (UnsupportedDiagnostic, bool) {
	if mod == nil {
		return UnsupportedDiagnostic{}, false
	}
	for _, decl := range mod.Decls {
		use, ok := decl.(*ostyir.UseDecl)
		if !ok || use == nil {
			continue
		}
		if use.IsGoFFI {
			return UnsupportedDiagnosticFor("go-ffi", use.GoPath), true
		}
		if use.IsRuntimeFFI && !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
			return UnsupportedDiagnosticFor("runtime-ffi", use.RuntimePath), true
		}
	}
	return UnsupportedDiagnostic{}, false
}

// specializedBuiltinSurface holds the {source name, concrete args}
// recovered from a monomorphized built-in specialization's
// BuiltinSource / BuiltinSourceArgs fields. The AST bridge uses this
// per-module index to re-synthesize a `Map<String, Int>` surface
// NamedType whenever it encounters a reference to a `_ZTS…` mangled
// built-in struct/enum, so the legacy LLVM emitter's intrinsic
// dispatch (keyed on Path[0] == "Map" / "Option" / …) keeps working.
type specializedBuiltinSurface struct {
	Source string
	Args   []ostyir.Type
}

// specializedBuiltinMeta is the llvmgen-side shape of a specialized
// built-in's surface-level identity. Used by signatureOf's `self`
// binding and by staticExprInfo to tag receiver values with the map /
// list / set metadata the intrinsic dispatch reads.
type specializedBuiltinMeta struct {
	sourceType     ast.Type
	listElemTyp    string
	listElemString bool
	mapKeyTyp      string
	mapValueTyp    string
	mapKeyString   bool
	setElemTyp     string
	setElemString  bool
}

// currentSpecializedBuiltinMeta maps mangled owner names (the
// `ownerName` passed into signatureOf) to their surface-level map /
// list / set metadata. Populated alongside
// currentSpecializedBuiltinSurfaces at the start of each bridge
// conversion; read by llvmgen's signatureOf to populate `self`
// paramInfo.
var currentSpecializedBuiltinMeta map[string]specializedBuiltinMeta

// specializedBuiltinMetaFor returns the metadata for a specialized
// built-in owner name, or (zero, false) if the name is unknown or
// unregistered.
func specializedBuiltinMetaFor(ownerName string) (specializedBuiltinMeta, bool) {
	if currentSpecializedBuiltinMeta == nil {
		return specializedBuiltinMeta{}, false
	}
	info, ok := currentSpecializedBuiltinMeta[ownerName]
	return info, ok
}

// specializedBuiltinMangledForSurface returns the mangled struct/enum
// name (the monomorphizer's `_ZTS…` key) matching a surface AST
// NamedType like `Map<String, Int>`. Used by userCallTarget to
// dispatch user-level method calls (`m.forEach(f)`, `m.getOr(...)`)
// to the specialized method set — Phase 2c's surface re-association
// leaves baseInfo.typ = "ptr" for runtime containers, so the usual
// `methodsByType[baseInfo.typ]` lookup misses. Returns (ownerLLVMType,
// true) so callers can index directly into g.methods.
func specializedBuiltinMangledForSurface(surface ast.Type) (string, bool) {
	if currentSpecializedBuiltinSurfaces == nil || surface == nil {
		return "", false
	}
	surfaceNamed, ok := surface.(*ast.NamedType)
	if !ok || len(surfaceNamed.Path) != 1 {
		return "", false
	}
	sourceName := surfaceNamed.Path[0]
	for mangled, surf := range currentSpecializedBuiltinSurfaces {
		if surf.Source != sourceName {
			continue
		}
		if len(surf.Args) != len(surfaceNamed.Args) {
			continue
		}
		if !surfaceBuiltinArgsMatch(surf.Args, surfaceNamed.Args) {
			continue
		}
		return "%" + mangled, true
	}
	return "", false
}

// surfaceBuiltinArgsMatch compares IR-level type args (carried on the
// specialized struct) against an AST-level args list (pulled from the
// user-side surface reference). A name-level match is sufficient for
// the built-in shapes we care about (primitive idents like `Int` /
// `String` / `Bool`, plus nested NamedType references). Nested
// generic args recurse so Map<String, List<Int>> round-trips cleanly.
//
// When an IR arg is itself a mangled `_ZTSN…` specialization — the
// inner `List<String>` of `List<List<String>>`, for example — look
// its surface entry back up in `currentSpecializedBuiltinSurfaces`
// and compare at that level. The AST side only ever carries the
// surface form (`List<String>`), so a literal string compare would
// miss every nested stdlib specialization.
func surfaceBuiltinArgsMatch(irArgs []ostyir.Type, astArgs []ast.Type) bool {
	if len(irArgs) != len(astArgs) {
		return false
	}
	for i := range irArgs {
		irName := ostyirTypeName(irArgs[i])
		astNamed, ok := astArgs[i].(*ast.NamedType)
		if !ok || len(astNamed.Path) != 1 {
			return false
		}
		if strings.HasPrefix(irName, "_ZTS") {
			surf, ok := currentSpecializedBuiltinSurfaces[irName]
			if !ok {
				return false
			}
			if surf.Source != astNamed.Path[0] {
				return false
			}
			if !surfaceBuiltinArgsMatch(surf.Args, astNamed.Args) {
				return false
			}
			continue
		}
		if irName != astNamed.Path[0] {
			return false
		}
		// Recurse into nested generics (e.g. List<Int> in Map<String, List<Int>>).
		irNested := ostyirTypeArgs(irArgs[i])
		if !surfaceBuiltinArgsMatch(irNested, astNamed.Args) {
			return false
		}
	}
	return true
}

// ostyirTypeName extracts the source-level name from an IR type for
// surface comparison. Primitives come through as *ostyir.PrimType
// (mapped via its String()), named refs via *ostyir.NamedType.
func ostyirTypeName(t ostyir.Type) string {
	switch x := t.(type) {
	case *ostyir.PrimType:
		return x.String()
	case *ostyir.NamedType:
		return x.Name
	}
	return ""
}

// ostyirTypeArgs returns the nested type-args of an IR type, or nil
// for types without args (primitives, optional wrappers, etc.).
func ostyirTypeArgs(t ostyir.Type) []ostyir.Type {
	if named, ok := t.(*ostyir.NamedType); ok {
		return named.Args
	}
	return nil
}

// buildSpecializedBuiltinMeta projects each mangled surface entry
// into an llvmgen-side metadata shape. Uses the legacy type bridge to
// turn IR type args into AST types, then classifies per source name:
// Map<K, V> populates mapKeyTyp / mapValueTyp, List<T> populates
// listElemTyp, etc. Callers pass the surface map produced by
// collectSpecializedBuiltinSurfaces.
func buildSpecializedBuiltinMeta(surfaces map[string]specializedBuiltinSurface) map[string]specializedBuiltinMeta {
	out := map[string]specializedBuiltinMeta{}
	for mangled, surf := range surfaces {
		if surf.Source == "" {
			continue
		}
		astArgs := make([]ast.Type, 0, len(surf.Args))
		for _, a := range surf.Args {
			astArgs = append(astArgs, legacyTypeFromIR(a))
		}
		meta := specializedBuiltinMeta{
			sourceType: &ast.NamedType{Path: []string{surf.Source}, Args: astArgs},
		}
		switch surf.Source {
		case "Map":
			if len(astArgs) == 2 {
				if k, err := llvmRuntimeABIType(astArgs[0], typeEnv{}); err == nil {
					meta.mapKeyTyp = k
				}
				if v, err := llvmRuntimeABIType(astArgs[1], typeEnv{}); err == nil {
					meta.mapValueTyp = v
				}
				meta.mapKeyString = llvmNamedTypeIsString(astArgs[0])
			}
		case "List":
			if len(astArgs) == 1 {
				if e, err := llvmRuntimeABIType(astArgs[0], typeEnv{}); err == nil {
					meta.listElemTyp = e
				}
				meta.listElemString = llvmNamedTypeIsString(astArgs[0])
			}
		case "Set":
			if len(astArgs) == 1 {
				if e, err := llvmRuntimeABIType(astArgs[0], typeEnv{}); err == nil {
					meta.setElemTyp = e
				}
				meta.setElemString = llvmNamedTypeIsString(astArgs[0])
			}
		}
		out[mangled] = meta
	}
	return out
}

func collectSpecializedBuiltinSurfaces(mod *ostyir.Module) map[string]specializedBuiltinSurface {
	out := map[string]specializedBuiltinSurface{}
	if mod == nil {
		return out
	}
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case *ostyir.StructDecl:
			if d == nil || d.BuiltinSource == "" {
				continue
			}
			out[d.Name] = specializedBuiltinSurface{Source: d.BuiltinSource, Args: d.BuiltinSourceArgs}
		case *ostyir.EnumDecl:
			if d == nil || d.BuiltinSource == "" {
				continue
			}
			out[d.Name] = specializedBuiltinSurface{Source: d.BuiltinSource, Args: d.BuiltinSourceArgs}
		}
	}
	return out
}

// currentSpecializedBuiltinSurfaces is set by legacyFileFromModule at
// the start of each conversion and consulted by legacyTypeFromIR when
// a NamedType's name matches a specialized built-in. Keeping it as a
// local (reset on each call) avoids threading the map through every
// IR-to-AST visitor helper, while still giving the bridge the lookup
// context it needs.
var currentSpecializedBuiltinSurfaces map[string]specializedBuiltinSurface

func legacyFileFromModule(mod *ostyir.Module) (*ast.File, error) {
	currentSpecializedBuiltinSurfaces = collectSpecializedBuiltinSurfaces(mod)
	currentSpecializedBuiltinMeta = buildSpecializedBuiltinMeta(currentSpecializedBuiltinSurfaces)
	// NOTE: callers (GenerateModule) are responsible for clearing
	// currentSpecializedBuiltinSurfaces / currentSpecializedBuiltinMeta
	// after `generateASTFile` consumes them. A deferred clear here
	// would nil them before the downstream signatureOf /
	// staticExprInfo paths can read the metadata off the built AST.
	//
	// Closure-literal hoisting runs alongside: every no-capture IR
	// Closure gets a synthesized top-level fn so the legacy emitter
	// sees a bare Ident (handled by the existing fn-value Env path
	// in fn_value.go) instead of a `*ast.ClosureExpr` it can't
	// lower. See closure_lift.go for the lift table contract.
	liftedDecls := liftClosuresFromModule(mod)
	start, end := legacySpan(mod.At())
	file := &ast.File{PosV: start, EndV: end}
	// Lifted closure fns go in first so collectDeclarations sees them
	// before any other decl that might call them — matters when a
	// lifted closure's name appears in the user's main fn signature
	// hash via auto-rename collisions (the monotonic counter makes
	// real collisions impossible, but order keeps the test snapshots
	// deterministic).
	for _, d := range liftedDecls {
		file.Decls = append(file.Decls, d)
	}
	for _, decl := range mod.Decls {
		legacyDecl, err := legacyDeclFromIR(decl)
		if err != nil {
			return nil, err
		}
		if legacyDecl == nil {
			continue
		}
		if use, ok := legacyDecl.(*ast.UseDecl); ok {
			file.Uses = append(file.Uses, use)
			continue
		}
		file.Decls = append(file.Decls, legacyDecl)
	}
	for _, stmt := range mod.Script {
		legacyStmt, err := legacyStmtFromIR(stmt)
		if err != nil {
			return nil, err
		}
		if legacyStmt != nil {
			file.Stmts = append(file.Stmts, legacyStmt)
		}
	}
	return file, nil
}

func legacyDeclFromIR(decl ostyir.Decl) (ast.Decl, error) {
	switch d := decl.(type) {
	case nil:
		return nil, nil
	case *ostyir.UseDecl:
		return legacyUseDeclFromIR(d)
	case *ostyir.FnDecl:
		return legacyFnDeclFromIR(d, false)
	case *ostyir.StructDecl:
		return legacyStructDeclFromIR(d)
	case *ostyir.EnumDecl:
		return legacyEnumDeclFromIR(d)
	case *ostyir.InterfaceDecl:
		return legacyInterfaceDeclFromIR(d)
	case *ostyir.TypeAliasDecl:
		return legacyTypeAliasDeclFromIR(d)
	case *ostyir.LetDecl:
		return legacyLetDeclFromIR(d)
	default:
		return nil, unsupportedf("source-layout", "IR declaration %T", decl)
	}
}

func legacyUseDeclFromIR(d *ostyir.UseDecl) (ast.Decl, error) {
	if d == nil {
		return nil, nil
	}
	start, end := legacySpan(d.At())
	out := &ast.UseDecl{
		PosV:         start,
		EndV:         end,
		Path:         append([]string(nil), d.Path...),
		RawPath:      d.RawPath,
		Alias:        d.Alias,
		IsGoFFI:      d.IsGoFFI,
		IsRuntimeFFI: d.IsRuntimeFFI,
		GoPath:       d.GoPath,
		RuntimePath:  d.RuntimePath,
	}
	for _, inner := range d.GoBody {
		legacyInner, err := legacyDeclFromIR(inner)
		if err != nil {
			return nil, err
		}
		if legacyInner != nil {
			out.GoBody = append(out.GoBody, legacyInner)
		}
	}
	return out, nil
}

// legacyVectorizeArgs reconstructs the `#[vectorize(...)]` argument
// list from the reified IR flags so the HIR emitter's annotation
// reader sees the same shape the source carried. Keeps parity with
// the MIR emitter, which reads the flags directly off `mir.Function`.
func legacyVectorizeArgs(pos token.Pos, fn *ostyir.FnDecl) []*ast.AnnotationArg {
	var args []*ast.AnnotationArg
	if fn.VectorizeScalable {
		args = append(args, &ast.AnnotationArg{PosV: pos, Key: "scalable"})
	}
	if fn.VectorizePredicate {
		args = append(args, &ast.AnnotationArg{PosV: pos, Key: "predicate"})
	}
	if fn.VectorizeWidth > 0 {
		args = append(args, &ast.AnnotationArg{
			PosV: pos,
			Key:  "width",
			Value: &ast.IntLit{
				PosV: pos,
				EndV: pos,
				Text: strconv.Itoa(fn.VectorizeWidth),
			},
		})
	}
	return args
}

func legacyFnDeclFromIR(fn *ostyir.FnDecl, asMethod bool) (*ast.FnDecl, error) {
	if fn == nil {
		return nil, nil
	}
	start, end := legacySpan(fn.At())
	out := &ast.FnDecl{
		PosV:       start,
		EndV:       end,
		Pub:        fn.Exported,
		Name:       fn.Name,
		ReturnType: legacyTypeFromIR(fn.Return),
	}
	// Surface the narrow set of backend-relevant annotations that the
	// legacy AST emitter re-reads from the reified FnDecl. The bridge
	// intentionally does not rematerialise user-facing annotations like
	// `#[json]` or `#[deprecated]` — those are consumed upstream (by the
	// resolver / lint) before the IR is handed to the backend. Only
	// codegen-behavior flags get reconstituted, and only as bare-flag
	// placeholders; their semantics are carried by the IR fields, the
	// annotation names here are just the signal the emitter checks for.
	// v0.6 A5.2: vectorize is default-on. The legacy bridge needs to
	// surface only the explicit opt-out + any tuning args the user
	// actually typed, not the default state itself (otherwise every
	// reified fn would carry a redundant synthetic `#[vectorize]`
	// annotation that the HIR emitter then has to undo).
	if fn.NoVectorize {
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start,
			EndV: start,
			Name: "no_vectorize",
		})
	} else if args := legacyVectorizeArgs(start, fn); len(args) > 0 {
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start,
			EndV: start,
			Name: "vectorize",
			Args: args,
		})
	}
	if fn.Parallel {
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start,
			EndV: start,
			Name: "parallel",
		})
	}
	if fn.Unroll {
		var args []*ast.AnnotationArg
		if fn.UnrollCount > 0 {
			args = []*ast.AnnotationArg{{
				PosV: start,
				Key:  "count",
				Value: &ast.IntLit{
					PosV: start,
					EndV: start,
					Text: strconv.Itoa(fn.UnrollCount),
				},
			}}
		}
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start,
			EndV: start,
			Name: "unroll",
			Args: args,
		})
	}
	// v0.6 A8/A9/A10: reinstate function-attribute annotations so the
	// HIR emitter's reified-AST path surfaces them for formatFnAttrs.
	switch fn.InlineMode {
	case ostyir.InlineSoft:
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "inline",
		})
	case ostyir.InlineAlways:
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "inline",
			Args: []*ast.AnnotationArg{{PosV: start, Key: "always"}},
		})
	case ostyir.InlineNever:
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "inline",
			Args: []*ast.AnnotationArg{{PosV: start, Key: "never"}},
		})
	}
	if fn.Hot {
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "hot",
		})
	}
	if fn.Cold {
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "cold",
		})
	}
	if len(fn.TargetFeatures) > 0 {
		args := make([]*ast.AnnotationArg, len(fn.TargetFeatures))
		for i, feat := range fn.TargetFeatures {
			args[i] = &ast.AnnotationArg{PosV: start, Key: feat}
		}
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "target_feature", Args: args,
		})
	}
	if fn.Pure {
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "pure",
		})
	}
	if fn.NoaliasAll {
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "noalias",
		})
	} else if len(fn.NoaliasParams) > 0 {
		args := make([]*ast.AnnotationArg, len(fn.NoaliasParams))
		for i, name := range fn.NoaliasParams {
			args[i] = &ast.AnnotationArg{PosV: start, Key: name}
		}
		out.Annotations = append(out.Annotations, &ast.Annotation{
			PosV: start, EndV: start, Name: "noalias", Args: args,
		})
	}
	if asMethod {
		out.Recv = &ast.Receiver{PosV: start, EndV: start, Mut: fn.ReceiverMut}
	}
	for _, gp := range fn.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, param := range fn.Params {
		legacyParam, err := legacyParamFromIR(param)
		if err != nil {
			return nil, err
		}
		out.Params = append(out.Params, legacyParam)
	}
	if fn.Body != nil {
		body, err := legacyBlockFromIR(fn.Body)
		if err != nil {
			return nil, err
		}
		out.Body = body
	}
	return out, nil
}

func legacyTypeParamFromIR(tp *ostyir.TypeParam) *ast.GenericParam {
	if tp == nil {
		return nil
	}
	start, end := legacySpan(tp.At())
	out := &ast.GenericParam{PosV: start, EndV: end, Name: tp.Name}
	for _, bound := range tp.Bounds {
		out.Constraints = append(out.Constraints, legacyTypeFromIR(bound))
	}
	return out
}

func legacyParamFromIR(param *ostyir.Param) (*ast.Param, error) {
	if param == nil {
		return nil, nil
	}
	start, end := legacySpan(param.At())
	out := &ast.Param{
		PosV: start,
		EndV: end,
		Name: param.Name,
		Type: legacyTypeFromIR(param.Type),
	}
	if param.Pattern != nil {
		pat, err := legacyPatternFromIR(param.Pattern)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
	}
	if param.Default != nil {
		value, err := legacyExprFromIR(param.Default)
		if err != nil {
			return nil, err
		}
		out.Default = value
	}
	return out, nil
}

func legacyStructDeclFromIR(sd *ostyir.StructDecl) (*ast.StructDecl, error) {
	if sd == nil {
		return nil, nil
	}
	start, end := legacySpan(sd.At())
	out := &ast.StructDecl{
		PosV: start,
		EndV: end,
		Pub:  sd.Exported,
		Name: sd.Name,
	}
	for _, gp := range sd.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, field := range sd.Fields {
		legacyField, err := legacyFieldFromIR(field)
		if err != nil {
			return nil, err
		}
		out.Fields = append(out.Fields, legacyField)
	}
	specializedBuiltin := isSpecializedBuiltinStructName(sd.Name)
	for _, method := range sd.Methods {
		// Option B Phase 2: specialized built-in structs (Map$String$Int,
		// Option$Int, …) inherit bodyless intrinsic methods from the
		// stdlib template (Map.len / Map.get / Map.insert / …). These
		// are served by runtime helpers (osty_rt_map_len etc.), not by
		// user-level LLVM functions. Dropping them here avoids LLVM010
		// "function has no body" at emit time; the method-call
		// dispatch in emitMapMethodCall still routes to the runtime
		// via the original source-type intrinsic recognition.
		if specializedBuiltin && method != nil && method.Body == nil {
			continue
		}
		legacyMethod, err := legacyFnDeclFromIR(method, true)
		if err != nil {
			return nil, err
		}
		out.Methods = append(out.Methods, legacyMethod)
	}
	return out, nil
}

// isSpecializedBuiltinStructName reports whether name is an
// Itanium-mangled specialization produced by the monomorphizer for a
// stdlib built-in template (Map, List, Set, Option, Result). All such
// names start with the `_ZTS` Itanium type-info prefix; user structs
// keep their source name unless explicitly specialized via the same
// mangler, and in that case they are equally free of bodyless
// methods (user structs don't carry intrinsic placeholders).
// isSpecializedBuiltinStructName delegates to the Osty-sourced
// `mirIsSpecializedBuiltinStructName` (`toolchain/mir_generator.osty`).
func isSpecializedBuiltinStructName(name string) bool {
	return mirIsSpecializedBuiltinStructName(name)
}

func legacyFieldFromIR(field *ostyir.Field) (*ast.Field, error) {
	if field == nil {
		return nil, nil
	}
	start, end := legacySpan(field.At())
	out := &ast.Field{
		PosV: start,
		EndV: end,
		Pub:  field.Exported,
		Name: field.Name,
		Type: legacyTypeFromIR(field.Type),
	}
	if field.Default != nil {
		value, err := legacyExprFromIR(field.Default)
		if err != nil {
			return nil, err
		}
		out.Default = value
	}
	return out, nil
}

func legacyEnumDeclFromIR(ed *ostyir.EnumDecl) (*ast.EnumDecl, error) {
	if ed == nil {
		return nil, nil
	}
	start, end := legacySpan(ed.At())
	out := &ast.EnumDecl{
		PosV: start,
		EndV: end,
		Pub:  ed.Exported,
		Name: ed.Name,
	}
	for _, gp := range ed.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, variant := range ed.Variants {
		legacyVariant := legacyVariantFromIR(variant)
		out.Variants = append(out.Variants, legacyVariant)
	}
	for _, method := range ed.Methods {
		legacyMethod, err := legacyFnDeclFromIR(method, true)
		if err != nil {
			return nil, err
		}
		out.Methods = append(out.Methods, legacyMethod)
	}
	return out, nil
}

func legacyVariantFromIR(variant *ostyir.Variant) *ast.Variant {
	if variant == nil {
		return nil
	}
	start, end := legacySpan(variant.At())
	out := &ast.Variant{
		PosV: start,
		EndV: end,
		Name: variant.Name,
	}
	for _, payload := range variant.Payload {
		out.Fields = append(out.Fields, legacyTypeFromIR(payload))
	}
	return out
}

func legacyInterfaceDeclFromIR(id *ostyir.InterfaceDecl) (*ast.InterfaceDecl, error) {
	if id == nil {
		return nil, nil
	}
	start, end := legacySpan(id.At())
	out := &ast.InterfaceDecl{
		PosV: start,
		EndV: end,
		Pub:  id.Exported,
		Name: id.Name,
	}
	for _, gp := range id.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	for _, ext := range id.Extends {
		out.Extends = append(out.Extends, legacyTypeFromIR(ext))
	}
	for _, method := range id.Methods {
		legacyMethod, err := legacyFnDeclFromIR(method, false)
		if err != nil {
			return nil, err
		}
		out.Methods = append(out.Methods, legacyMethod)
	}
	return out, nil
}

func legacyTypeAliasDeclFromIR(td *ostyir.TypeAliasDecl) (*ast.TypeAliasDecl, error) {
	if td == nil {
		return nil, nil
	}
	start, end := legacySpan(td.At())
	out := &ast.TypeAliasDecl{
		PosV:   start,
		EndV:   end,
		Pub:    td.Exported,
		Name:   td.Name,
		Target: legacyTypeFromIR(td.Target),
	}
	for _, gp := range td.Generics {
		out.Generics = append(out.Generics, legacyTypeParamFromIR(gp))
	}
	return out, nil
}

func legacyLetDeclFromIR(ld *ostyir.LetDecl) (*ast.LetDecl, error) {
	if ld == nil {
		return nil, nil
	}
	start, end := legacySpan(ld.At())
	out := &ast.LetDecl{
		PosV: start,
		EndV: end,
		Pub:  ld.Exported,
		Mut:  ld.Mut,
		Name: ld.Name,
		Type: legacyBindingTypeFromIR(ld.Type, ld.Value != nil),
	}
	if ld.Value != nil {
		value, err := legacyExprFromIR(ld.Value)
		if err != nil {
			return nil, err
		}
		out.Value = value
	}
	return out, nil
}

func legacyTypeFromIR(typ ostyir.Type) ast.Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *ostyir.PrimType:
		name := legacyPrimTypeName(t.Kind)
		if name == "" {
			return nil
		}
		return &ast.NamedType{Path: []string{name}}
	case *ostyir.NamedType:
		// Phase 2c: if the name is a `_ZTS…` mangled built-in
		// specialization, rebuild the surface `Map<String, Int>` /
		// `Option<T>` form so the legacy emitter's intrinsic
		// dispatch (keyed on Path[0] == "Map" / …) still fires.
		if surf, ok := currentSpecializedBuiltinSurfaces[t.Name]; ok && surf.Source != "" {
			out := &ast.NamedType{Path: []string{surf.Source}}
			for _, arg := range surf.Args {
				out.Args = append(out.Args, legacyTypeFromIR(arg))
			}
			return out
		}
		path := splitQualifiedName(t.Package)
		path = append(path, t.Name)
		out := &ast.NamedType{Path: path}
		for _, arg := range t.Args {
			out.Args = append(out.Args, legacyTypeFromIR(arg))
		}
		return out
	case *ostyir.OptionalType:
		return &ast.OptionalType{Inner: legacyTypeFromIR(t.Inner)}
	case *ostyir.TupleType:
		out := &ast.TupleType{}
		for _, elem := range t.Elems {
			out.Elems = append(out.Elems, legacyTypeFromIR(elem))
		}
		return out
	case *ostyir.FnType:
		out := &ast.FnType{ReturnType: legacyTypeFromIR(t.Return)}
		for _, param := range t.Params {
			out.Params = append(out.Params, legacyTypeFromIR(param))
		}
		return out
	case *ostyir.TypeVar:
		return &ast.NamedType{Path: []string{t.Name}}
	case *ostyir.ErrType:
		return &ast.NamedType{Path: []string{"<error>"}}
	default:
		return nil
	}
}

func legacyBindingTypeFromIR(typ ostyir.Type, hasValue bool) ast.Type {
	// Lowering currently bakes inferred let types into IR. If the checker leaves
	// an inferred value at ErrType, replaying that as an explicit `<error>` type
	// in the bridge makes the legacy emitter fail even when the value path is
	// otherwise compilable. Keep the annotation only when it carries useful type
	// information.
	if hasValue {
		if _, ok := typ.(*ostyir.ErrType); ok {
			return nil
		}
	}
	return legacyTypeFromIR(typ)
}

// legacyPrimTypeName returns the user-visible Osty type name for an
// `ir.PrimKind`. Delegates to the Osty-sourced `mirLegacyPrimTypeName`.
func legacyPrimTypeName(kind ostyir.PrimKind) string {
	return mirLegacyPrimTypeName(int(kind),
		int(ostyir.PrimInt), int(ostyir.PrimInt8), int(ostyir.PrimInt16),
		int(ostyir.PrimInt32), int(ostyir.PrimInt64),
		int(ostyir.PrimUInt8), int(ostyir.PrimUInt16),
		int(ostyir.PrimUInt32), int(ostyir.PrimUInt64),
		int(ostyir.PrimByte),
		int(ostyir.PrimFloat), int(ostyir.PrimFloat32), int(ostyir.PrimFloat64),
		int(ostyir.PrimBool), int(ostyir.PrimChar),
		int(ostyir.PrimString), int(ostyir.PrimBytes),
	)
}

func legacyBlockFromIR(block *ostyir.Block) (*ast.Block, error) {
	if block == nil {
		return nil, nil
	}
	start, end := legacySpan(block.At())
	out := &ast.Block{PosV: start, EndV: end}
	for _, stmt := range block.Stmts {
		legacyStmt, err := legacyStmtFromIR(stmt)
		if err != nil {
			return nil, err
		}
		if legacyStmt != nil {
			out.Stmts = append(out.Stmts, legacyStmt)
		}
	}
	if block.Result != nil {
		resultExpr, err := legacyExprFromIR(block.Result)
		if err != nil {
			return nil, err
		}
		out.Stmts = append(out.Stmts, &ast.ExprStmt{X: resultExpr})
	}
	return out, nil
}

func legacyStmtFromIR(stmt ostyir.Stmt) (ast.Stmt, error) {
	switch s := stmt.(type) {
	case nil:
		return nil, nil
	case *ostyir.Block:
		return legacyBlockFromIR(s)
	case *ostyir.LetStmt:
		return legacyLetStmtFromIR(s)
	case *ostyir.ExprStmt:
		expr, err := legacyExprFromIR(s.X)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	case *ostyir.AssignStmt:
		return legacyAssignStmtFromIR(s)
	case *ostyir.ReturnStmt:
		start, end := legacySpan(s.At())
		out := &ast.ReturnStmt{PosV: start, EndV: end}
		if s.Value != nil {
			value, err := legacyExprFromIR(s.Value)
			if err != nil {
				return nil, err
			}
			out.Value = value
		}
		return out, nil
	case *ostyir.BreakStmt:
		start, end := legacySpan(s.At())
		return &ast.BreakStmt{PosV: start, EndV: end}, nil
	case *ostyir.ContinueStmt:
		start, end := legacySpan(s.At())
		return &ast.ContinueStmt{PosV: start, EndV: end}, nil
	case *ostyir.IfStmt:
		return legacyIfStmtFromIR(s)
	case *ostyir.ForStmt:
		return legacyForStmtFromIR(s)
	case *ostyir.DeferStmt:
		return legacyDeferStmtFromIR(s)
	case *ostyir.MatchStmt:
		expr, err := legacyMatchExprFromIR(&ostyir.MatchExpr{
			Scrutinee: s.Scrutinee,
			Arms:      s.Arms,
			SpanV:     s.At(),
		})
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	default:
		return nil, unsupportedf("statement", "IR statement %T", stmt)
	}
}

func legacyLetStmtFromIR(stmt *ostyir.LetStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	out := &ast.LetStmt{
		PosV: start,
		EndV: end,
		Mut:  stmt.Mut,
		Type: legacyBindingTypeFromIR(stmt.Type, stmt.Value != nil),
	}
	if stmt.Pattern != nil {
		pat, err := legacyPatternFromIR(stmt.Pattern)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
	} else {
		out.Pattern = &ast.IdentPat{PosV: start, EndV: end, Name: stmt.Name}
	}
	if stmt.Value != nil {
		value, err := legacyExprFromIR(stmt.Value)
		if err != nil {
			return nil, err
		}
		out.Value = value
	}
	return out, nil
}

func legacyAssignStmtFromIR(stmt *ostyir.AssignStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	out := &ast.AssignStmt{
		PosV: start,
		EndV: end,
		Op:   legacyAssignOp(stmt.Op),
	}
	for _, target := range stmt.Targets {
		legacyTarget, err := legacyExprFromIR(target)
		if err != nil {
			return nil, err
		}
		out.Targets = append(out.Targets, legacyTarget)
	}
	value, err := legacyExprFromIR(stmt.Value)
	if err != nil {
		return nil, err
	}
	out.Value = value
	return out, nil
}

// legacyAssignOp maps the IR-level `AssignOp` to the `token.Kind` used
// by AST AssignStmt nodes. Delegates to the Osty-sourced
// `mirLegacyAssignOpCode`; the wrapper plumbs every relevant enum int
// so the mapping stays in lockstep with both `internal/ir.AssignOp` and
// `internal/token`.
func legacyAssignOp(op ostyir.AssignOp) token.Kind {
	return token.Kind(mirLegacyAssignOpCode(int(op),
		int(ostyir.AssignEq), int(ostyir.AssignAdd), int(ostyir.AssignSub),
		int(ostyir.AssignMul), int(ostyir.AssignDiv), int(ostyir.AssignMod),
		int(ostyir.AssignAnd), int(ostyir.AssignOr), int(ostyir.AssignXor),
		int(ostyir.AssignShl), int(ostyir.AssignShr),
		int(token.ASSIGN), int(token.PLUSEQ), int(token.MINUSEQ),
		int(token.STAREQ), int(token.SLASHEQ), int(token.PERCENTEQ),
		int(token.BITANDEQ), int(token.BITOREQ), int(token.BITXOREQ),
		int(token.SHLEQ), int(token.SHREQ),
	))
}

func legacyIfStmtFromIR(stmt *ostyir.IfStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	cond, err := legacyExprFromIR(stmt.Cond)
	if err != nil {
		return nil, err
	}
	thenBlock, err := legacyBlockFromIR(stmt.Then)
	if err != nil {
		return nil, err
	}
	var elseExpr ast.Expr
	if stmt.Else != nil {
		elseBlock, err := legacyBlockFromIR(stmt.Else)
		if err != nil {
			return nil, err
		}
		if elseBlock != nil {
			elseExpr = elseBlock
		}
	}
	return &ast.ExprStmt{
		X: &ast.IfExpr{
			PosV: start,
			EndV: end,
			Cond: cond,
			Then: thenBlock,
			Else: elseExpr,
		},
	}, nil
}

func legacyForStmtFromIR(stmt *ostyir.ForStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	out := &ast.ForStmt{PosV: start, EndV: end}
	body, err := legacyBlockFromIR(stmt.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	switch stmt.Kind {
	case ostyir.ForInfinite:
		return out, nil
	case ostyir.ForWhile:
		iter, err := legacyExprFromIR(stmt.Cond)
		if err != nil {
			return nil, err
		}
		out.Iter = iter
		return out, nil
	case ostyir.ForRange:
		pat, err := legacyLoopPattern(stmt, start)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
		startExpr, err := legacyExprFromIR(stmt.Start)
		if err != nil {
			return nil, err
		}
		endExpr, err := legacyExprFromIR(stmt.End)
		if err != nil {
			return nil, err
		}
		out.Iter = &ast.RangeExpr{
			PosV:      start,
			EndV:      end,
			Start:     startExpr,
			Stop:      endExpr,
			Inclusive: stmt.Inclusive,
		}
		return out, nil
	case ostyir.ForIn:
		pat, err := legacyLoopPattern(stmt, start)
		if err != nil {
			return nil, err
		}
		out.Pattern = pat
		iter, err := legacyExprFromIR(stmt.Iter)
		if err != nil {
			return nil, err
		}
		out.Iter = iter
		return out, nil
	default:
		return nil, unsupportedf("control-flow", "IR for-kind %d", stmt.Kind)
	}
}

// legacyLoopPattern bridges a for-loop binding back to ast.Pattern.
func legacyLoopPattern(stmt *ostyir.ForStmt, pos token.Pos) (ast.Pattern, error) {
	if stmt.Pattern != nil {
		pat, err := legacyPatternFromIR(stmt.Pattern)
		if err != nil {
			return nil, err
		}
		return pat, nil
	}
	return &ast.IdentPat{PosV: pos, EndV: pos, Name: stmt.Var}, nil
}

func legacyDeferStmtFromIR(stmt *ostyir.DeferStmt) (ast.Stmt, error) {
	if stmt == nil {
		return nil, nil
	}
	start, end := legacySpan(stmt.At())
	body, err := legacyBlockFromIR(stmt.Body)
	if err != nil {
		return nil, err
	}
	return &ast.DeferStmt{PosV: start, EndV: end, X: body}, nil
}

func legacyExprFromIR(expr ostyir.Expr) (ast.Expr, error) {
	switch e := expr.(type) {
	case nil:
		return nil, nil
	case *ostyir.IntLit:
		start, end := legacySpan(e.At())
		return &ast.IntLit{PosV: start, EndV: end, Text: e.Text}, nil
	case *ostyir.FloatLit:
		start, end := legacySpan(e.At())
		return &ast.FloatLit{PosV: start, EndV: end, Text: e.Text}, nil
	case *ostyir.BoolLit:
		start, end := legacySpan(e.At())
		return &ast.BoolLit{PosV: start, EndV: end, Value: e.Value}, nil
	case *ostyir.CharLit:
		start, end := legacySpan(e.At())
		return &ast.CharLit{PosV: start, EndV: end, Value: e.Value}, nil
	case *ostyir.ByteLit:
		start, end := legacySpan(e.At())
		return &ast.ByteLit{PosV: start, EndV: end, Value: e.Value}, nil
	case *ostyir.StringLit:
		return legacyStringLitFromIR(e)
	case *ostyir.UnitLit:
		return nil, unsupported("expression", "unit literals are not yet supported in the LLVM IR bridge")
	case *ostyir.Ident:
		start, end := legacySpan(e.At())
		return &ast.Ident{PosV: start, EndV: end, Name: e.Name}, nil
	case *ostyir.UnaryExpr:
		return legacyUnaryExprFromIR(e)
	case *ostyir.BinaryExpr:
		return legacyBinaryExprFromIR(e)
	case *ostyir.CallExpr:
		return legacyCallExprFromIR(e)
	case *ostyir.IntrinsicCall:
		return legacyIntrinsicCallFromIR(e)
	case *ostyir.ListLit:
		return legacyListLitFromIR(e)
	case *ostyir.BlockExpr:
		return legacyBlockFromIR(e.Block)
	case *ostyir.IfExpr:
		return legacyIfExprFromIR(e)
	case *ostyir.ErrorExpr:
		return nil, unsupportedf("expression", "IR error expression: %s", e.Note)
	case *ostyir.FieldExpr:
		return legacyFieldExprFromIR(e)
	case *ostyir.IndexExpr:
		return legacyIndexExprFromIR(e)
	case *ostyir.MethodCall:
		return legacyMethodCallFromIR(e)
	case *ostyir.StructLit:
		return legacyStructLitFromIR(e)
	case *ostyir.TupleLit:
		return legacyTupleLitFromIR(e)
	case *ostyir.MapLit:
		return legacyMapLitFromIR(e)
	case *ostyir.RangeLit:
		return legacyRangeLitFromIR(e)
	case *ostyir.QuestionExpr:
		return legacyQuestionExprFromIR(e)
	case *ostyir.CoalesceExpr:
		return legacyCoalesceExprFromIR(e)
	case *ostyir.Closure:
		return legacyClosureFromIR(e)
	case *ostyir.VariantLit:
		return legacyVariantLitFromIR(e)
	case *ostyir.MatchExpr:
		return legacyMatchExprFromIR(e)
	case *ostyir.IfLetExpr:
		return legacyIfLetExprFromIR(e)
	case *ostyir.TupleAccess:
		return legacyTupleAccessFromIR(e)
	default:
		return nil, unsupportedf("expression", "IR expression %T", expr)
	}
}

func legacyStringLitFromIR(lit *ostyir.StringLit) (ast.Expr, error) {
	if lit == nil {
		return nil, nil
	}
	start, end := legacySpan(lit.At())
	out := &ast.StringLit{
		PosV:     start,
		EndV:     end,
		IsRaw:    lit.IsRaw,
		IsTriple: lit.IsTriple,
	}
	for _, part := range lit.Parts {
		entry := ast.StringPart{IsLit: part.IsLit, Lit: part.Lit}
		if !part.IsLit {
			expr, err := legacyExprFromIR(part.Expr)
			if err != nil {
				return nil, err
			}
			entry.Expr = expr
		}
		out.Parts = append(out.Parts, entry)
	}
	return out, nil
}

func legacyUnaryExprFromIR(expr *ostyir.UnaryExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	inner, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.UnaryExpr{
		PosV: start,
		EndV: end,
		Op:   legacyUnaryOp(expr.Op),
		X:    inner,
	}, nil
}

func legacyBinaryExprFromIR(expr *ostyir.BinaryExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	left, err := legacyExprFromIR(expr.Left)
	if err != nil {
		return nil, err
	}
	right, err := legacyExprFromIR(expr.Right)
	if err != nil {
		return nil, err
	}
	return &ast.BinaryExpr{
		PosV:  start,
		EndV:  end,
		Op:    legacyBinaryOp(expr.Op),
		Left:  left,
		Right: right,
	}, nil
}

func legacyCallExprFromIR(expr *ostyir.CallExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	fn, err := legacyExprFromIR(expr.Callee)
	if err != nil {
		return nil, err
	}
	if len(expr.TypeArgs) > 0 {
		tf := &ast.TurbofishExpr{PosV: start, EndV: end, Base: fn}
		for _, ta := range expr.TypeArgs {
			tf.Args = append(tf.Args, legacyTypeFromIR(ta))
		}
		fn = tf
	}
	out := &ast.CallExpr{PosV: start, EndV: end, Fn: fn}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

func legacyIntrinsicCallFromIR(expr *ostyir.IntrinsicCall) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.CallExpr{
		PosV: start,
		EndV: end,
		Fn: &ast.Ident{
			PosV: start,
			EndV: start,
			Name: legacyIntrinsicName(expr.Kind),
		},
	}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

// legacyIRArg bridges an ir.Arg into an ast.Arg.
func legacyIRArg(arg ostyir.Arg) (*ast.Arg, error) {
	value, err := legacyExprFromIR(arg.Value)
	if err != nil {
		return nil, err
	}
	pos := value.Pos()
	if arg.SpanV.Start.Line != 0 {
		pos = legacyPos(arg.SpanV.Start)
	}
	return &ast.Arg{PosV: pos, Name: arg.Name, Value: value}, nil
}

func legacyListLitFromIR(expr *ostyir.ListLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.ListExpr{PosV: start, EndV: end}
	for _, elem := range expr.Elems {
		value, err := legacyExprFromIR(elem)
		if err != nil {
			return nil, err
		}
		out.Elems = append(out.Elems, value)
	}
	return out, nil
}

func legacyIfExprFromIR(expr *ostyir.IfExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	cond, err := legacyExprFromIR(expr.Cond)
	if err != nil {
		return nil, err
	}
	thenBlock, err := legacyBlockFromIR(expr.Then)
	if err != nil {
		return nil, err
	}
	elseBlock, err := legacyBlockFromIR(expr.Else)
	if err != nil {
		return nil, err
	}
	var elseExpr ast.Expr
	if elseBlock != nil {
		elseExpr = elseBlock
	}
	return &ast.IfExpr{
		PosV: start,
		EndV: end,
		Cond: cond,
		Then: thenBlock,
		Else: elseExpr,
	}, nil
}

func legacyFieldExprFromIR(expr *ostyir.FieldExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	base, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.FieldExpr{
		PosV:       start,
		EndV:       end,
		X:          base,
		Name:       expr.Name,
		IsOptional: expr.Optional,
	}, nil
}

func legacyIndexExprFromIR(expr *ostyir.IndexExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	base, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	index, err := legacyExprFromIR(expr.Index)
	if err != nil {
		return nil, err
	}
	return &ast.IndexExpr{
		PosV:  start,
		EndV:  end,
		X:     base,
		Index: index,
	}, nil
}

func legacyMethodCallFromIR(expr *ostyir.MethodCall) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	receiver, err := legacyExprFromIR(expr.Receiver)
	if err != nil {
		return nil, err
	}
	fn := ast.Expr(&ast.FieldExpr{
		PosV: start,
		EndV: end,
		X:    receiver,
		Name: expr.Name,
	})
	typeArgs := expr.TypeArgs
	// Option B Phase 2c: method calls inside a specialized built-in
	// body (Map$String$Int.containsKey's body calling self.get(key),
	// etc.) still carry the struct-level K/V TypeArgs after
	// monomorph's SubstituteTypes — they're semantically redundant
	// (already encoded by the specialized receiver type) but trip
	// the legacy emitter with LLVM015 when wrapped in TurbofishExpr.
	// Drop them here when the receiver is a specialized built-in.
	//
	// The receiver type can be:
	//   - `*ir.NamedType` with a `_ZTS…` mangled name for specialized
	//     struct/enum owners (Map, List, Set, specialized Result),
	//   - `*ir.OptionalType` — surface form for `Option<T>`, which
	//     keeps the unmangled shape because `T?` has a direct LLVM
	//     representation as a nullable ptr. Method calls on it
	//     (`x.unwrap()`, `x.isSome()`) are still specialized-builtin
	//     dispatches and carry the same redundant owner-level args.
	if len(typeArgs) != 0 && expr.Receiver != nil {
		rt := expr.Receiver.Type()
		if opt, ok := rt.(*ostyir.OptionalType); ok && opt != nil {
			typeArgs = nil
		} else if named, ok := rt.(*ostyir.NamedType); ok && strings.HasPrefix(named.Name, "_ZTS") {
			typeArgs = nil
		}
	}
	if len(typeArgs) != 0 {
		tf := &ast.TurbofishExpr{PosV: start, EndV: end, Base: fn}
		for _, arg := range typeArgs {
			tf.Args = append(tf.Args, legacyTypeFromIR(arg))
		}
		fn = tf
	}
	out := &ast.CallExpr{PosV: start, EndV: end, Fn: fn}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

func legacyStructLitFromIR(expr *ostyir.StructLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.StructLit{
		PosV: start,
		EndV: end,
		Type: legacyTypeExpr(expr.TypeName, start, end),
	}
	for _, field := range expr.Fields {
		legacyField := &ast.StructLitField{PosV: legacyPos(field.At().Start), Name: field.Name}
		if field.Value != nil {
			value, err := legacyExprFromIR(field.Value)
			if err != nil {
				return nil, err
			}
			legacyField.Value = value
		}
		out.Fields = append(out.Fields, legacyField)
	}
	if expr.Spread != nil {
		spread, err := legacyExprFromIR(expr.Spread)
		if err != nil {
			return nil, err
		}
		out.Spread = spread
	}
	return out, nil
}

func legacyTupleLitFromIR(expr *ostyir.TupleLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.TupleExpr{PosV: start, EndV: end}
	for _, elem := range expr.Elems {
		value, err := legacyExprFromIR(elem)
		if err != nil {
			return nil, err
		}
		out.Elems = append(out.Elems, value)
	}
	return out, nil
}

func legacyMapLitFromIR(expr *ostyir.MapLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.MapExpr{PosV: start, EndV: end, Empty: len(expr.Entries) == 0}
	for _, entry := range expr.Entries {
		key, err := legacyExprFromIR(entry.Key)
		if err != nil {
			return nil, err
		}
		value, err := legacyExprFromIR(entry.Value)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, &ast.MapEntry{Key: key, Value: value})
	}
	return out, nil
}

func legacyRangeLitFromIR(expr *ostyir.RangeLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	out := &ast.RangeExpr{PosV: start, EndV: end, Inclusive: expr.Inclusive}
	var err error
	if expr.Start != nil {
		out.Start, err = legacyExprFromIR(expr.Start)
		if err != nil {
			return nil, err
		}
	}
	if expr.End != nil {
		out.Stop, err = legacyExprFromIR(expr.End)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func legacyQuestionExprFromIR(expr *ostyir.QuestionExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	inner, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.QuestionExpr{PosV: start, EndV: end, X: inner}, nil
}

func legacyCoalesceExprFromIR(expr *ostyir.CoalesceExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	left, err := legacyExprFromIR(expr.Left)
	if err != nil {
		return nil, err
	}
	right, err := legacyExprFromIR(expr.Right)
	if err != nil {
		return nil, err
	}
	return &ast.BinaryExpr{
		PosV:  start,
		EndV:  end,
		Op:    token.QQ,
		Left:  left,
		Right: right,
	}, nil
}

func legacyClosureFromIR(expr *ostyir.Closure) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	// If the lift pre-pass scheduled this closure as a top-level fn,
	// substitute either a bare Ident (no captures, Phase 1 thunk
	// path) or a synthesized maker CallExpr (with captures, Phase 4
	// env-first path). See closure_lift.go for the contract.
	if lifted := liftedClosureFor(expr); lifted != nil {
		if lifted.makerName == "" {
			return &ast.Ident{
				PosV: start,
				EndV: end,
				Name: lifted.name,
			}, nil
		}
		// Capturing closure: emit `__osty_make_closure_<n>(<cap0>, <cap1>, ...)`
		// where each arg is a bare Ident with the captured name. The
		// args evaluate at the call site in the original lexical
		// scope (which still has those bindings live) so the env
		// constructor sees the same values the closure body would
		// have read directly.
		args := make([]*ast.Arg, 0, len(lifted.captures))
		for _, cap := range lifted.captures {
			args = append(args, &ast.Arg{
				PosV: start,
				Value: &ast.Ident{
					PosV: start,
					EndV: end,
					Name: cap.name,
				},
			})
		}
		return &ast.CallExpr{
			PosV: start,
			EndV: end,
			Fn: &ast.Ident{
				PosV: start,
				EndV: end,
				Name: lifted.makerName,
			},
			Args: args,
		}, nil
	}
	out := &ast.ClosureExpr{
		PosV:       start,
		EndV:       end,
		ReturnType: legacyClosureReturnType(expr.Return),
	}
	for _, param := range expr.Params {
		legacyParam, err := legacyParamFromIR(param)
		if err != nil {
			return nil, err
		}
		out.Params = append(out.Params, legacyParam)
	}
	body, err := legacyBlockFromIR(expr.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

func legacyClosureReturnType(t ostyir.Type) ast.Type {
	if prim, ok := t.(*ostyir.PrimType); ok && prim != nil && prim.Kind == ostyir.PrimUnit {
		return nil
	}
	return legacyTypeFromIR(t)
}

func legacyVariantLitFromIR(expr *ostyir.VariantLit) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	callee := ast.Expr(&ast.Ident{PosV: start, EndV: end, Name: expr.Variant})
	if expr.Enum != "" {
		callee = &ast.FieldExpr{
			PosV: start,
			EndV: end,
			X:    &ast.Ident{PosV: start, EndV: start, Name: expr.Enum},
			Name: expr.Variant,
		}
	}
	if len(expr.Args) == 0 {
		return callee, nil
	}
	out := &ast.CallExpr{PosV: start, EndV: end, Fn: callee}
	for _, arg := range expr.Args {
		legacyArg, err := legacyIRArg(arg)
		if err != nil {
			return nil, err
		}
		out.Args = append(out.Args, legacyArg)
	}
	return out, nil
}

func legacyMatchExprFromIR(expr *ostyir.MatchExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	scrutinee, err := legacyExprFromIR(expr.Scrutinee)
	if err != nil {
		return nil, err
	}
	out := &ast.MatchExpr{PosV: start, EndV: end, Scrutinee: scrutinee}
	for _, arm := range expr.Arms {
		legacyArm, err := legacyMatchArmFromIR(arm)
		if err != nil {
			return nil, err
		}
		out.Arms = append(out.Arms, legacyArm)
	}
	return out, nil
}

func legacyMatchArmFromIR(arm *ostyir.MatchArm) (*ast.MatchArm, error) {
	if arm == nil {
		return nil, nil
	}
	start, _ := legacySpan(arm.At())
	pattern, err := legacyPatternFromIR(arm.Pattern)
	if err != nil {
		return nil, err
	}
	out := &ast.MatchArm{PosV: start, Pattern: pattern}
	if arm.Guard != nil {
		guard, err := legacyExprFromIR(arm.Guard)
		if err != nil {
			return nil, err
		}
		out.Guard = guard
	}
	body, err := legacyBlockFromIR(arm.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

func legacyIfLetExprFromIR(expr *ostyir.IfLetExpr) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	pattern, err := legacyPatternFromIR(expr.Pattern)
	if err != nil {
		return nil, err
	}
	scrutinee, err := legacyExprFromIR(expr.Scrutinee)
	if err != nil {
		return nil, err
	}
	thenBlock, err := legacyBlockFromIR(expr.Then)
	if err != nil {
		return nil, err
	}
	elseBlock, err := legacyBlockFromIR(expr.Else)
	if err != nil {
		return nil, err
	}
	var elseExpr ast.Expr
	if elseBlock != nil {
		elseExpr = elseBlock
	}
	return &ast.IfExpr{
		PosV:    start,
		EndV:    end,
		IsIfLet: true,
		Pattern: pattern,
		Cond:    scrutinee,
		Then:    thenBlock,
		Else:    elseExpr,
	}, nil
}

func legacyTupleAccessFromIR(expr *ostyir.TupleAccess) (ast.Expr, error) {
	start, end := legacySpan(expr.At())
	base, err := legacyExprFromIR(expr.X)
	if err != nil {
		return nil, err
	}
	return &ast.FieldExpr{
		PosV: start,
		EndV: end,
		X:    base,
		Name: strconv.Itoa(expr.Index),
	}, nil
}

func legacyPatternFromIR(pattern ostyir.Pattern) (ast.Pattern, error) {
	switch p := pattern.(type) {
	case nil:
		return nil, nil
	case *ostyir.WildPat:
		start, end := legacySpan(p.At())
		return &ast.WildcardPat{PosV: start, EndV: end}, nil
	case *ostyir.IdentPat:
		start, end := legacySpan(p.At())
		return &ast.IdentPat{PosV: start, EndV: end, Name: p.Name}, nil
	case *ostyir.LitPat:
		start, end := legacySpan(p.At())
		lit, err := legacyExprFromIR(p.Value)
		if err != nil {
			return nil, err
		}
		return &ast.LiteralPat{PosV: start, EndV: end, Literal: lit}, nil
	case *ostyir.TuplePat:
		start, end := legacySpan(p.At())
		out := &ast.TuplePat{PosV: start, EndV: end}
		for _, elem := range p.Elems {
			legacyElem, err := legacyPatternFromIR(elem)
			if err != nil {
				return nil, err
			}
			out.Elems = append(out.Elems, legacyElem)
		}
		return out, nil
	case *ostyir.StructPat:
		start, end := legacySpan(p.At())
		out := &ast.StructPat{
			PosV: start,
			EndV: end,
			Type: splitQualifiedName(p.TypeName),
			Rest: p.Rest,
		}
		for _, field := range p.Fields {
			pat, err := legacyPatternFromIR(field.Pattern)
			if err != nil {
				return nil, err
			}
			out.Fields = append(out.Fields, &ast.StructPatField{
				PosV:    legacyPos(field.At().Start),
				Name:    field.Name,
				Pattern: pat,
			})
		}
		return out, nil
	case *ostyir.VariantPat:
		start, end := legacySpan(p.At())
		out := &ast.VariantPat{PosV: start, EndV: end}
		if p.Enum != "" {
			out.Path = append(out.Path, p.Enum)
		}
		out.Path = append(out.Path, p.Variant)
		for _, arg := range p.Args {
			legacyArg, err := legacyPatternFromIR(arg)
			if err != nil {
				return nil, err
			}
			out.Args = append(out.Args, legacyArg)
		}
		return out, nil
	case *ostyir.RangePat:
		start, end := legacySpan(p.At())
		out := &ast.RangePat{PosV: start, EndV: end, Inclusive: p.Inclusive}
		var err error
		if p.Low != nil {
			out.Start, err = legacyExprFromIR(p.Low)
			if err != nil {
				return nil, err
			}
		}
		if p.High != nil {
			out.Stop, err = legacyExprFromIR(p.High)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case *ostyir.OrPat:
		start, end := legacySpan(p.At())
		out := &ast.OrPat{PosV: start, EndV: end}
		for _, alt := range p.Alts {
			legacyAlt, err := legacyPatternFromIR(alt)
			if err != nil {
				return nil, err
			}
			out.Alts = append(out.Alts, legacyAlt)
		}
		return out, nil
	case *ostyir.BindingPat:
		start, end := legacySpan(p.At())
		inner, err := legacyPatternFromIR(p.Pattern)
		if err != nil {
			return nil, err
		}
		return &ast.BindingPat{
			PosV:    start,
			EndV:    end,
			Name:    p.Name,
			Pattern: inner,
		}, nil
	case *ostyir.ErrorPat:
		return nil, unsupportedf("pattern", "IR error pattern: %s", p.Note)
	default:
		return nil, unsupportedf("pattern", "IR pattern %T", pattern)
	}
}

// legacyUnaryOp maps the IR-level `UnOp` to the `token.Kind` used by
// AST UnaryExpr nodes. Delegates to the Osty-sourced
// `mirLegacyUnaryOpCode`.
func legacyUnaryOp(op ostyir.UnOp) token.Kind {
	return token.Kind(mirLegacyUnaryOpCode(int(op),
		int(ostyir.UnNeg), int(ostyir.UnPlus), int(ostyir.UnNot), int(ostyir.UnBitNot),
		int(token.MINUS), int(token.PLUS), int(token.NOT), int(token.BITNOT),
		int(token.ILLEGAL),
	))
}

// legacyBinaryOp maps the IR-level `BinOp` to the `token.Kind` used by
// AST BinaryExpr nodes. Delegates to the Osty-sourced
// `mirLegacyBinaryOpCode`.
func legacyBinaryOp(op ostyir.BinOp) token.Kind {
	return token.Kind(mirLegacyBinaryOpCode(int(op),
		int(ostyir.BinAdd), int(ostyir.BinSub), int(ostyir.BinMul),
		int(ostyir.BinDiv), int(ostyir.BinMod),
		int(ostyir.BinEq), int(ostyir.BinNeq),
		int(ostyir.BinLt), int(ostyir.BinLeq),
		int(ostyir.BinGt), int(ostyir.BinGeq),
		int(ostyir.BinAnd), int(ostyir.BinOr),
		int(ostyir.BinBitAnd), int(ostyir.BinBitOr), int(ostyir.BinBitXor),
		int(ostyir.BinShl), int(ostyir.BinShr),
		int(token.PLUS), int(token.MINUS), int(token.STAR),
		int(token.SLASH), int(token.PERCENT),
		int(token.EQ), int(token.NEQ),
		int(token.LT), int(token.LEQ), int(token.GT), int(token.GEQ),
		int(token.AND), int(token.OR),
		int(token.BITAND), int(token.BITOR), int(token.BITXOR),
		int(token.SHL), int(token.SHR),
		int(token.ILLEGAL),
	))
}

// legacyIntrinsicName returns the user-visible name for an
// `ir.IntrinsicKind`. Delegates to the Osty-sourced
// `mirLegacyIntrinsicName`. Note this is distinct from the MIR
// IntrinsicKind label table — IR-level intrinsics today only cover the
// print family, while MIR's `mirIntrinsicLabel` covers ~114 cases.
func legacyIntrinsicName(kind ostyir.IntrinsicKind) string {
	return mirLegacyIntrinsicName(int(kind),
		int(ostyir.IntrinsicPrint), int(ostyir.IntrinsicPrintln),
		int(ostyir.IntrinsicEprint), int(ostyir.IntrinsicEprintln),
	)
}

func legacyTypeExpr(name string, start, end token.Pos) ast.Expr {
	parts := splitQualifiedName(name)
	if len(parts) == 0 {
		return &ast.Ident{PosV: start, EndV: end, Name: name}
	}
	expr := ast.Expr(&ast.Ident{PosV: start, EndV: start, Name: parts[0]})
	for _, part := range parts[1:] {
		expr = &ast.FieldExpr{PosV: start, EndV: end, X: expr, Name: part}
	}
	return expr
}

func splitQualifiedName(name string) []string {
	return mirSplitQualifiedName(name)
}

func legacySpan(span ostyir.Span) (token.Pos, token.Pos) {
	return legacyPos(span.Start), legacyPos(span.End)
}

func legacyPos(pos ostyir.Pos) token.Pos {
	return token.Pos{
		Offset: pos.Offset,
		Line:   pos.Line,
		Column: pos.Column,
	}
}
