// Package legacyglobals implements the v0.6.x compatibility detector for
// v0.5 legacy global effect functions.
//
// When the front-end is invoked with `--legacy-globals`, this package
// walks each parsed file's AST, finds calls that match the v0.5 legacy
// global surface (`time.now()`, `random.next()`, `env.get(...)`,
// `fs.read(...)`, `os.exec(...)`, `net.dial(...)`, etc.), and emits
// `W0750` deprecation warnings pointing at every call site. The
// detector is intentionally textual (selector head names + leaf method
// name) so it stays useful for sources where the resolver cannot bind
// the legacy callee to a stdlib symbol — that is the whole point of
// `--legacy-globals`: today the legacy modules are not exposed as
// callable globals, so the resolver would only emit `E0500
// (UnknownName)` and the user has no way to enumerate migration work.
//
// Authority for the migration catalog is the spec table in
// `LANG_SPEC_v0.6/20-capabilities.md` §20.15 plus the broader call list
// in `BREAKING_v0.6.md` §3. The legacy module list also lives in
// `internal/stdlib/modules/capability.osty::legacyGlobalModules` so the
// stdlib registry's parity test pins drift between the two surfaces.
//
// Auto-desugar (rewrite to `time.systemClock.now()` / etc.) is a
// follow-up PR — `CHANGELOG_v0.6.md` line 50 stays at "partial" until
// then. The `[stability] = experimental` enforcement on packages that
// trigger any W0750 is also planned but not in this commit.
//
// v0.6.x only — v0.7 removes the flag and the desugar. After v0.7 each
// legacy global call becomes `E0701 (unknown name in this scope)`.
package legacyglobals

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
)

// Detect walks pkg's parsed files (after EnsureFile) and returns one
// W0750 deprecation diagnostic per legacy-global call site.
//
// Files that fail to materialize (Run-only packages where EnsureFile
// errors) are skipped silently — the parse-error path will already
// surface the failure.
func Detect(pkg *resolve.Package) []*diag.Diagnostic {
	if pkg == nil {
		return nil
	}
	var out []*diag.Diagnostic
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		out = append(out, detectFile(pf)...)
	}
	return out
}

// DetectFile is Detect for a single PackageFile.
func DetectFile(pf *resolve.PackageFile) []*diag.Diagnostic {
	if pf == nil {
		return nil
	}
	return detectFile(pf)
}

// DetectAST runs the detector over a parsed *ast.File directly. Used by
// the single-file front-end paths (`osty lint FILE`,
// `osty check FILE` without a containing package) where building a
// resolve.PackageFile would be unnecessary ceremony.
func DetectAST(file *ast.File, path string) []*diag.Diagnostic {
	if file == nil {
		return nil
	}
	visitor := newCallVisitor(path)
	for _, decl := range file.Decls {
		visitor.visitDecl(decl)
	}
	for _, stmt := range file.Stmts {
		visitor.visitStmt(stmt)
	}
	return visitor.diags
}

func detectFile(pf *resolve.PackageFile) []*diag.Diagnostic {
	file := materializeFile(pf)
	if file == nil {
		return nil
	}
	visitor := newCallVisitor(pf.Path)
	for _, decl := range file.Decls {
		visitor.visitDecl(decl)
	}
	for _, stmt := range file.Stmts {
		visitor.visitStmt(stmt)
	}
	return visitor.diags
}

// materializeFile pulls the public AST out of the PackageFile, calling
// EnsureFile only when the package was loaded via the native arena
// path. The detector tolerates a nil result — native-only packages
// without a public AST simply skip the W0750 pass.
func materializeFile(pf *resolve.PackageFile) *ast.File {
	if pf == nil {
		return nil
	}
	if pf.File != nil {
		return pf.File
	}
	if pf.Run == nil {
		return nil
	}
	return pf.EnsureFile()
}

// callVisitor accumulates W0750 diagnostics for one file.
type callVisitor struct {
	path  string
	diags []*diag.Diagnostic
}

func newCallVisitor(path string) *callVisitor {
	return &callVisitor{path: path}
}

func (v *callVisitor) visitDecl(decl ast.Decl) {
	if decl == nil {
		return
	}
	switch d := decl.(type) {
	case *ast.FnDecl:
		v.visitBlock(d.Body)
	case *ast.LetDecl:
		v.visitExpr(d.Value)
	case *ast.StructDecl:
		for _, m := range d.Methods {
			v.visitDecl(m)
		}
	case *ast.EnumDecl:
		for _, m := range d.Methods {
			v.visitDecl(m)
		}
	case *ast.InterfaceDecl:
		for _, m := range d.Methods {
			v.visitDecl(m)
		}
	}
}

func (v *callVisitor) visitStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		v.visitExpr(s.X)
	case *ast.LetStmt:
		v.visitExpr(s.Value)
	case *ast.AssignStmt:
		for _, t := range s.Targets {
			v.visitExpr(t)
		}
		v.visitExpr(s.Value)
	case *ast.ReturnStmt:
		v.visitExpr(s.Value)
	case *ast.ForStmt:
		v.visitExpr(s.Iter)
		v.visitBlock(s.Body)
	case *ast.DeferStmt:
		v.visitExpr(s.X)
	case *ast.ChanSendStmt:
		v.visitExpr(s.Channel)
		v.visitExpr(s.Value)
	case *ast.Block:
		v.visitBlock(s)
	case *ast.BreakStmt:
		v.visitExpr(s.Value)
	case *ast.ContinueStmt:
		// no expressions
	}
}

func (v *callVisitor) visitBlock(block *ast.Block) {
	if block == nil {
		return
	}
	for _, stmt := range block.Stmts {
		v.visitStmt(stmt)
	}
}

func (v *callVisitor) visitExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		v.checkCall(e)
		v.visitExpr(e.Fn)
		for _, arg := range e.Args {
			if arg != nil {
				v.visitExpr(arg.Value)
			}
		}
	case *ast.FieldExpr:
		v.visitExpr(e.X)
	case *ast.IndexExpr:
		v.visitExpr(e.X)
		v.visitExpr(e.Index)
	case *ast.UnaryExpr:
		v.visitExpr(e.X)
	case *ast.BinaryExpr:
		v.visitExpr(e.Left)
		v.visitExpr(e.Right)
	case *ast.QuestionExpr:
		v.visitExpr(e.X)
	case *ast.TurbofishExpr:
		v.visitExpr(e.Base)
	case *ast.RangeExpr:
		v.visitExpr(e.Start)
		v.visitExpr(e.Stop)
	case *ast.ParenExpr:
		v.visitExpr(e.X)
	case *ast.TupleExpr:
		for _, x := range e.Elems {
			v.visitExpr(x)
		}
	case *ast.ListExpr:
		for _, x := range e.Elems {
			v.visitExpr(x)
		}
	case *ast.MapExpr:
		for _, entry := range e.Entries {
			if entry != nil {
				v.visitExpr(entry.Key)
				v.visitExpr(entry.Value)
			}
		}
	case *ast.StructLit:
		for _, f := range e.Fields {
			if f != nil {
				v.visitExpr(f.Value)
			}
		}
		v.visitExpr(e.Spread)
	case *ast.IfExpr:
		v.visitExpr(e.Cond)
		v.visitBlock(e.Then)
		v.visitExpr(e.Else)
	case *ast.LoopExpr:
		v.visitBlock(e.Body)
	case *ast.MatchExpr:
		v.visitExpr(e.Scrutinee)
		for _, arm := range e.Arms {
			if arm != nil {
				v.visitExpr(arm.Guard)
				v.visitExpr(arm.Body)
			}
		}
	case *ast.ClosureExpr:
		v.visitExpr(e.Body)
	case *ast.Block:
		v.visitBlock(e)
	case *ast.StringLit:
		for _, part := range e.Parts {
			if !part.IsLit {
				v.visitExpr(part.Expr)
			}
		}
	}
}

// checkCall classifies a single CallExpr as a legacy global if the
// callee selector matches the migration catalog. Bare `Ident` callees
// (e.g., `println("...")`) are *not* flagged: the catalog entry for
// console output is the explicit `console.println` host call form, and
// flagging the bare prelude `println` would warn on every test file.
func (v *callVisitor) checkCall(call *ast.CallExpr) {
	if call == nil || call.Fn == nil {
		return
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return
	}
	head, ok := field.X.(*ast.Ident)
	if !ok {
		return
	}
	rule, ok := lookupRule(head.Name, field.Name)
	if !ok {
		return
	}
	v.diags = append(v.diags, buildDiagnostic(call, rule, v.path))
}

func buildDiagnostic(call *ast.CallExpr, rule legacyRule, path string) *diag.Diagnostic {
	span := diag.Span{Start: call.Pos(), End: call.End()}
	msg := fmt.Sprintf(
		"v0.5 legacy global `%s.%s` is deprecated — pass a `%s` capability parameter (LANG_SPEC_v0.6 §20.15)",
		rule.module, rule.method, rule.capability,
	)
	hint := fmt.Sprintf("migrate to `%s` (capability parameter); see CLAUDE.md 부록 C.1", rule.replacement)
	b := diag.New(diag.Warning, msg).
		Code(diag.CodeDeprecatedUse).
		Primary(span, "legacy global call site").
		Note("`--legacy-globals` is v0.6.x only — v0.7 removes the flag and the desugar; this call site becomes E0701 (unknown name)").
		Hint(hint)
	if path != "" {
		b = b.File(path)
	}
	return b.Build()
}
