package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// ServerName is advertised in Initialize and used as the `source`
// field of every published Diagnostic.
const ServerName = "osty-lsp"

// Server implements the read/dispatch/write loop of the language
// server. A Server is single-use: create it once per process with
// NewServer, call Run, and let it return when the client disconnects
// or sends the `exit` notification.
//
// The server is goroutine-safe: text-sync notifications mutate the
// document store under docs.mu, and response writes are serialized
// by the underlying conn.
type Server struct {
	conn *conn
	log  *log.Logger

	// prelude is the resolver's root scope. It's immutable once
	// populated so we build it once per server instead of allocating
	// ~40 builtin symbols on every keystroke's re-analysis.
	prelude *resolve.Scope

	// initialized flips to true after the client sends Initialize.
	// Requests that arrive before that point return errServerNotInitialized
	// per the LSP spec.
	mu          sync.Mutex
	initialized bool
	shutdown    bool
	// exit is closed when the client sends `exit`; Run returns on
	// the next loop iteration so callers can translate shutdown
	// state into a process exit code themselves.
	exit chan struct{}

	// trace is enabled via the OSTY_LSP_TRACE environment variable
	// (any non-empty value). When set, dispatch wraps each request
	// with a wall-clock timer and logs `lsp-trace: <method> <dur>`
	// per request. Useful for diagnosing slow editor interactions.
	trace bool

	docs docStore

	// wsIndex caches the result of a whole-workspace scan used by
	// workspace/symbol. Populated lazily on the first query and
	// invalidated whenever any document changes — subsequent
	// queries rebuild from disk.
	wsIndex workspaceIndex
}

// workspaceIndex stores every package loaded from a single workspace
// root. Built by scanning the filesystem for `.osty` files under
// that root; the resolver then links them via ResolvePackage so the
// returned Packages have complete Refs/TypeRefs maps.
type workspaceIndex struct {
	mu     sync.Mutex
	root   string
	pkgs   []*resolve.Package
	loaded bool
}

// invalidate clears the cached workspace index so the next query
// rebuilds it. Called from didOpen/didChange/didClose so the search
// reflects freshly-edited source.
func (w *workspaceIndex) invalidate() {
	w.mu.Lock()
	w.loaded = false
	w.pkgs = nil
	w.mu.Unlock()
}

// NewServer builds a Server wired to the given streams. Pass stderr
// (or any writer) as `logOut` to collect protocol-level trace logs;
// the server must never write logs to stdout because that's the
// LSP wire.
func NewServer(in io.Reader, out io.Writer, logOut io.Writer) *Server {
	if logOut == nil {
		logOut = io.Discard
	}
	return &Server{
		conn:    newConn(in, out),
		log:     log.New(logOut, "osty-lsp: ", log.LstdFlags),
		prelude: resolve.NewPrelude(),
		docs:    docStore{m: map[string]*document{}},
		exit:    make(chan struct{}),
		trace:   os.Getenv("OSTY_LSP_TRACE") != "",
	}
}

// NewStdioServer is a convenience that wires the server to os.Stdin /
// os.Stdout with logs on os.Stderr — the standard arrangement used by
// LSP clients that spawn the server as a child process.
func NewStdioServer() *Server {
	return NewServer(os.Stdin, os.Stdout, os.Stderr)
}

// Run blocks, processing messages until the client sends `exit` or
// stdin hits EOF. Returns nil on clean shutdown; returns an error
// only for unrecoverable transport failures. Callers that care
// about the exit code (0 for shutdown-then-exit, 1 for exit-without-
// shutdown) call ExitCode after Run returns.
func (s *Server) Run() error {
	for {
		select {
		case <-s.exit:
			return nil
		default:
		}
		body, err := s.conn.readMessage()
		if err == io.EOF {
			// Client hung up; in a well-behaved session we will
			// already have seen `exit` and closed the channel,
			// but an unexpected EOF is also a clean exit from
			// our side.
			return nil
		}
		if err != nil {
			s.log.Printf("read: %v", err)
			return err
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			s.log.Printf("parse: %v", err)
			_ = s.conn.writeError(nil, errParseError, err.Error())
			continue
		}
		s.dispatch(&req)
	}
}

// ExitCode returns the process-exit status the caller should use
// after Run returns: 0 when the client issued shutdown before exit,
// 1 otherwise (LSP spec §Exit Notification).
func (s *Server) ExitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return 0
	}
	return 1
}

// dispatch routes a decoded message to the right handler. Requests
// return a result (or error) over the wire; notifications are
// side-effectful only.
//
// We handle each message synchronously so that a didChange followed
// by a hover sees the freshly-analyzed AST without explicit
// synchronization. Long-running work (none today) can be parallelized
// later with per-method goroutines.
func (s *Server) dispatch(req *rpcRequest) {
	if s.trace {
		t0 := time.Now()
		defer func() {
			s.log.Printf("lsp-trace: %-40s %v", req.Method, time.Since(t0))
		}()
	}
	// Fast-path the lifecycle methods that can legally appear
	// before `initialized`.
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
		return
	case "exit":
		// Per spec, exit terminates the process regardless of
		// whether a prior shutdown was received. Signal Run to
		// stop; the caller decides whether to os.Exit.
		s.log.Println("received exit")
		s.mu.Lock()
		select {
		case <-s.exit:
			// Already closed.
		default:
			close(s.exit)
		}
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	initialized := s.initialized
	shutdown := s.shutdown
	s.mu.Unlock()
	if !initialized {
		if !req.isNotification() {
			_ = s.conn.writeError(req.ID, errServerNotInitialized,
				"server has not been initialized")
		}
		return
	}
	if shutdown {
		// After shutdown only `exit` is valid; everything else
		// is an invalid-request error (spec §Lifecycle).
		if !req.isNotification() {
			_ = s.conn.writeError(req.ID, errInvalidRequest,
				"server is shutting down")
		}
		return
	}

	switch req.Method {
	case "initialized":
		// One-shot acknowledgement; nothing to do.
	case "shutdown":
		s.mu.Lock()
		s.shutdown = true
		s.mu.Unlock()
		_ = s.conn.writeResponse(req.ID, json.RawMessage("null"))
	case "textDocument/didOpen":
		s.handleDidOpen(req)
	case "textDocument/didChange":
		s.handleDidChange(req)
	case "textDocument/didClose":
		s.handleDidClose(req)
	case "textDocument/hover":
		s.handleHover(req)
	case "textDocument/definition":
		s.handleDefinition(req)
	case "textDocument/formatting":
		s.handleFormatting(req)
	case "textDocument/documentSymbol":
		s.handleDocumentSymbol(req)
	case "textDocument/completion":
		s.handleCompletion(req)
	case "textDocument/references":
		s.handleReferences(req)
	case "textDocument/rename":
		s.handleRename(req)
	case "textDocument/signatureHelp":
		s.handleSignatureHelp(req)
	case "workspace/symbol":
		s.handleWorkspaceSymbol(req)
	case "textDocument/inlayHint":
		s.handleInlayHint(req)
	case "textDocument/semanticTokens/full":
		s.handleSemanticTokens(req)
	case "textDocument/codeAction":
		s.handleCodeAction(req)
	default:
		if !req.isNotification() {
			_ = s.conn.writeError(req.ID, errMethodNotFound,
				fmt.Sprintf("method not implemented: %s", req.Method))
		}
	}
}

// ---- Document store & analysis cache ----

// document holds the client's view of a file plus the analysis we've
// computed for it.
type document struct {
	uri      string
	version  int32
	src      []byte
	analysis *docAnalysis
	// lastDiags memoizes the LSP diagnostics most recently published
	// for this document. publishDiagnostics consults it to skip
	// notifications whose payload is byte-identical to the previous
	// one, sparing the editor a pointless re-render.
	lastDiags []LSPDiagnostic
}

// docAnalysis caches the full compile pipeline's output. Every time
// the source changes we recompute from scratch — the Osty front-end
// is fast enough that incremental reuse isn't worth the complexity
// at this stage.
type docAnalysis struct {
	lines   *lineIndex
	file    *ast.File
	resolve *resolve.Result
	check   *check.Result
	// lint is the lint pass output (may be nil if the pipeline
	// skipped linting, e.g. a future mode flag). Downstream handlers
	// use it for the source.organizeImports / source.fixAll actions
	// so they don't re-run lint.File on every request.
	lint  *lint.Result
	diags []*diag.Diagnostic
	// packages is every package loaded as part of this analysis.
	// Empty for scratch/single-file buffers; one entry for package
	// mode; one per package for workspace mode. Cross-file handlers
	// (references, rename, workspaceSymbol) walk these.
	packages []*resolve.Package
	// identIndex maps the byte offset of an Ident's name token to
	// the resolver Symbol it refers to. Built once per analysis so
	// semanticTokens, completion context, and other
	// offset-keyed lookups don't have to scan `resolve.Refs` in
	// O(n) per query.
	identIndex map[int]*resolve.Symbol
}

// buildIdentIndex walks the file-level Refs/TypeRefs and inverts them
// into a byte-offset → Symbol lookup. TypeRef NamedTypes index under
// their head token's offset so a click on `auth.User` resolves to
// the right symbol for the head segment.
func buildIdentIndex(r *resolve.Result) map[int]*resolve.Symbol {
	if r == nil {
		return nil
	}
	out := make(map[int]*resolve.Symbol, len(r.Refs)+len(r.TypeRefs))
	for id, sym := range r.Refs {
		out[id.PosV.Offset] = sym
	}
	for nt, sym := range r.TypeRefs {
		out[nt.PosV.Offset] = sym
	}
	return out
}

// docStore is the mutex-guarded map of URI → document.
type docStore struct {
	mu sync.Mutex
	m  map[string]*document
}

// put stores or overwrites the document for uri.
func (d *docStore) put(doc *document) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[doc.uri] = doc
}

// get returns the document for uri, or nil if the client never
// opened it (or closed it).
func (d *docStore) get(uri string) *document {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.m[uri]
}

// remove drops the document for uri. A no-op if absent.
func (d *docStore) remove(uri string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.m, uri)
}

// analyze runs the full compile pipeline on src and returns a fresh
// docAnalysis. The function is pure (no side effects beyond the
// allocated structures) so callers can run it off the main goroutine
// once we add concurrency.
//
// When `uri` names a file on disk with `.osty` siblings, we analyze
// the whole package (or whole workspace if the directory sits in a
// multi-package tree) so cross-file and cross-package references
// surface correct types. Off-disk buffers (unsaved scratch docs) fall
// back to single-file mode.
func (s *Server) analyze(uri string, src []byte) *docAnalysis {
	if s.trace {
		t0 := time.Now()
		defer func() {
			s.log.Printf("lsp-trace:   analyze(%s, %d bytes) %v",
				uri, len(src), time.Since(t0))
		}()
	}
	if path, ok := fileURIPath(uri); ok {
		if a := s.analyzePackageContaining(path, src); a != nil {
			return a
		}
	}
	return s.analyzeSingleFile(src)
}

// analyzePackageContaining walks the on-disk context of `path` and runs
// the full pipeline with whatever scope fits:
//
//   - Workspace mode: the file's parent directory has OTHER
//     subdirectories that contain `.osty` files → load the grandparent
//     as a workspace so cross-package references resolve.
//   - Package mode: the file has sibling `.osty` files but no workspace
//     structure → load just the containing directory as a package.
//   - Not applicable: return nil and let the caller fall back to
//     single-file analysis.
//
// When the file sits in a package/workspace but its on-disk content
// differs from the client's unsaved buffer, we substitute `src` for
// that file's source before running resolution. That way the LSP
// reflects the user's in-progress edits — not the saved copy.
func (s *Server) analyzePackageContaining(path string, src []byte) *docAnalysis {
	if !isExistingFile(path) {
		return nil
	}
	dir := filepath.Dir(path)
	// A file qualifies for workspace analysis when EITHER:
	//   - its own directory holds sibling packages (dir IS the root,
	//     this file is the root package's source); or
	//   - the parent directory does (dir is one of several sibling
	//     packages that share a workspace).
	// Package-only mode kicks in when none of the above applies but
	// dir has other `.osty` files sitting alongside this one.
	if resolve.IsWorkspaceRoot(dir, "") {
		return s.analyzeWorkspace(dir, path, src)
	}
	if parent := filepath.Dir(dir); parent != dir && resolve.IsWorkspaceRoot(parent, dir) {
		return s.analyzeWorkspace(parent, path, src)
	}
	if dirHasOstySiblings(dir, path) {
		return s.analyzePackage(dir, path, src)
	}
	return nil
}

func isExistingFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirHasOstySiblings reports whether `dir` contains at least one
// `.osty` file other than the given `selfPath`.
func dirHasOstySiblings(dir, selfPath string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".osty" {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if full == selfPath {
			continue
		}
		return true
	}
	return false
}

// analyzePackage runs resolve+check over every `.osty` file in pkgDir
// as one package. The file whose URI the client opened is substituted
// with `src` so unsaved edits are honored.
func (s *Server) analyzePackage(pkgDir, path string, src []byte) *docAnalysis {
	pkg, err := resolve.LoadPackage(pkgDir)
	if err != nil {
		return nil
	}
	if pkg == nil || len(pkg.Files) == 0 {
		return nil
	}
	substituteFileSource(pkg, path, src)
	pr := resolve.ResolvePackage(pkg, s.prelude)
	chk := check.Package(pkg, pr)
	lr := lint.Package(pkg, pr, chk)
	a := analysisForFileInPackage(pkg, pr, chk, lr, path, src)
	if a != nil {
		a.packages = []*resolve.Package{pkg}
	}
	return a
}

// analyzeWorkspace loads the full workspace rooted at `root`, runs
// cross-package resolution + checking, and slices out the per-file
// view for the client's currently-open document.
func (s *Server) analyzeWorkspace(root, path string, src []byte) *docAnalysis {
	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		return nil
	}
	// Seed the root package (if any) and every immediate subdir that
	// has `.osty` files; LoadPackage chases `use` edges from there.
	seedWorkspace(ws, root)
	// Swap in the client's buffer for the currently-open file. Only
	// one package owns it; stop once substituteFileSource reports a
	// hit so the remaining packages don't pay for a useless walk.
	for _, pkg := range ws.Packages {
		if substituteFileSource(pkg, path, src) {
			break
		}
	}
	resolved := ws.ResolveAll()
	checks := check.Workspace(ws, resolved)
	// Collect every loaded package for cross-file handlers.
	allPkgs := make([]*resolve.Package, 0, len(ws.Packages))
	for _, pkg := range ws.Packages {
		if pkg != nil {
			allPkgs = append(allPkgs, pkg)
		}
	}
	// Find the package + file entry this document belongs to.
	for pkgPath, pkg := range ws.Packages {
		for _, pf := range pkg.Files {
			if pf.Path == path {
				// Run lint over this package only — the owner of the
				// opened file. Sibling packages aren't relevant for
				// the diagnostics we publish to the editor, and
				// linting every package in the workspace on every
				// keystroke is too expensive.
				lr := lint.Package(pkg, resolved[pkgPath], checks[pkgPath])
				a := analysisForFileInPackage(
					pkg,
					resolved[pkgPath],
					checks[pkgPath],
					lr,
					path, src,
				)
				if a != nil {
					a.packages = allPkgs
				}
				return a
			}
		}
	}
	return nil
}

// seedWorkspace loads the root package (if .osty files sit directly
// in root) plus every immediate subdirectory that has .osty files.
// Delegates to the shared helper so CLI and LSP don't drift.
func seedWorkspace(ws *resolve.Workspace, root string) {
	for _, p := range resolve.WorkspacePackagePaths(root) {
		_, _ = ws.LoadPackage(p)
	}
}

// substituteFileSource overwrites the parsed state of the file at
// `path` with a fresh parse of `src`. Returns true when a matching
// file was found so callers can stop scanning additional packages.
// Used so the LSP analyzes the client's unsaved buffer even when
// on-disk content is stale.
func substituteFileSource(pkg *resolve.Package, path string, src []byte) bool {
	for _, pf := range pkg.Files {
		if pf.Path != path {
			continue
		}
		file, parseDiags := parser.ParseDiagnostics(src)
		pf.Source = src
		pf.File = file
		pf.ParseDiags = parseDiags
		return true
	}
	return false
}

// analysisForFileInPackage builds a docAnalysis that carries the
// per-file view (file, Refs, TypeRefs) plus the shared checker
// Result. Diagnostics filter to those attributable to this file.
func analysisForFileInPackage(
	pkg *resolve.Package,
	pr *resolve.PackageResult,
	chk *check.Result,
	lr *lint.Result,
	path string,
	src []byte,
) *docAnalysis {
	var pf *resolve.PackageFile
	for _, f := range pkg.Files {
		if f.Path == path {
			pf = f
			break
		}
	}
	if pf == nil {
		return nil
	}
	fileRes := &resolve.Result{
		Refs:      pf.Refs,
		TypeRefs:  pf.TypeRefs,
		FileScope: pf.FileScope,
	}
	all := collectDiagsForFile(pr, chk, lr, pf)
	return &docAnalysis{
		lines:      newLineIndex(src),
		file:       pf.File,
		resolve:    fileRes,
		check:      chk,
		lint:       lr,
		diags:      all,
		identIndex: buildIdentIndex(fileRes),
	}
}

// collectDiagsForFile picks out the parser, resolver, checker, and
// lint diagnostics that belong to one file. Parser diagnostics are
// already file-attributed via PackageFile.ParseDiags; the other three
// stages are filtered by byte-offset containment — positions from
// different files have disjoint offset ranges because each file was
// lexed against its own source buffer.
func collectDiagsForFile(
	pr *resolve.PackageResult,
	chk *check.Result,
	lr *lint.Result,
	pf *resolve.PackageFile,
) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	out = append(out, pf.ParseDiags...)
	if pr != nil {
		for _, d := range pr.Diags {
			if diagBelongsToFile(d, pf) {
				out = append(out, d)
			}
		}
	}
	if chk != nil {
		for _, d := range chk.Diags {
			if diagBelongsToFile(d, pf) {
				out = append(out, d)
			}
		}
	}
	if lr != nil {
		for _, d := range lr.Diags {
			if diagBelongsToFile(d, pf) {
				out = append(out, d)
			}
		}
	}
	return out
}

// diagBelongsToFile returns true when the diagnostic's primary
// position could plausibly have come from this file. Positions carry
// no file identity, so we filter on byte-range containment: a
// diagnostic from file B at offset N would fail this test for any
// file A with `len(A.Source) < N`. The heuristic misclassifies when
// multiple files in the same package share an overlapping offset
// range; fixing that requires threading a file identifier through
// every diag.Diagnostic, which is a resolver-side change.
func diagBelongsToFile(d *diag.Diagnostic, pf *resolve.PackageFile) bool {
	pos := d.PrimaryPos()
	if pos.Line == 0 {
		return false
	}
	return pos.Offset <= len(pf.Source)
}

// analyzeSingleFile is the original file-only analysis used for
// scratch buffers and files without .osty siblings.
func (s *Server) analyzeSingleFile(src []byte) *docAnalysis {
	file, parseDiags := parser.ParseDiagnostics(src)
	res := resolve.File(file, s.prelude)
	chk := check.File(file, res)
	lr := lint.File(file, res, chk)
	all := make([]*diag.Diagnostic, 0,
		len(parseDiags)+len(res.Diags)+len(chk.Diags)+len(lr.Diags))
	all = append(all, parseDiags...)
	all = append(all, res.Diags...)
	all = append(all, chk.Diags...)
	all = append(all, lr.Diags...)
	return &docAnalysis{
		lines:      newLineIndex(src),
		file:       file,
		resolve:    res,
		check:      chk,
		lint:       lr,
		diags:      all,
		identIndex: buildIdentIndex(res),
	}
}

// refreshDoc is the shared update path for didOpen and didChange:
// store the new buffer, run analysis, publish diagnostics. Also
// invalidates the workspace-symbol cache because any edit can add,
// remove, or rename a top-level decl visible to that query.
func (s *Server) refreshDoc(uri string, version int32, src []byte) *document {
	doc := &document{
		uri:      uri,
		version:  version,
		src:      src,
		analysis: s.analyze(uri, src),
	}
	s.docs.put(doc)
	s.wsIndex.invalidate()
	s.publishDiagnostics(doc)
	return doc
}

// ensureWorkspaceIndex makes sure wsIndex is populated with every
// package reachable from `root`. Uses the existing seedWorkspace /
// ResolveAll pipeline, substituting live buffers from `s.docs` so
// the cross-workspace symbol search sees the same un-saved state
// the editor does.
//
// Returns the cached package slice. Callers must not mutate it —
// invalidation is handled through wsIndex.invalidate().
func (s *Server) ensureWorkspaceIndex(root string) []*resolve.Package {
	s.wsIndex.mu.Lock()
	defer s.wsIndex.mu.Unlock()
	if s.wsIndex.loaded && s.wsIndex.root == root {
		return s.wsIndex.pkgs
	}
	s.wsIndex.root = root
	s.wsIndex.pkgs = nil
	s.wsIndex.loaded = false

	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		return nil
	}
	seedWorkspace(ws, root)
	// Substitute in every open document's current buffer so the
	// index reflects unsaved edits, not stale disk contents.
	s.docs.mu.Lock()
	openBufs := make(map[string][]byte, len(s.docs.m))
	for uri, doc := range s.docs.m {
		if path, ok := fileURIPath(uri); ok {
			openBufs[path] = doc.src
		}
	}
	s.docs.mu.Unlock()
	for _, pkg := range ws.Packages {
		for path, src := range openBufs {
			substituteFileSource(pkg, path, src)
		}
	}
	_ = ws.ResolveAll()
	pkgs := make([]*resolve.Package, 0, len(ws.Packages))
	for _, pkg := range ws.Packages {
		if pkg != nil {
			pkgs = append(pkgs, pkg)
		}
	}
	s.wsIndex.pkgs = pkgs
	s.wsIndex.loaded = true
	return pkgs
}

// workspaceRootForAny returns a plausible workspace root for the
// server: whichever open document sits in a workspace structure,
// picked deterministically by URI order. Returns "" when no open
// doc has a recognized on-disk layout.
func (s *Server) workspaceRootForAny() string {
	s.docs.mu.Lock()
	defer s.docs.mu.Unlock()
	var best string
	for uri := range s.docs.m {
		path, ok := fileURIPath(uri)
		if !ok {
			continue
		}
		if !isExistingFile(path) {
			continue
		}
		dir := filepath.Dir(path)
		parent := filepath.Dir(dir)
		var root string
		if resolve.IsWorkspaceRoot(dir, "") {
			root = dir
		} else if parent != dir && resolve.IsWorkspaceRoot(parent, dir) {
			root = parent
		} else if dirHasOstySiblings(dir, path) {
			root = dir
		}
		if root == "" {
			continue
		}
		if best == "" || root < best {
			best = root
		}
	}
	return best
}
