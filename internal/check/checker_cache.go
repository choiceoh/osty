package check

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/osty/osty/internal/selfhost/api"
)

func EmbeddedCheckerFingerprint(repoRoot string) string {
	h := sha256.New()
	fmt.Fprintf(h, "go=%s\nos=%s\narch=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	files := []string{
		"internal/selfhost/generated.go",
		"internal/selfhost/astbridge/generated.go",
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			return ""
		}
		fmt.Fprintf(h, "%s=%d\n", rel, len(data))
		h.Write(data)
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:12])
}

// UseCachedDefaultNativeChecker wraps whichever checker `defaultNativeChecker`
// would return (embedded by default, or an explicit subprocess override) in
// the on-disk
// cache layer. First-time builds pay the full check cost; second-and-later
// builds with unchanged package inputs short-circuit to a JSON read
// (~microseconds) instead of re-running the checker. Unchanged-package
// granularity gives multi-second wins on incremental `osty check` / `osty
// build` iterations where only one or two packages change per edit.
//
// Calling this from cmd/osty build.go / run.go / query.go activates the
// cache for the lifetime of the process; fingerprint validity is the
// caller's responsibility (typically a digest of internal/selfhost/
// generated.go so a checker-binary update invalidates every entry).
func UseCachedDefaultNativeChecker(cacheDir, validity string) {
	backing, note := defaultNativeChecker()
	if backing == nil {
		// Preserve the error note so callers see the same diagnostic as
		// the uncached path when an explicitly configured checker can't
		// start.
		nativeCheckerFactory = func() (nativeChecker, string) {
			return nil, note
		}
		return
	}
	checker := cachedNativeChecker{
		backing:  backing,
		dir:      filepath.Join(cacheDir, validity),
		validity: validity,
	}
	nativeCheckerFactory = func() (nativeChecker, string) {
		return checker, note
	}
}

// cachedNativeChecker wraps any nativeChecker (embedded, managed exec,
// or future backends) with an on-disk JSON cache keyed by the
// fingerprint of the input. First-time inputs pay the full cost of
// `backing.CheckSourceStructured` / `backing.CheckPackageStructured`;
// subsequent identical inputs hit the cache and return in
// microseconds, which turns `osty check` / `osty build` on a clean
// incremental edit from multi-second into near-zero.
//
// The on-disk entry is validity-scoped: callers pass a version tag
// (typically a hex digest of internal/selfhost/generated.go) so a
// checker-binary update transparently invalidates every entry without
// explicit migration.
type cachedNativeChecker struct {
	backing  nativeChecker
	dir      string
	validity string
}

func (c cachedNativeChecker) CheckSourceStructured(src []byte) (api.CheckResult, error) {
	key := cachedEmbeddedKey("src", src)
	if res, ok := c.read(key); ok {
		return res, nil
	}
	res, err := c.backing.CheckSourceStructured(src)
	if err == nil {
		c.write(key, res)
	}
	return res, err
}

func (c cachedNativeChecker) CheckPackageStructured(input api.PackageCheckInput) (api.CheckResult, error) {
	// Key on the raw source + a stable subset of the import surface.
	// Hashing the full PackageCheckInput through json.Marshal would
	// traverse the entire parsed AST — multi-second for the regen
	// bundle, and unstable when pointer-graph ordering differs across
	// runs (map iteration, slice identity) — which both defeats the
	// cache and makes the hit path slower than the call it replaces.
	key := cachedEmbeddedKey("pkg", packageCheckFingerprint(input))
	if res, ok := c.read(key); ok {
		return res, nil
	}
	// The backing checker may or may not implement the package path;
	// embedded does, the subprocess exec does, and anything else
	// falls through to the single-source entry. We pick the package
	// path explicitly so the subprocess round-trip isn't bypassed.
	var (
		res api.CheckResult
		err error
	)
	if pc, ok := c.backing.(nativePackageChecker); ok {
		res, err = pc.CheckPackageStructured(input)
	} else {
		// Backing doesn't implement the package path; fall back to
		// concatenating file sources so the single-source entry sees
		// a coherent snapshot. This mirrors how the managed checker
		// exec splices the package together pre-subprocess.
		var buf bytes.Buffer
		for _, f := range input.Files {
			buf.Write(f.Source)
			if len(f.Source) > 0 && f.Source[len(f.Source)-1] != '\n' {
				buf.WriteByte('\n')
			}
		}
		res, err = c.backing.CheckSourceStructured(buf.Bytes())
	}
	if err == nil {
		c.write(key, res)
	}
	return res, err
}

func packageCheckFingerprint(input api.PackageCheckInput) []byte {
	h := sha256.New()
	for _, f := range input.Files {
		fmt.Fprintf(h, "file=%s base=%d len=%d\n", f.Name, f.Base, len(f.Source))
		h.Write(f.Source)
		h.Write([]byte{'\n'})
	}
	for _, imp := range input.Imports {
		fmt.Fprintf(h, "import=%s fns=%d types=%d variants=%d fields=%d aliases=%d iface=%d\n",
			imp.Alias,
			len(imp.Functions),
			len(imp.TypeDecls),
			len(imp.Variants),
			len(imp.Fields),
			len(imp.Aliases),
			len(imp.InterfaceExts),
		)
		for _, fn := range imp.Functions {
			fmt.Fprintf(h, "  fn=%s owner=%s recv=%s recvRepr=%s ret=%s retRepr=%s params=%d reprParams=%d\n",
				fn.Name, fn.Owner, fn.ReceiverType, checkFingerprintTypeRepr(fn.ReceiverTypeRepr), fn.ReturnType, checkFingerprintTypeRepr(fn.ReturnTypeRepr), len(fn.ParamTypes), len(fn.ParamTypeReprs))
			for i, pt := range fn.ParamTypes {
				fmt.Fprintf(h, "    p%d=%s\n", i, pt)
			}
			for i := range fn.ParamTypeReprs {
				fmt.Fprintf(h, "    pr%d=%s\n", i, fn.ParamTypeReprs[i].String())
			}
			for _, bound := range fn.GenericBounds {
				fmt.Fprintf(h, "    bound=%s:%s:%s\n", bound.TyParam, bound.InterfaceType, checkFingerprintTypeRepr(bound.InterfaceTypeRepr))
			}
		}
		for _, td := range imp.TypeDecls {
			fmt.Fprintf(h, "  type=%s kind=%s generics=%d\n", td.Name, td.Kind, len(td.Generics))
			for _, bound := range td.GenericBounds {
				fmt.Fprintf(h, "    typeBound=%s:%s:%s\n", bound.TyParam, bound.InterfaceType, checkFingerprintTypeRepr(bound.InterfaceTypeRepr))
			}
		}
		for _, field := range imp.Fields {
			fmt.Fprintf(h, "  field=%s/%s type=%s typeRepr=%s exported=%t default=%t\n",
				field.Owner, field.Name, field.TypeName, checkFingerprintTypeRepr(field.Type), field.Exported, field.HasDefault)
		}
		for _, v := range imp.Variants {
			fmt.Fprintf(h, "  variant=%s/%s fields=%d reprFields=%d\n", v.Owner, v.Name, len(v.FieldTypes), len(v.FieldTypeReprs))
			for i := range v.FieldTypeReprs {
				fmt.Fprintf(h, "    vr%d=%s\n", i, v.FieldTypeReprs[i].String())
			}
		}
		for _, alias := range imp.Aliases {
			fmt.Fprintf(h, "  alias=%s target=%s targetRepr=%s generics=%d\n",
				alias.Name, alias.Target, checkFingerprintTypeRepr(alias.TargetRepr), len(alias.Generics))
		}
		for _, ext := range imp.InterfaceExts {
			fmt.Fprintf(h, "  ifaceExt=%s iface=%s ifaceRepr=%s\n",
				ext.Owner, ext.InterfaceType, checkFingerprintTypeRepr(ext.InterfaceTypeRepr))
		}
	}
	sum := h.Sum(nil)
	return sum[:]
}

func checkFingerprintTypeRepr(repr *api.TypeRepr) string {
	if repr == nil {
		return ""
	}
	return repr.String()
}

func (c cachedNativeChecker) read(key string) (api.CheckResult, bool) {
	data, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return api.CheckResult{}, false
	}
	var res api.CheckResult
	if err := json.Unmarshal(data, &res); err != nil {
		return api.CheckResult{}, false
	}
	res.EnsureStableIDs()
	return res, true
}

func (c cachedNativeChecker) write(key string, res api.CheckResult) {
	data, err := json.Marshal(res)
	if err != nil {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	// Atomic swap: write to a sibling file then rename. Avoids a racing
	// reader seeing a half-written entry if two regen pipelines overlap.
	tmp, err := os.CreateTemp(c.dir, key+"-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, filepath.Join(c.dir, key+".json")); err != nil {
		os.Remove(tmpPath)
	}
}

func cachedEmbeddedKey(tag string, data []byte) string {
	sum := sha256.Sum256(data)
	return tag + "-" + hex.EncodeToString(sum[:])
}

func defaultNativeChecker() (nativeChecker, string) {
	path := strings.TrimSpace(os.Getenv(nativeCheckerEnv))
	if path != "" {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return nil, fmt.Sprintf("%s=%q override was not found; unset it to use the default checker path", nativeCheckerEnv, path)
		}
		return nativeCheckerExec{path: resolved}, ""
	}
	return embeddedNativeChecker{}, ""
}
