// Package pipeline runs the full Osty front-end (lex → parse → resolve →
// check → lint) over a single source buffer and records per-phase
// timing + output stats. It is the engine behind the `osty pipeline`
// subcommand and the `--trace` global flag — both want the same
// numbers, just rendered differently.
//
// Single-file mode only. The package isn't trying to subsume the
// manifest-driven build orchestrator; for multi-file packages call
// resolve.LoadPackage / check.Package directly.
package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// declName returns the user-visible name of a top-level declaration,
// or a placeholder when the AST node has no Name field. Used by the
// per-decl timing table so each row is identifiable at a glance.
func declName(d ast.Decl) string {
	switch n := d.(type) {
	case *ast.FnDecl:
		return n.Name
	case *ast.StructDecl:
		return n.Name
	case *ast.EnumDecl:
		return n.Name
	case *ast.InterfaceDecl:
		return n.Name
	case *ast.TypeAliasDecl:
		return n.Name
	case *ast.LetDecl:
		return n.Name
	case *ast.UseDecl:
		return n.RawPath
	}
	return "<anonymous>"
}

// declKind returns a short human label for a declaration's category.
func declKind(d ast.Decl) string {
	switch d.(type) {
	case *ast.FnDecl:
		return "fn"
	case *ast.StructDecl:
		return "struct"
	case *ast.EnumDecl:
		return "enum"
	case *ast.InterfaceDecl:
		return "interface"
	case *ast.TypeAliasDecl:
		return "alias"
	case *ast.LetDecl:
		return "let"
	case *ast.UseDecl:
		return "use"
	}
	return "?"
}

// Stage records what happened during one pipeline phase. Output is a
// human-readable one-line summary of what the phase produced (e.g.
// "1234 tokens", "42 decls, 89 stmts"); machine consumers should read
// the typed counts instead.
type Stage struct {
	Name     string        // "lex", "parse", "resolve", "check", "lint"
	Duration time.Duration // wall-clock time spent in the phase
	Output   string        // human-readable summary of what came out
	Errors   int           // diagnostic count at Severity == Error
	Warnings int           // diagnostic count at Severity == Warning
	// Counts holds the per-phase metric values keyed by metric name.
	// JSON output uses these. Kept open-ended so individual phases can
	// add more dimensions without reshaping every consumer.
	Counts map[string]int
}

// Result bundles the stage stats with the actual artefacts produced
// along the way. Callers that just want the table can ignore the
// pointers; callers that want to do something with the AST / resolve
// result (e.g. the existing single-file subcommands when --trace is
// set) read them directly.
type Result struct {
	Stages   []Stage
	Tokens   []token.Token
	File     *ast.File
	Resolve  *resolve.Result
	Check    *check.Result
	Lint     *lint.Result
	AllDiags []*diag.Diagnostic // every diag from every phase, in phase order

	// PerDecl is populated when Config.PerDecl is set. One entry per
	// (declaration, pass) pair the type checker visited. Sorted by
	// total descending in RenderText / RenderJSON.
	PerDecl []DeclTiming

	// GenBytes, GenError are populated when Config.RunGen is set.
	GenBytes []byte
	GenError error
}

// DeclTiming records how long the type checker spent on one
// declaration during one pass ("collect" or "check").
type DeclTiming struct {
	Name     string        `json:"name"`  // best-effort declaration name
	Kind     string        `json:"kind"`  // "fn", "struct", "enum", …
	Phase    string        `json:"phase"` // "collect" | "check"
	Duration time.Duration `json:"-"`
	// DurationMS mirrors Duration for JSON consumers.
	DurationMS float64 `json:"duration_ms"`
}

// Config tweaks what Run does. The zero value runs the standard
// front-end (lex → parse → resolve → check → lint) without optional
// instrumentation; opting into PerDecl or RunGen extends what gets
// recorded but never changes the order or count of the always-on
// phases.
type Config struct {
	// PerDecl turns on the type checker's OnDecl callback so the
	// returned Result includes a per-declaration timing table.
	PerDecl bool

	// RunGen runs the Go transpiler as a sixth pipeline phase. The
	// returned GenBytes are the (possibly partial) Go source the
	// transpiler emitted; GenError holds any fatal generator error.
	RunGen bool

	// GenPackageName is the Go package clause used when RunGen is set.
	// Single-file mode defaults to "main"; package/workspace mode derives
	// a valid Go identifier from the Osty package name when this is empty.
	GenPackageName string
}

func genPackageName(cfgName, fallback string) string {
	if cfgName != "" {
		return sanitizeGoPackageName(cfgName)
	}
	if fallback == "" {
		return "main"
	}
	return sanitizeGoPackageName(fallback)
}

func sanitizeGoPackageName(name string) string {
	var b strings.Builder
	for i, r := range name {
		ok := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			continue
		}
		if i == 0 && r >= '0' && r <= '9' {
			b.WriteByte('_')
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := b.String()
	if out == "" || strings.Trim(out, "_") == "" {
		return "main"
	}
	if goKeywords[out] {
		return "_" + out
	}
	return out
}

var goKeywords = map[string]bool{
	"break":       true,
	"default":     true,
	"func":        true,
	"interface":   true,
	"select":      true,
	"case":        true,
	"defer":       true,
	"go":          true,
	"map":         true,
	"struct":      true,
	"chan":        true,
	"else":        true,
	"goto":        true,
	"package":     true,
	"switch":      true,
	"const":       true,
	"fallthrough": true,
	"if":          true,
	"range":       true,
	"type":        true,
	"continue":    true,
	"for":         true,
	"import":      true,
	"return":      true,
	"var":         true,
}

// Run executes lex → parse → resolve → check → lint over src with
// default Config and returns a fully populated Result. See RunWithConfig
// for the optional per-decl timing and gen-phase extensions.
//
// stream, if non-nil, receives one human-readable trace line per
// completed phase (used by the --trace global flag). Pass nil to
// suppress streaming output.
func Run(src []byte, stream io.Writer) Result {
	return RunWithConfig(src, stream, Config{})
}

// RunWithConfig is the configurable form of Run. The stage list always
// includes lex/parse/resolve/check/lint in that order; gen is appended
// when cfg.RunGen is set. Per-decl timing is collected when cfg.PerDecl
// is set and surfaces in Result.PerDecl.
func RunWithConfig(src []byte, stream io.Writer, cfg Config) Result {
	var r Result
	emit := func(s Stage) {
		r.Stages = append(r.Stages, s)
		if stream != nil {
			fmt.Fprintf(stream, "trace: %-8s %8s   %s\n",
				s.Name, formatDuration(s.Duration), traceTail(s))
		}
	}

	// --- lex ---
	t0 := time.Now()
	l := lexer.New(src)
	toks := l.Lex()
	lexDiags := l.Errors()
	r.Tokens = toks
	r.AllDiags = append(r.AllDiags, lexDiags...)
	emit(Stage{
		Name:     "lex",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d tokens", len(toks)),
		Errors:   countSeverity(lexDiags, diag.Error),
		Warnings: countSeverity(lexDiags, diag.Warning),
		Counts:   map[string]int{"tokens": len(toks)},
	})

	// --- parse ---
	t0 = time.Now()
	file, parseDiags := parser.ParseDiagnostics(src)
	r.File = file
	r.AllDiags = append(r.AllDiags, parseDiags...)
	declCount, stmtCount, useCount := 0, 0, 0
	if file != nil {
		declCount = len(file.Decls)
		stmtCount = len(file.Stmts)
		useCount = len(file.Uses)
	}
	emit(Stage{
		Name:     "parse",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d decls, %d top-level stmts, %d use", declCount, stmtCount, useCount),
		Errors:   countSeverity(parseDiags, diag.Error),
		Warnings: countSeverity(parseDiags, diag.Warning),
		Counts: map[string]int{
			"decls": declCount,
			"stmts": stmtCount,
			"uses":  useCount,
		},
	})

	// --- resolve ---
	t0 = time.Now()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	r.Resolve = res
	r.AllDiags = append(r.AllDiags, res.Diags...)
	emit(Stage{
		Name:     "resolve",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d refs, %d type refs", len(res.Refs), len(res.TypeRefs)),
		Errors:   countSeverity(res.Diags, diag.Error),
		Warnings: countSeverity(res.Diags, diag.Warning),
		Counts: map[string]int{
			"refs":      len(res.Refs),
			"type_refs": len(res.TypeRefs),
		},
	})

	// --- check ---
	t0 = time.Now()
	reg := stdlib.LoadCached()
	checkOpts := check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods}
	if cfg.PerDecl {
		checkOpts.OnDecl = func(d ast.Decl, phase string, dur time.Duration) {
			r.PerDecl = append(r.PerDecl, DeclTiming{
				Name:       declName(d),
				Kind:       declKind(d),
				Phase:      phase,
				Duration:   dur,
				DurationMS: float64(dur.Microseconds()) / 1000.0,
			})
		}
	}
	chk := check.File(file, res, checkOpts)
	r.Check = chk
	r.AllDiags = append(r.AllDiags, chk.Diags...)
	typedExprs := 0
	for _, t := range chk.Types {
		if !types.IsError(t) {
			typedExprs++
		}
	}
	emit(Stage{
		Name:     "check",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d typed exprs, %d let bindings", typedExprs, len(chk.LetTypes)),
		Errors:   countSeverity(chk.Diags, diag.Error),
		Warnings: countSeverity(chk.Diags, diag.Warning),
		Counts: map[string]int{
			"typed_exprs": typedExprs,
			"let_types":   len(chk.LetTypes),
			"sym_types":   len(chk.SymTypes),
			"instantiate": len(chk.Instantiations),
		},
	})

	// --- lint ---
	t0 = time.Now()
	lr := lint.File(file, res, chk)
	r.Lint = lr
	r.AllDiags = append(r.AllDiags, lr.Diags...)
	emit(Stage{
		Name:     "lint",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d lint findings", len(lr.Diags)),
		Errors:   countSeverity(lr.Diags, diag.Error),
		Warnings: countSeverity(lr.Diags, diag.Warning),
		Counts:   map[string]int{"findings": len(lr.Diags)},
	})

	// --- gen (optional) ---
	// The transpiler is only meaningful on a clean front-end: a check
	// error means the type info gen relies on is partial, and the Go
	// it would emit is at best unhelpful. We still run gen if the user
	// asked, but report the error in the stage stats so the table
	// makes the situation obvious.
	if cfg.RunGen {
		t0 = time.Now()
		pkg := genPackageName(cfg.GenPackageName, "main")
		out, err := gen.Generate(pkg, file, res, chk)
		r.GenBytes = out
		r.GenError = err
		genErr := 0
		summary := fmt.Sprintf("%d bytes Go", len(out))
		if err != nil {
			genErr = 1
			summary = fmt.Sprintf("%d bytes Go (error: %v)", len(out), err)
		}
		emit(Stage{
			Name:     "gen",
			Duration: time.Since(t0),
			Output:   summary,
			Errors:   genErr,
			Warnings: 0,
			Counts:   map[string]int{"bytes": len(out)},
		})
	}

	return r
}

// RunPackage runs the front-end over a directory of `.osty` files,
// using the same stage shape as Run. The stages are renamed slightly
// so the table reflects what actually happened: "load" combines the
// loader's lex + parse step (LoadPackage doesn't expose them
// separately), and the rest mirrors Run. Workspaces are not handled
// here — point at a leaf package directory.
//
// If LoadPackage fails (e.g. directory unreadable), the returned
// Result has empty Stages and the error is returned to the caller so
// the CLI can decide whether to abort.
func RunPackage(dir string, stream io.Writer, cfg Config) (Result, error) {
	var r Result
	emit := func(s Stage) {
		r.Stages = append(r.Stages, s)
		if stream != nil {
			fmt.Fprintf(stream, "trace: %-8s %8s   %s\n",
				s.Name, formatDuration(s.Duration), traceTail(s))
		}
	}

	// --- load (lex + parse, all files) ---
	t0 := time.Now()
	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		return r, err
	}
	totalBytes, totalDecls, totalStmts, totalUses := 0, 0, 0, 0
	var loadDiags []*diag.Diagnostic
	for _, pf := range pkg.Files {
		totalBytes += len(pf.Source)
		loadDiags = append(loadDiags, pf.ParseDiags...)
		if pf.File != nil {
			totalDecls += len(pf.File.Decls)
			totalStmts += len(pf.File.Stmts)
			totalUses += len(pf.File.Uses)
		}
	}
	r.AllDiags = append(r.AllDiags, loadDiags...)
	emit(Stage{
		Name:     "load",
		Duration: time.Since(t0),
		Output: fmt.Sprintf("%d files, %d bytes, %d decls",
			len(pkg.Files), totalBytes, totalDecls),
		Errors:   countSeverity(loadDiags, diag.Error),
		Warnings: countSeverity(loadDiags, diag.Warning),
		Counts: map[string]int{
			"files": len(pkg.Files),
			"bytes": totalBytes,
			"decls": totalDecls,
			"stmts": totalStmts,
			"uses":  totalUses,
		},
	})

	// --- resolve (whole-package) ---
	t0 = time.Now()
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	r.AllDiags = append(r.AllDiags, res.Diags...)
	totalRefs, totalTypeRefs := 0, 0
	for _, pf := range pkg.Files {
		totalRefs += len(pf.Refs)
		totalTypeRefs += len(pf.TypeRefs)
	}
	emit(Stage{
		Name:     "resolve",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d refs, %d type refs", totalRefs, totalTypeRefs),
		Errors:   countSeverity(res.Diags, diag.Error),
		Warnings: countSeverity(res.Diags, diag.Warning),
		Counts: map[string]int{
			"refs":      totalRefs,
			"type_refs": totalTypeRefs,
		},
	})

	// --- check (whole-package) ---
	t0 = time.Now()
	reg := stdlib.LoadCached()
	checkOpts := check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods}
	if cfg.PerDecl {
		checkOpts.OnDecl = func(d ast.Decl, phase string, dur time.Duration) {
			r.PerDecl = append(r.PerDecl, DeclTiming{
				Name:       declName(d),
				Kind:       declKind(d),
				Phase:      phase,
				Duration:   dur,
				DurationMS: float64(dur.Microseconds()) / 1000.0,
			})
		}
	}
	chk := check.Package(pkg, res, checkOpts)
	r.Check = chk
	r.AllDiags = append(r.AllDiags, chk.Diags...)
	typedExprs := 0
	for _, t := range chk.Types {
		if !types.IsError(t) {
			typedExprs++
		}
	}
	emit(Stage{
		Name:     "check",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d typed exprs, %d let bindings", typedExprs, len(chk.LetTypes)),
		Errors:   countSeverity(chk.Diags, diag.Error),
		Warnings: countSeverity(chk.Diags, diag.Warning),
		Counts: map[string]int{
			"typed_exprs": typedExprs,
			"let_types":   len(chk.LetTypes),
			"sym_types":   len(chk.SymTypes),
			"instantiate": len(chk.Instantiations),
		},
	})

	// --- lint (per-file, aggregated) ---
	t0 = time.Now()
	var lintDiags []*diag.Diagnostic
	for _, pf := range pkg.Files {
		fileRes := &resolve.Result{
			Refs:      pf.Refs,
			TypeRefs:  pf.TypeRefs,
			FileScope: pf.FileScope,
		}
		lr := lint.File(pf.File, fileRes, chk)
		lintDiags = append(lintDiags, lr.Diags...)
	}
	r.AllDiags = append(r.AllDiags, lintDiags...)
	emit(Stage{
		Name:     "lint",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d lint findings (across %d files)", len(lintDiags), len(pkg.Files)),
		Errors:   countSeverity(lintDiags, diag.Error),
		Warnings: countSeverity(lintDiags, diag.Warning),
		Counts:   map[string]int{"findings": len(lintDiags)},
	})

	// gen (optional, per-file aggregated). The transpiler is per-file;
	// in package mode we run it once per file with that file's
	// resolve view but the shared check.Result, then sum bytes-out
	// and surface the first fatal error. Output bytes are concatenated
	// into r.GenBytes with file headers so a downstream consumer can
	// still reconstruct per-file boundaries.
	if cfg.RunGen {
		t0 = time.Now()
		pkgName := genPackageName(cfg.GenPackageName, pkg.Name)
		var genBuf []byte
		var firstErr error
		genErrs := 0
		for _, pf := range pkg.Files {
			if pf.File == nil {
				continue
			}
			fileRes := &resolve.Result{
				Refs:      pf.Refs,
				TypeRefs:  pf.TypeRefs,
				FileScope: pf.FileScope,
			}
			out, err := gen.Generate(pkgName, pf.File, fileRes, chk)
			header := fmt.Sprintf("\n// ---- %s ----\n", pf.Path)
			genBuf = append(genBuf, []byte(header)...)
			genBuf = append(genBuf, out...)
			if err != nil {
				genErrs++
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		r.GenBytes = genBuf
		r.GenError = firstErr
		summary := fmt.Sprintf("%d bytes Go (across %d files)", len(genBuf), len(pkg.Files))
		if firstErr != nil {
			summary = fmt.Sprintf("%d bytes Go, %d file error(s); first: %v",
				len(genBuf), genErrs, firstErr)
		}
		emit(Stage{
			Name:     "gen",
			Duration: time.Since(t0),
			Output:   summary,
			Errors:   genErrs,
			Warnings: 0,
			Counts: map[string]int{
				"bytes":       len(genBuf),
				"files":       len(pkg.Files),
				"file_errors": genErrs,
			},
		})
	}

	return r, nil
}

// RunWorkspace runs the front-end across every package in a workspace
// rooted at dir. Stages here are still load → resolve → check → lint
// (+ optional gen) but each stage's counts roll up across every
// package. Per-package diagnostic ordering is preserved in AllDiags so
// downstream renderers can still attribute findings.
//
// A workspace contains zero or more package directories underneath a
// root marked by `osty.toml [workspace]` or by an empty root holding
// only subdirectories with `.osty` files. Use this when --pipeline
// runs on the workspace root; for a leaf package call RunPackage.
func RunWorkspace(dir string, stream io.Writer, cfg Config) (Result, error) {
	var r Result
	emit := func(s Stage) {
		r.Stages = append(r.Stages, s)
		if stream != nil {
			fmt.Fprintf(stream, "trace: %-8s %8s   %s\n",
				s.Name, formatDuration(s.Duration), traceTail(s))
		}
	}

	// --- load (lex + parse, every package) ---
	t0 := time.Now()
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		return r, err
	}
	ws.Stdlib = stdlib.LoadCached()
	for _, p := range resolve.WorkspacePackagePaths(dir) {
		_, _ = ws.LoadPackage(p)
	}

	totalFiles, totalBytes, totalDecls, totalStmts, totalUses := 0, 0, 0, 0, 0
	var loadDiags []*diag.Diagnostic
	pkgPaths := make([]string, 0, len(ws.Packages))
	for p := range ws.Packages {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)
	for _, p := range pkgPaths {
		pkg := ws.Packages[p]
		if pkg == nil {
			continue
		}
		for _, pf := range pkg.Files {
			totalFiles++
			totalBytes += len(pf.Source)
			loadDiags = append(loadDiags, pf.ParseDiags...)
			if pf.File != nil {
				totalDecls += len(pf.File.Decls)
				totalStmts += len(pf.File.Stmts)
				totalUses += len(pf.File.Uses)
			}
		}
	}
	r.AllDiags = append(r.AllDiags, loadDiags...)
	emit(Stage{
		Name:     "load",
		Duration: time.Since(t0),
		Output: fmt.Sprintf("%d packages, %d files, %d bytes, %d decls",
			len(pkgPaths), totalFiles, totalBytes, totalDecls),
		Errors:   countSeverity(loadDiags, diag.Error),
		Warnings: countSeverity(loadDiags, diag.Warning),
		Counts: map[string]int{
			"packages": len(pkgPaths),
			"files":    totalFiles,
			"bytes":    totalBytes,
			"decls":    totalDecls,
			"stmts":    totalStmts,
			"uses":     totalUses,
		},
	})

	// --- resolve (workspace-wide) ---
	t0 = time.Now()
	results := ws.ResolveAll()
	totalRefs, totalTypeRefs := 0, 0
	var resolveDiags []*diag.Diagnostic
	for _, p := range pkgPaths {
		pr, ok := results[p]
		if !ok || pr == nil {
			continue
		}
		resolveDiags = append(resolveDiags, pr.Diags...)
		pkg := ws.Packages[p]
		if pkg == nil {
			continue
		}
		for _, pf := range pkg.Files {
			totalRefs += len(pf.Refs)
			totalTypeRefs += len(pf.TypeRefs)
		}
	}
	r.AllDiags = append(r.AllDiags, resolveDiags...)
	emit(Stage{
		Name:     "resolve",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d refs, %d type refs", totalRefs, totalTypeRefs),
		Errors:   countSeverity(resolveDiags, diag.Error),
		Warnings: countSeverity(resolveDiags, diag.Warning),
		Counts: map[string]int{
			"refs":      totalRefs,
			"type_refs": totalTypeRefs,
		},
	})

	// --- check (cross-package) ---
	t0 = time.Now()
	reg := stdlib.LoadCached()
	checkOpts := check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods}
	if cfg.PerDecl {
		checkOpts.OnDecl = func(d ast.Decl, phase string, dur time.Duration) {
			r.PerDecl = append(r.PerDecl, DeclTiming{
				Name:       declName(d),
				Kind:       declKind(d),
				Phase:      phase,
				Duration:   dur,
				DurationMS: float64(dur.Microseconds()) / 1000.0,
			})
		}
	}
	checks := check.Workspace(ws, results, checkOpts)
	var checkDiags []*diag.Diagnostic
	totalTypedExprs, totalLetTypes, totalSymTypes := 0, 0, 0
	for _, p := range pkgPaths {
		cr, ok := checks[p]
		if !ok || cr == nil {
			continue
		}
		checkDiags = append(checkDiags, cr.Diags...)
		for _, t := range cr.Types {
			if !types.IsError(t) {
				totalTypedExprs++
			}
		}
		totalLetTypes += len(cr.LetTypes)
		totalSymTypes += len(cr.SymTypes)
	}
	r.AllDiags = append(r.AllDiags, checkDiags...)
	emit(Stage{
		Name:     "check",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d typed exprs, %d let bindings", totalTypedExprs, totalLetTypes),
		Errors:   countSeverity(checkDiags, diag.Error),
		Warnings: countSeverity(checkDiags, diag.Warning),
		Counts: map[string]int{
			"typed_exprs": totalTypedExprs,
			"let_types":   totalLetTypes,
			"sym_types":   totalSymTypes,
		},
	})

	// --- lint (per-file across every package) ---
	t0 = time.Now()
	var lintDiags []*diag.Diagnostic
	for _, p := range pkgPaths {
		pkg := ws.Packages[p]
		cr := checks[p]
		if pkg == nil || cr == nil {
			continue
		}
		for _, pf := range pkg.Files {
			fileRes := &resolve.Result{
				Refs:      pf.Refs,
				TypeRefs:  pf.TypeRefs,
				FileScope: pf.FileScope,
			}
			lr := lint.File(pf.File, fileRes, cr)
			lintDiags = append(lintDiags, lr.Diags...)
		}
	}
	r.AllDiags = append(r.AllDiags, lintDiags...)
	emit(Stage{
		Name:     "lint",
		Duration: time.Since(t0),
		Output:   fmt.Sprintf("%d lint findings (across %d packages)", len(lintDiags), len(pkgPaths)),
		Errors:   countSeverity(lintDiags, diag.Error),
		Warnings: countSeverity(lintDiags, diag.Warning),
		Counts:   map[string]int{"findings": len(lintDiags)},
	})

	// --- gen (workspace-wide aggregation) ---
	if cfg.RunGen {
		t0 = time.Now()
		var genBuf []byte
		var firstErr error
		genErrs := 0
		for _, p := range pkgPaths {
			pkg := ws.Packages[p]
			cr := checks[p]
			if pkg == nil || cr == nil {
				continue
			}
			pkgName := cfg.GenPackageName
			pkgName = genPackageName(pkgName, pkg.Name)
			for _, pf := range pkg.Files {
				if pf.File == nil {
					continue
				}
				fileRes := &resolve.Result{
					Refs:      pf.Refs,
					TypeRefs:  pf.TypeRefs,
					FileScope: pf.FileScope,
				}
				out, err := gen.Generate(pkgName, pf.File, fileRes, cr)
				header := fmt.Sprintf("\n// ---- %s ----\n", pf.Path)
				genBuf = append(genBuf, []byte(header)...)
				genBuf = append(genBuf, out...)
				if err != nil {
					genErrs++
					if firstErr == nil {
						firstErr = err
					}
				}
			}
		}
		r.GenBytes = genBuf
		r.GenError = firstErr
		summary := fmt.Sprintf("%d bytes Go (across %d packages)", len(genBuf), len(pkgPaths))
		if firstErr != nil {
			summary = fmt.Sprintf("%d bytes Go, %d file error(s); first: %v",
				len(genBuf), genErrs, firstErr)
		}
		emit(Stage{
			Name:     "gen",
			Duration: time.Since(t0),
			Output:   summary,
			Errors:   genErrs,
			Warnings: 0,
			Counts: map[string]int{
				"bytes":       len(genBuf),
				"file_errors": genErrs,
			},
		})
	}

	return r, nil
}

// RenderText writes a fixed-width table summarising the run to w.
// Always finishes with a totals row.
func (r Result) RenderText(w io.Writer) {
	fmt.Fprintf(w, "%-8s  %10s  %-50s  %6s  %6s\n",
		"PHASE", "TIME", "OUTPUT", "ERR", "WARN")
	fmt.Fprintln(w, strings.Repeat("-", 90))
	var total time.Duration
	var totalErr, totalWarn int
	for _, s := range r.Stages {
		fmt.Fprintf(w, "%-8s  %10s  %-50s  %6d  %6d\n",
			s.Name, formatDuration(s.Duration), truncate(s.Output, 50),
			s.Errors, s.Warnings)
		total += s.Duration
		totalErr += s.Errors
		totalWarn += s.Warnings
	}
	fmt.Fprintln(w, strings.Repeat("-", 90))
	fmt.Fprintf(w, "%-8s  %10s  %-50s  %6d  %6d\n",
		"TOTAL", formatDuration(total), "", totalErr, totalWarn)

	// Per-declaration timing — present only when Config.PerDecl was
	// set. Sorted by total time descending so the slow declarations
	// land at the top.
	if len(r.PerDecl) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "per-declaration timing (slowest first):")
		fmt.Fprintf(w, "  %-9s  %-8s  %-8s  %s\n", "TIME", "PHASE", "KIND", "NAME")
		fmt.Fprintln(w, "  "+strings.Repeat("-", 60))
		rows := append([]DeclTiming(nil), r.PerDecl...)
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Duration > rows[j].Duration
		})
		for _, dt := range rows {
			fmt.Fprintf(w, "  %9s  %-8s  %-8s  %s\n",
				formatDuration(dt.Duration), dt.Phase, dt.Kind, dt.Name)
		}
	}

	// Diagnostic-code histogram — useful when one phase reports lots
	// of findings and you want to know which code dominates.
	if len(r.AllDiags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "diagnostics by code:")
		hist := map[string]int{}
		for _, d := range r.AllDiags {
			c := d.Code
			if c == "" {
				c = "(no code)"
			}
			hist[c]++
		}
		keys := make([]string, 0, len(hist))
		for k := range hist {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s  %d\n", k, hist[k])
		}
	}
}

// RenderJSON writes a machine-readable summary. Schema is intentionally
// flat: top-level keys are "stages" (array) and "diagnostics_by_code"
// (object). Stages preserve their per-metric Counts map verbatim so
// downstream tooling can pick fields without re-parsing the human Output.
func (r Result) RenderJSON(w io.Writer) error {
	type stageJSON struct {
		Name       string         `json:"name"`
		DurationMS float64        `json:"duration_ms"`
		Output     string         `json:"output"`
		Errors     int            `json:"errors"`
		Warnings   int            `json:"warnings"`
		Counts     map[string]int `json:"counts,omitempty"`
	}
	stages := make([]stageJSON, len(r.Stages))
	for i, s := range r.Stages {
		stages[i] = stageJSON{
			Name:       s.Name,
			DurationMS: float64(s.Duration.Microseconds()) / 1000.0,
			Output:     s.Output,
			Errors:     s.Errors,
			Warnings:   s.Warnings,
			Counts:     s.Counts,
		}
	}
	hist := map[string]int{}
	for _, d := range r.AllDiags {
		c := d.Code
		if c == "" {
			c = "(no code)"
		}
		hist[c]++
	}
	out := struct {
		Stages            []stageJSON    `json:"stages"`
		DiagnosticsByCode map[string]int `json:"diagnostics_by_code"`
		PerDecl           []DeclTiming   `json:"per_decl,omitempty"`
	}{Stages: stages, DiagnosticsByCode: hist, PerDecl: r.PerDecl}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ---- helpers ----

func countSeverity(ds []*diag.Diagnostic, sev diag.Severity) int {
	n := 0
	for _, d := range ds {
		if d.Severity == sev {
			n++
		}
	}
	return n
}

// formatDuration prints durations with a unit appropriate to their
// magnitude — micros for sub-millisecond phases, millis otherwise.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000.0)
	case d < time.Second:
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return s[:n-1] + "…"
}

func traceTail(s Stage) string {
	tail := s.Output
	if s.Errors > 0 || s.Warnings > 0 {
		tail = fmt.Sprintf("%s  [err=%d warn=%d]", tail, s.Errors, s.Warnings)
	}
	return tail
}
