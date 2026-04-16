package pkgmgr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/manifest"
)

// gitSource fetches a dependency from a git repository. We shell out
// to the `git` binary rather than pulling in a Go git library, which
// keeps the compile-time dependency surface small and relies on
// something every user already has installed (and whose behavior is
// stable across platforms for the operations we use: clone, fetch,
// rev-parse, checkout).
type gitSource struct {
	name   string
	url    string
	tag    string
	branch string
	rev    string
}

func (s *gitSource) Kind() SourceKind { return SourceGit }
func (s *gitSource) Name() string     { return s.name }

// URI renders a lockfile-stable identifier. Tag / branch / rev are
// encoded as a query string so the URI is parseable even by tools
// that don't understand our schema.
func (s *gitSource) URI() string {
	return GolegacyGitSourceURI(s.url, s.tag, s.branch, s.rev)
}

// Fetch clones the repository into the user cache and checks out the
// requested ref. Re-entrant: the clone is cached under
// <cache>/git/<sanitized-url>/, and subsequent fetches only run
// `git fetch` + `git rev-parse` + checkout.
func (s *gitSource) Fetch(ctx context.Context, env *Env) (*FetchedPackage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if env.Offline {
		return nil, fmt.Errorf("git dependency %s: offline mode forbids fetching %s", s.name, s.url)
	}
	cacheRoot := filepath.Join(env.CacheDir, "git", sanitizeURL(s.url))
	if err := ensureDir(cacheRoot); err != nil {
		return nil, err
	}
	if err := s.ensureClone(ctx, cacheRoot); err != nil {
		return nil, err
	}

	// Resolve the requested ref to a concrete commit so the lockfile
	// records a reproducible pin even when the user asked for a
	// branch / tag whose tip may move.
	ref := s.checkoutRef()
	commit, err := s.revParse(ctx, cacheRoot, ref)
	if err != nil {
		return nil, err
	}

	// Materialize a worktree-style snapshot at the requested commit
	// under <cache>/git-worktrees/<url>/<commit>/ so concurrent
	// resolves across projects share the same extracted tree.
	snapshot := filepath.Join(env.CacheDir, "git-worktrees", sanitizeURL(s.url), commit)
	if _, err := os.Stat(snapshot); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := ensureDir(filepath.Dir(snapshot)); err != nil {
			return nil, err
		}
		if err := s.snapshot(ctx, cacheRoot, commit, snapshot); err != nil {
			return nil, err
		}
	}
	depManifest, err := manifest.Read(filepath.Join(snapshot, manifest.ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("git dependency %s: missing or invalid osty.toml at %s: %w",
			s.name, commit[:minInt(12, len(commit))], err)
	}
	// We pin by commit hash regardless of whether the user asked for a
	// tag or branch. Tag/branch info is preserved in URI so a future
	// `osty update` can re-resolve the movable ref.
	return &FetchedPackage{
		LocalDir: snapshot,
		Manifest: depManifest,
		Version:  firstNonEmpty(s.tag, depManifest.Package.Version, commit[:minInt(12, len(commit))]),
		Checksum: HashPrefix + commit, // The commit hash IS the content hash for git.
	}, nil
}

// ensureClone clones the URL to dir if not already present; otherwise
// fetches the latest refs so tags/branches move forward.
func (s *gitSource) ensureClone(ctx context.Context, dir string) error {
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Already cloned: fetch the remote.
		return runGit(ctx, dir, "fetch", "--quiet", "--tags", "--prune", "origin")
	}
	// Fresh clone. `--bare` would be smaller but we need a worktree
	// to copy files out of; keep a plain clone at this layer.
	return runGit(ctx, filepath.Dir(dir), "clone", "--quiet", s.url, filepath.Base(dir))
}

// revParse turns a symbolic ref into a commit SHA.
func (s *gitSource) revParse(ctx context.Context, dir, ref string) (string, error) {
	out, err := runGitOutput(ctx, dir, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

// snapshot copies the files at commit into dst. We use `git archive`
// piped through tar so file modes and symlinks are preserved and we
// don't leave a `.git/` in the worktree.
func (s *gitSource) snapshot(ctx context.Context, repo, commit, dst string) error {
	if err := ensureDir(dst); err != nil {
		return err
	}
	// git -C <repo> archive --format=tar <commit> | tar -x -C <dst>
	archive := exec.CommandContext(ctx, "git", "-C", repo, "archive", "--format=tar", commit)
	extract := exec.CommandContext(ctx, "tar", "-x", "-C", dst)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	extract.Stdin = pipe
	archive.Stderr = os.Stderr
	extract.Stderr = os.Stderr
	if err := archive.Start(); err != nil {
		return err
	}
	if err := extract.Start(); err != nil {
		_ = archive.Wait()
		return err
	}
	if err := archive.Wait(); err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	if err := extract.Wait(); err != nil {
		return fmt.Errorf("tar extract: %w", err)
	}
	return nil
}

// checkoutRef picks the ref the user asked for, in precedence
// order rev > tag > branch > HEAD. The fetcher resolves this into a
// commit hash via rev-parse.
func (s *gitSource) checkoutRef() string {
	return GolegacyGitCheckoutRef(s.url, s.tag, s.branch, s.rev)
}

// sanitizeURL turns a URL into a filesystem-safe directory name.
// Not reversible; we only need collision resistance plus some
// human-readability, so a simple replacement of non-alnum chars with
// `_` is enough.
func sanitizeURL(u string) string {
	return GolegacySanitizeURL(u)
}

// runGit executes `git <args>` in dir, inheriting stderr so users see
// git's own error messages. stdout is discarded because the
// operations we use here (fetch, clone) don't emit structured output.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runGitOutput captures stdout so callers can consume it. stderr is
// inherited so the user sees the same messages they'd see in a
// terminal.
func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
