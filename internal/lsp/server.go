package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/parser"
	ostyquery "github.com/osty/osty/internal/query/osty"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/stdlib"
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

	// engine is the Salsa-style incremental query engine. For file-
	// mode analysis (scratch buffers / files without .osty siblings)
	// it backs the analyze path and persists cached parse / resolve
	// / check results across document edits. Package and workspace
	// modes retain their legacy eager paths pending a follow-up
	// migration.
	engine *ostyquery.Engine
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
	prelude := resolve.NewPrelude()
	reg := stdlib.LoadCached()
	return &Server{
		conn:    newConn(in, out),
		log:     log.New(logOut, "osty-lsp: ", log.LstdFlags),
		prelude: prelude,
		docs:    docStore{m: map[string]*document{}},
		exit:    make(chan struct{}),
		trace:   os.Getenv("OSTY_LSP_TRACE") != "",
		engine:  ostyquery.NewEngineForTest(prelude, reg),
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
	lines      *lineIndex
	file       *ast.File
	provenance *parser.Provenance
	canonical  []byte
	resolve    *resolve.Result
	check      *check.Result
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
	// semanticTokenData memoizes the encoded reply payload for
	// textDocument/semanticTokens/full. Editors often request this
	// repeatedly for an unchanged buffer, so caching avoids re-lexing
	// and re-encoding until the next analysis refresh replaces the
	// whole docAnalysis.
	semanticTokenOnce sync.Once
	semanticTokenData []uint32
}

// buildIdentIndex walks the file-level Refs/TypeRefs and inverts them
// into a byte-offset → Symbol lookup. TypeRef NamedTypes index under
// their head token's offset so a click on `auth.User` resolves to
// the right symbol for the head segment.
func buildIdentIndex(r *resolve.Result) map[int]*resolve.Symbol {
	if r == nil {
		return nil
	}
	out := make(map[int]*resolve.Symbol, len(r.RefIdents)+len(r.TypeRefIdents))
	for _, id := range r.RefIdents {
		out[id.PosV.Offset] = r.RefsByID[id.ID]
	}
	for _, nt := range r.TypeRefIdents {
		out[nt.PosV.Offset] = r.TypeRefsByID[nt.ID]
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
	return s.analyzeSingleFileViaEngine(uri, src)
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
		if a := s.analyzeWorkspaceViaEngine(dir, path, src); a != nil {
			return a
		}
		return s.analyzeWorkspace(dir, path, src)
	}
	if parent := filepath.Dir(dir); parent != dir && resolve.IsWorkspaceRoot(parent, dir) {
		if a := s.analyzeWorkspaceViaEngine(parent, path, src); a != nil {
			return a
		}
		return s.analyzeWorkspace(parent, path, src)
	}
	if dirHasOstySiblings(dir, path) {
		// Try the incremental engine path first; fall back to
		// legacy if the engine cannot handle the package.
		if a := s.analyzePackageViaEngine(dir, path, src); a != nil {
			return a
		}
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
	pkg, err := resolve.LoadPackageForNativeWithTransform(pkgDir, lspSourceOverrideTransform(path, src))
	if err != nil {
		return nil
	}
	if pkg == nil || len(pkg.Files) == 0 {
		return nil
	}
	lspMaterializeNativePackageFiles(pkg)
	pr := resolve.ResolvePackage(pkg, s.prelude)
	chk := check.Package(pkg, pr, lspCheckOpts(nil))
	lr := lint.Package(pkg, pr, chk)
	a := analysisForFileInPackage(pkg, pr, chk, lr, path, src)
	if a != nil {
		a.packages = []*resolve.Package{pkg}
	}
	return a
}

// analyzePackageViaEngine seeds sibling file contents into the Salsa
// engine and pulls the per-file analysis via the existing query chain.
// Unlike the legacy analyzePackage, this path gets incremental caching:
// unchanged siblings hit the Parse cache, and semantically-identical
// edits trigger early cutoff, sparing the checker and linter.
//
// Returns nil if the package cannot be analyzed through the engine
// (e.g. no source files found).
func (s *Server) analyzePackageViaEngine(pkgDir, path string, src []byte) *docAnalysis {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil
	}

	key := ostyquery.NormalizePath(path)
	dir := ostyquery.NormalizePath(pkgDir)

	// Discover .osty files in the package (excluding _test.osty),
	// matching LoadPackageForNativeWithTransform's behavior.
	var filePaths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		filePaths = append(filePaths, ostyquery.NormalizePath(filepath.Join(pkgDir, name)))
	}
	sort.Strings(filePaths)

	if len(filePaths) == 0 {
		return nil
	}

	// Build a lookup of open-document buffers keyed by normalized path
	// so unsaved edits in sibling files are honoured.
	openBuffers := s.openBuffersByPath()

	// Seed SourceText for every file in the package.
	for _, fp := range filePaths {
		if fp == key {
			// The file being edited: use the live buffer.
			s.engine.Inputs.SourceText.Set(s.engine.DB, fp, src)
			continue
		}
		if buf, ok := openBuffers[fp]; ok {
			// Sibling with unsaved edits: use its buffer.
			s.engine.Inputs.SourceText.Set(s.engine.DB, fp, buf)
			continue
		}
		// Not open in the editor: read from disk. Skip if already
		// seeded (common on second and subsequent edits of any file
		// in the same package).
		if s.engine.Inputs.SourceText.Has(s.engine.DB, fp) {
			continue
		}
		diskSrc, err := os.ReadFile(fp)
		if err != nil {
			return nil
		}
		s.engine.Inputs.SourceText.Set(s.engine.DB, fp, diskSrc)
	}

	// Seed PackageFiles so BuildPackage can assemble the resolve.Package.
	s.engine.Inputs.PackageFiles.Set(s.engine.DB, dir, filePaths)

	// Pull per-file results from the engine query chain.
	pr := s.engine.Queries.Parse.Get(s.engine.DB, key)
	rr := s.engine.Queries.ResolveFile.Get(s.engine.DB, key)
	chk := s.engine.Queries.CheckFile.Get(s.engine.DB, key)
	lr := s.engine.Queries.LintFile.Get(s.engine.DB, key)
	idx := s.engine.Queries.IdentIndex.Get(s.engine.DB, key)
	all := s.engine.Queries.FileDiagnostics.Get(s.engine.DB, key)

	// Retrieve the resolved package for cross-file handlers (references,
	// rename, workspaceSymbol).
	var packages []*resolve.Package
	rp := s.engine.Queries.ResolvePackage.Get(s.engine.DB, dir)
	if rp != nil && rp.Package() != nil {
		packages = []*resolve.Package{rp.Package()}
	}

	return &docAnalysis{
		lines:      newLineIndex(src),
		file:       pr.File,
		provenance: pr.Provenance,
		canonical:  pr.CanonicalSource,
		resolve:    rr,
		check:      chk,
		lint:       lr,
		diags:      all,
		identIndex: idx,
		packages:   packages,
	}
}

// openBuffersByPath returns a map from normalized file path to the
// current unsaved buffer for every open document. Used by
// analyzePackageViaEngine to honour unsaved edits in sibling files.
func (s *Server) openBuffersByPath() map[string][]byte {
	s.docs.mu.Lock()
	defer s.docs.mu.Unlock()
	out := make(map[string][]byte, len(s.docs.m))
	for uri, doc := range s.docs.m {
		path, ok := fileURIPath(uri)
		if !ok {
			continue
		}
		out[ostyquery.NormalizePath(path)] = doc.src
	}
	return out
}

// analyzeWorkspaceViaEngine seeds the entire workspace into the Salsa
// engine and pulls the per-file analysis via the workspace query chain
// (ResolveWorkspace → CheckWorkspace). Unlike the legacy
// analyzeWorkspace, this path gets incremental caching across edits:
// unchanged packages hit the Parse cache, and semantically-identical
// edits trigger early cutoff in ResolveWorkspace / CheckWorkspace,
// sparing the checker and linter re-runs.
//
// Returns nil if the workspace cannot be analyzed through the engine
// (e.g. no packages found, disk read failure).
func (s *Server) analyzeWorkspaceViaEngine(root, path string, src []byte) *docAnalysis {
	rootNorm := ostyquery.NormalizePath(root)
	key := ostyquery.NormalizePath(path)
	dir := ostyquery.PackageDirOf(path)
	openBuffers := s.openBuffersByPath()

	// Discover every package directory in the workspace.
	pkgPaths := resolve.WorkspacePackagePaths(root)
	if len(pkgPaths) == 0 {
		return nil
	}

	// Build the list of normalized package directories.
	pkgDirs := make([]string, 0, len(pkgPaths))
	for _, pp := range pkgPaths {
		var absDir string
		if pp == "" {
			absDir = root
		} else {
			absDir = filepath.Join(root, pp)
		}
		pkgDirs = append(pkgDirs, ostyquery.NormalizePath(absDir))
	}

	// For each package, discover .osty files, seed SourceText and
	// PackageFiles.
	for _, pkgDir := range pkgDirs {
		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			return nil
		}
		var filePaths []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
				continue
			}
			filePaths = append(filePaths, ostyquery.NormalizePath(filepath.Join(pkgDir, name)))
		}
		sort.Strings(filePaths)

		if len(filePaths) == 0 {
			continue
		}

		// Seed SourceText for every file in this package.
		for _, fp := range filePaths {
			if fp == key {
				s.engine.Inputs.SourceText.Set(s.engine.DB, fp, src)
				continue
			}
			if buf, ok := openBuffers[fp]; ok {
				s.engine.Inputs.SourceText.Set(s.engine.DB, fp, buf)
				continue
			}
			if s.engine.Inputs.SourceText.Has(s.engine.DB, fp) {
				continue
			}
			diskSrc, err := os.ReadFile(fp)
			if err != nil {
				return nil
			}
			s.engine.Inputs.SourceText.Set(s.engine.DB, fp, diskSrc)
		}

		s.engine.Inputs.PackageFiles.Set(s.engine.DB, pkgDir, filePaths)
	}

	// Seed WorkspaceMembers so ResolveWorkspace knows which packages
	// to include.
	s.engine.Inputs.WorkspaceMembers.Set(s.engine.DB, struct{}{}, pkgDirs)

	// Pull workspace-level results from the engine.
	rw := s.engine.Queries.ResolveWorkspace.Get(s.engine.DB, rootNorm)
	if rw == nil {
		return nil
	}
	cw := s.engine.Queries.CheckWorkspace.Get(s.engine.DB, rootNorm)

	// Find the owning package and file for the edited document.
	pkg := rw.PackageByDir(dir)
	if pkg == nil {
		return nil
	}
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

	// Slice out per-file results.
	rr := rw.FileResult(path)
	if rr == nil {
		rr = &resolve.Result{}
	}
	var chk *check.Result
	if cw != nil {
		chk = cw.ResultByDir(dir)
	}
	if chk == nil {
		chk = &check.Result{}
	}

	// Lint only the owning package — sibling packages aren't relevant
	// for the diagnostics we publish to the editor, and linting every
	// package on every keystroke is too expensive.
	pr := rw.ResultByDir(dir)
	lr := lint.Package(pkg, pr, chk)

	allDiags := collectDiagsForFile(pr, chk, lr, pf)

	return &docAnalysis{
		lines:      newLineIndex(src),
		file:       pf.File,
		provenance: pf.ParseProvenance,
		canonical:  pf.CanonicalSource,
		resolve:    rr,
		check:      chk,
		lint:       lr,
		diags:      allDiags,
		identIndex: buildIdentIndex(rr),
		packages:   rw.Packages(),
	}
}

// analyzeWorkspace loads the full workspace rooted at `root`, runs
// cross-package resolution + checking, and slices out the per-file
// view for the client's currently-open document.
func (s *Server) analyzeWorkspace(root, path string, src []byte) *docAnalysis {
	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		return nil
	}
	ws.SourceTransform = lspSourceOverrideTransform(path, src)
	// Seed the root package (if any) and every immediate subdir that
	// has `.osty` files; LoadPackage chases `use` edges from there.
	seedWorkspace(ws, root)
	lspMaterializeNativeWorkspace(ws)
	resolved := ws.ResolveAll()
	checks := check.Workspace(ws, resolved, lspCheckOpts(nil))
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
		_, _ = ws.LoadPackageNative(p)
	}
}

func lspSourceOverrideTransform(path string, src []byte) resolve.SourceTransform {
	if path == "" {
		return nil
	}
	return func(candidate string, original []byte) []byte {
		if candidate != path {
			return original
		}
		return append([]byte(nil), src...)
	}
}

func lspSourceOverrideMapTransform(overrides map[string][]byte) resolve.SourceTransform {
	if len(overrides) == 0 {
		return nil
	}
	return func(candidate string, original []byte) []byte {
		src, ok := overrides[candidate]
		if !ok {
			return original
		}
		return append([]byte(nil), src...)
	}
}

func lspMaterializeNativeWorkspace(ws *resolve.Workspace) {
	if ws == nil {
		return
	}
	for _, pkg := range ws.Packages {
		lspMaterializeNativePackageFiles(pkg)
	}
}

func lspMaterializeNativePackageFiles(pkg *resolve.Package) {
	if pkg == nil {
		return
	}
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if pf.File == nil && pf.Run != nil {
			pf.File = selfhost.LowerPublicFileFromRun(pf.Run)
		}
		if len(pf.CanonicalSource) == 0 && pf.File != nil {
			pf.CanonicalSource, pf.CanonicalMap = canonical.SourceWithMap(pf.Source, pf.File)
		}
	}
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
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
	}
	all := collectDiagsForFile(pr, chk, lr, pf)
	return &docAnalysis{
		lines:      newLineIndex(src),
		file:       pf.File,
		provenance: pf.ParseProvenance,
		canonical:  pf.CanonicalSource,
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

// analyzeSingleFileViaEngine uses the Salsa-style incremental query
// engine for file-mode analysis. The URI serves as the engine key
// (normalized for `file://` URIs, verbatim for scratch buffers) so
// successive edits of the same buffer share a cache lineage.
//
// When the new source hashes identical to the cached input the engine
// skips the entire Parse/Resolve/Check cascade; when the source
// differs but the resolver's semantic output doesn't (e.g. whitespace
// or comment-only edits), the early-cutoff path spares the checker
// and linter re-runs.
//
// Single-file mode's canonical entry since Phase 1c.4 retired the Go-
// legacy analyzeSingleFile: scratch buffers synthesize an
// `untitled:...` URI for cache identity.
func (s *Server) analyzeSingleFileViaEngine(uri string, src []byte) *docAnalysis {
	key := engineKeyForURI(uri)
	s.engine.Inputs.SourceText.Set(s.engine.DB, key, src)
	// PackageFiles must be seeded for BuildPackage to succeed. A
	// scratch buffer is treated as a one-file package keyed by its
	// directory (or by the URI itself when we have no path).
	dir := ostyquery.PackageDirOf(key)
	// Build the list deterministically from what the engine already
	// knows about — i.e., just this one file. Multi-file scratch
	// scenarios are rare in single-file mode.
	s.engine.Inputs.PackageFiles.Set(s.engine.DB, dir, []string{key})

	pr := s.engine.Queries.Parse.Get(s.engine.DB, key)
	rr := s.engine.Queries.ResolveFile.Get(s.engine.DB, key)
	chk := s.engine.Queries.CheckFile.Get(s.engine.DB, key)
	lr := s.engine.Queries.LintFile.Get(s.engine.DB, key)
	idx := s.engine.Queries.IdentIndex.Get(s.engine.DB, key)
	all := s.engine.Queries.FileDiagnostics.Get(s.engine.DB, key)
	return &docAnalysis{
		lines:      newLineIndex(src),
		file:       pr.File,
		provenance: pr.Provenance,
		canonical:  pr.CanonicalSource,
		resolve:    rr,
		check:      chk,
		lint:       lr,
		diags:      all,
		identIndex: idx,
	}
}

// engineKeyForURI converts an LSP URI to the engine's canonical key.
// Defers to ostyquery.FromURI for file:// URIs; returns the URI
// verbatim for other schemes so scratch-buffer identity is preserved
// across edits.
func engineKeyForURI(uri string) string {
	key, _ := ostyquery.FromURI(uri)
	return key
}

func lspCheckOpts(src []byte) check.Opts {
	reg := stdlib.LoadCached()
	return check.Opts{

		Source:        src,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
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
	s.refreshSiblingDiagnostics(uri)
	return doc
}

// refreshSiblingDiagnostics re-publishes diagnostics for other open
// documents that share the same package directory as changedURI.
//
// When the engine's package-mode path is active (i.e. PackageFiles has
// been seeded for the directory), editing one file can change
// diagnostics in siblings — for example, removing an exported function
// causes type errors in files that call it. This method pulls fresh
// FileDiagnostics from the engine for each sibling and pushes them to
// the editor without running a full re-analysis.
//
// No-op when the engine has no PackageFiles slot for the directory
// (single-file mode, scratch buffers, or legacy fallback).
func (s *Server) refreshSiblingDiagnostics(changedURI string) {
	path, ok := fileURIPath(changedURI)
	if !ok {
		return
	}
	dir := ostyquery.NormalizePath(filepath.Dir(path))

	// Only proceed when the engine has package-level data, meaning
	// analyzePackageViaEngine seeded the directory.
	if !s.engine.Inputs.PackageFiles.Has(s.engine.DB, dir) {
		return
	}

	// Snapshot the set of open sibling documents.
	s.docs.mu.Lock()
	type sibRef struct {
		uri string
		key string
		src []byte
	}
	var siblings []sibRef
	for uri, doc := range s.docs.m {
		if uri == changedURI {
			continue
		}
		sibPath, sibOK := fileURIPath(uri)
		if !sibOK {
			continue
		}
		sibDir := ostyquery.NormalizePath(filepath.Dir(sibPath))
		if sibDir != dir {
			continue
		}
		siblings = append(siblings, sibRef{
			uri: uri,
			key: ostyquery.NormalizePath(sibPath),
			src: doc.src,
		})
	}
	s.docs.mu.Unlock()

	if len(siblings) == 0 {
		return
	}

	// Pull fresh diagnostics for each sibling from the engine and
	// republish. The engine reuses cached Parse/Resolve/Check results
	// for unchanged files, so this is cheap when the edit doesn't
	// affect siblings semantically.
	for _, sib := range siblings {
		engineDiags := s.engine.Queries.FileDiagnostics.Get(s.engine.DB, sib.key)
		s.docs.mu.Lock()
		doc, ok := s.docs.m[sib.uri]
		if !ok || doc.analysis == nil {
			s.docs.mu.Unlock()
			continue
		}
		doc.analysis.diags = engineDiags
		s.docs.mu.Unlock()
		s.publishDiagnostics(doc)
	}
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
	ws.SourceTransform = lspSourceOverrideMapTransform(openBufs)
	ws.Packages = map[string]*resolve.Package{}
	seedWorkspace(ws, root)
	lspMaterializeNativeWorkspace(ws)
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
