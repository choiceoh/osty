package resolve

// Conditional compilation — `#[cfg(key = "value")]` pre-resolve filter
// per LANG_SPEC v0.5 §5 (G29).
//
// Declarations carrying a `#[cfg(...)]` whose argument evaluates to
// false are removed from the file's declaration list before any name
// resolution, type check, or cross-file symbol walk runs. The filter
// is intentionally done here (not in the parser) so the AST keeps the
// original declaration shape and tooling that consumes the file
// before resolve — formatter, linter — sees the unfiltered program.
//
// v0.5 scope: simple `key = "value"` form only. Composite
// `all(...)` / `any(...)` / `not(...)` forms are deferred to the next
// Phase-2 sub-phase (the annotation argument grammar in
// `toolchain/parser.osty` does not yet accept nested function-like
// calls). Multiple `#[cfg(...)]` annotations on the same declaration
// are conjoined — every one must pass for the declaration to survive.
//
// Keys accepted in v0.5: `os`, `target`, `arch`, `feature`. Any other
// key is `E0405`. Unknown values in a recognised key simply evaluate
// to false — they do not emit a diagnostic, matching the
// forward-compatibility behaviour of Rust's `cfg`.

import (
	"runtime"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

// CfgEnv carries the values that `#[cfg(key = "value")]` predicates
// compare against. Empty Target / Arch fall back to the Go host
// values.
type CfgEnv struct {
	// OS is the operating system class. Values match Go's
	// `runtime.GOOS` nomenclature ("linux", "darwin", "windows",
	// "freebsd", "wasm", ...).
	OS string
	// Arch is the CPU architecture class. Values match Go's
	// `runtime.GOARCH` nomenclature ("amd64", "arm64", "wasm", ...).
	Arch string
	// Target is the compilation-target label. When the build invokes
	// cross-compilation this differs from OS/Arch; otherwise the
	// build driver sets this to a string the user chose (typically
	// matching the GOOS value but potentially distinguishing, e.g.,
	// "wasm" from native "linux").
	Target string
	// Features is the set of feature flags active for the build.
	// Keys are feature names; presence means "enabled."
	Features map[string]bool
}

// DefaultCfgEnv returns a CfgEnv populated from the Go host runtime.
// The build driver overrides individual fields when cross-compiling.
func DefaultCfgEnv() *CfgEnv {
	return &CfgEnv{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Target:   runtime.GOOS,
		Features: map[string]bool{},
	}
}

// toSelfhost projects this Go-side CfgEnv onto the selfhost.CfgEnv the
// bootstrapped resolver consumes. Returns nil for a nil receiver so the
// native path inherits "no filtering" behaviour without extra casing.
func (c *CfgEnv) toSelfhost() *selfhost.CfgEnv {
	if c == nil {
		return nil
	}
	features := make([]string, 0, len(c.Features))
	for name, enabled := range c.Features {
		if enabled {
			features = append(features, name)
		}
	}
	return &selfhost.CfgEnv{
		OS:       c.OS,
		Arch:     c.Arch,
		Target:   c.Target,
		Features: features,
	}
}

// filterCfgDecls removes declarations whose `#[cfg(...)]` guard
// evaluates to false against env, and returns any diagnostics produced
// by ill-formed cfg annotations. The file's Decls slice is replaced
// in place.
func filterCfgDecls(file *ast.File, env *CfgEnv) []*diag.Diagnostic {
	if file == nil || len(file.Decls) == 0 {
		return nil
	}
	var diags []*diag.Diagnostic
	kept := file.Decls[:0]
	for _, d := range file.Decls {
		pass, ds := evaluateCfgOnDecl(d, env)
		diags = append(diags, ds...)
		if pass {
			kept = append(kept, d)
		}
	}
	file.Decls = kept
	return diags
}

// evaluateCfgOnDecl returns whether a declaration survives the cfg
// filter and the diagnostics produced by ill-formed cfg arguments.
// Declarations without `#[cfg(...)]` always pass.
func evaluateCfgOnDecl(d ast.Decl, env *CfgEnv) (bool, []*diag.Diagnostic) {
	annots := annotationsOf(d)
	if len(annots) == 0 {
		return true, nil
	}
	var diags []*diag.Diagnostic
	pass := true
	for _, a := range annots {
		if a.Name != "cfg" {
			continue
		}
		result, ds := evaluateCfgAnnotation(a, env)
		diags = append(diags, ds...)
		if !result {
			pass = false
		}
	}
	return pass, diags
}

// evaluateCfgAnnotation walks one `#[cfg(...)]` annotation and
// returns its boolean verdict plus any argument diagnostics.
func evaluateCfgAnnotation(a *ast.Annotation, env *CfgEnv) (bool, []*diag.Diagnostic) {
	if len(a.Args) == 0 {
		return false, []*diag.Diagnostic{
			diag.New(diag.Error,
				"`#[cfg(...)]` requires at least one predicate").
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(a.PosV, "empty cfg predicate").
				Note("v0.5 §5 / G29: use `#[cfg(key = \"value\")]` where key is `os`, `target`, `arch`, or `feature`").
				Build(),
		}
	}
	// v0.5 simple form: every argument must be `key = "value"` and all
	// arguments are conjoined. Composite `all`/`any`/`not` requires
	// nested annotation-argument grammar, deferred to the next Phase-2
	// sub-phase.
	var diags []*diag.Diagnostic
	pass := true
	for _, arg := range a.Args {
		result, ds := evaluateCfgArg(arg, env)
		diags = append(diags, ds...)
		if !result {
			pass = false
		}
	}
	return pass, diags
}

// evaluateCfgArg handles one `key = "value"` pair.
func evaluateCfgArg(arg *ast.AnnotationArg, env *CfgEnv) (bool, []*diag.Diagnostic) {
	value, ok := stringArg(arg.Value)
	if !ok {
		return false, []*diag.Diagnostic{
			diag.New(diag.Error,
				"`#[cfg("+arg.Key+" = ...)]` requires a string literal").
				Code(diag.CodeAnnotationBadArg).
				PrimaryPos(arg.PosV, "expected string literal").
				Note("v0.5 §5: cfg argument values must be string literals — no identifiers or computed expressions").
				Build(),
		}
	}
	switch arg.Key {
	case "os":
		return env.OS == value, nil
	case "target":
		return env.Target == value, nil
	case "arch":
		return env.Arch == value, nil
	case "feature":
		return env.Features[value], nil
	default:
		return false, []*diag.Diagnostic{
			diag.New(diag.Error,
				"unknown cfg key `"+arg.Key+"`").
				Code(diag.CodeCfgUnknownKey).
				PrimaryPos(arg.PosV, "unrecognised cfg key").
				Note("v0.5 §5 / G29 recognises only `os`, `target`, `arch`, `feature`").
				Build(),
		}
	}
}

// annotationsOf extracts the annotation slice on any declaration
// kind that can carry annotations. Returns nil for decl types that
// don't accept annotations.
func annotationsOf(d ast.Decl) []*ast.Annotation {
	switch x := d.(type) {
	case *ast.FnDecl:
		return x.Annotations
	case *ast.StructDecl:
		return x.Annotations
	case *ast.EnumDecl:
		return x.Annotations
	case *ast.InterfaceDecl:
		return x.Annotations
	case *ast.TypeAliasDecl:
		return x.Annotations
	case *ast.LetDecl:
		return x.Annotations
	}
	return nil
}
