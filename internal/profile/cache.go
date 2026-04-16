package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CacheDirName is the relative directory under the project root that
// `osty build` writes per-profile artifacts and fingerprints into.
// Each subdirectory is named after the effective (profile, target)
// pair so concurrent cross-builds can coexist.
const CacheDirName = ".osty/cache"

// OutDirName is the backend artifact output root. Laid out as
// .osty/out/<profile>[-<triple>]/ so `osty build --release` doesn't
// clobber debug artifacts.
const OutDirName = ".osty/out"

const defaultCacheBackend = "llvm"

// ArtifactKey formats the (profile, triple) tuple used to scope cache
// entries and output directories. The triple portion is elided when
// empty so host builds land at the familiar `out/<profile>/` path.
func ArtifactKey(profile, triple string) string {
	if triple == "" {
		return profile
	}
	return profile + "-" + triple
}

// OutputDir returns <root>/.osty/out/<key>/ for the given profile +
// triple. The directory is not created; callers that need it to
// exist call os.MkdirAll on the result.
func OutputDir(root, profile, triple string) string {
	return filepath.Join(root, OutDirName, ArtifactKey(profile, triple))
}

// BackendCachePath returns the per-(profile, triple, backend) fingerprint JSON
// file. Backend names are directory/file-safe stable identifiers such as "go"
// and "llvm".
func BackendCachePath(root, profile, triple, backend string) string {
	if backend == "" {
		backend = defaultCacheBackend
	}
	return filepath.Join(root, CacheDirName, ArtifactKey(profile, triple), backend+".json")
}

// CachePath returns the default native backend fingerprint JSON file.
func CachePath(root, profile, triple string) string {
	return BackendCachePath(root, profile, triple, defaultCacheBackend)
}

// LegacyCachePath returns the pre-backend-aware fingerprint JSON file. New
// builds do not write this path; it remains here so migration code and layout
// tests can identify stale records deliberately.
func LegacyCachePath(root, profile, triple string) string {
	return filepath.Join(root, CacheDirName, ArtifactKey(profile, triple)+".json")
}

// Fingerprint is the on-disk record describing one build. Sources
// maps the project-relative .osty file path to its sha256 content
// hash; ToolVersion records the toolchain stamp so a binary upgrade
// invalidates the cache even when source bytes haven't moved.
//
// The schema is forward-compatible by ignoring unknown JSON fields
// — future tool versions can extend the record without migrating.
type Fingerprint struct {
	Backend     string            `json:"backend,omitempty"`
	Emit        string            `json:"emit,omitempty"`
	Profile     string            `json:"profile"`
	Target      string            `json:"target,omitempty"`
	ToolVersion string            `json:"tool_version"`
	Features    []string          `json:"features,omitempty"`
	Sources     map[string]string `json:"sources"`
	Artifacts   map[string]string `json:"artifacts,omitempty"`
	BuiltAt     time.Time         `json:"built_at"`
}

// Equal reports whether two fingerprints represent an identical build
// (every source hash matches + same tool version + same feature set).
// The built_at stamp is ignored because it's an observation, not an
// input.
func (f *Fingerprint) Equal(other *Fingerprint) bool {
	if f == nil || other == nil {
		return false
	}
	if f.ToolVersion != other.ToolVersion {
		return false
	}
	if f.Backend != other.Backend {
		return false
	}
	if f.Emit != other.Emit {
		return false
	}
	if !stringSliceEq(f.Features, other.Features) {
		return false
	}
	if len(f.Sources) != len(other.Sources) {
		return false
	}
	for k, v := range f.Sources {
		if other.Sources[k] != v {
			return false
		}
	}
	return true
}

// HashFile computes the hex-encoded sha256 of the file at path. An
// I/O error aborts — callers use this on inputs they already expect
// to exist (parser has opened them).
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// HashSources walks every file under root whose name matches the
// supplied predicate (callers pass an .osty filter) and returns a
// path→hash map keyed by path relative to root. Symlinks and
// hidden directories (leading ".") are skipped so the cache
// directory itself doesn't feed back into the fingerprint.
func HashSources(root string, want func(name string) bool) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !want(name) {
			return nil
		}
		h, err := HashFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NewFingerprint is a convenience that builds a Fingerprint from a
// computed sources map plus the effective resolved config. toolVer
// is the stamp the caller got from build info (a git sha in real
// builds, "dev" in tests).
func NewFingerprint(sources map[string]string, r *Resolved, toolVer string) *Fingerprint {
	triple := ""
	if r != nil && r.Target != nil {
		triple = r.Target.Triple
	}
	profileName := ""
	var feats []string
	if r != nil && r.Profile != nil {
		profileName = r.Profile.Name
	}
	if r != nil {
		feats = append([]string(nil), r.Features...)
	}
	return &Fingerprint{
		Profile:     profileName,
		Target:      triple,
		ToolVersion: toolVer,
		Features:    feats,
		Sources:     sources,
		BuiltAt:     time.Now().UTC(),
	}
}

// NewBackendFingerprint builds a Fingerprint stamped with the backend and emit
// mode that produced its artifacts.
func NewBackendFingerprint(sources map[string]string, r *Resolved, toolVer, backend, emit string, artifacts map[string]string) *Fingerprint {
	fp := NewFingerprint(sources, r, toolVer)
	fp.Backend = backend
	fp.Emit = emit
	if len(artifacts) > 0 {
		fp.Artifacts = map[string]string{}
		for k, v := range artifacts {
			fp.Artifacts[k] = v
		}
	}
	return fp
}

// Write serializes f to the cache path for (profile, triple, backend) under
// root. Parent directories are created as needed. The write is
// atomic: content goes to a sibling `.tmp` file first, then Renamed
// into place — a crash during `osty build` never leaves a truncated
// JSON record behind.
func (f *Fingerprint) Write(root string) error {
	if f == nil {
		return fmt.Errorf("nil fingerprint")
	}
	if f.Backend == "" {
		f.Backend = defaultCacheBackend
	}
	path := BackendCachePath(root, f.Profile, f.Target, f.Backend)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadFingerprint loads the default native backend fingerprint for
// (profile, triple) from root.
func ReadFingerprint(root, profile, triple string) (*Fingerprint, error) {
	return ReadFingerprintForBackend(root, profile, triple, defaultCacheBackend)
}

// ReadFingerprintForBackend loads the cached fingerprint for (profile, triple,
// backend) from root. Legacy <key>.json records are intentionally ignored so a
// migrated build cannot accidentally skip work based on a Go-only cache path.
//
// Returns (nil, nil) when no such cache exists — a missing cache is not an
// error, just a cold build.
func ReadFingerprintForBackend(root, profile, triple, backend string) (*Fingerprint, error) {
	if backend == "" {
		backend = defaultCacheBackend
	}
	path := BackendCachePath(root, profile, triple, backend)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	f, err := parseFingerprint(path, data)
	if err != nil {
		return nil, err
	}
	if f.Backend == "" {
		f.Backend = backend
	}
	return f, nil
}

// ReadLegacyFingerprint loads the old <key>.json cache path. Build does not
// use this for freshness decisions; it exists for cache migration diagnostics.
func ReadLegacyFingerprint(root, profile, triple string) (*Fingerprint, error) {
	path := LegacyCachePath(root, profile, triple)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseFingerprint(path, data)
}

// parseFingerprint decodes one cache JSON record and annotates corrupt-cache
// errors with the path that failed.
func parseFingerprint(path string, data []byte) (*Fingerprint, error) {
	var f Fingerprint
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("corrupt cache %s: %w", path, err)
	}
	return &f, nil
}

// CacheEntry is a compact summary used by `osty cache ls`.
type CacheEntry struct {
	Backend     string
	Emit        string
	Profile     string
	Target      string
	ToolVersion string
	Sources     int
	Size        int64
	BuiltAt     time.Time
}

// ListCache walks the backend-aware cache directory under root and returns one
// entry per valid fingerprint JSON. Legacy <key>.json files are ignored: they
// may be cleaned, but they should not participate in migrated freshness checks.
// Corrupt files are silently skipped so a single bad record doesn't break the
// listing. Entries are sorted by (profile, target, backend) for reproducible
// output.
func ListCache(root string) ([]CacheEntry, error) {
	dir := filepath.Join(root, CacheDirName)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CacheEntry
	err := filepath.WalkDir(dir, func(full string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			return nil
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 2 {
			return nil
		}
		backend := strings.TrimSuffix(parts[1], ".json")
		if backend == "" {
			return nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil
		}
		f, err := parseFingerprint(full, data)
		if err != nil {
			return nil
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		out = append(out, CacheEntry{
			Backend:     firstNonEmpty(f.Backend, backend),
			Emit:        f.Emit,
			Profile:     f.Profile,
			Target:      f.Target,
			ToolVersion: f.ToolVersion,
			Sources:     len(f.Sources),
			Size:        size,
			BuiltAt:     f.BuiltAt,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Profile != out[j].Profile {
			return out[i].Profile < out[j].Profile
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Backend < out[j].Backend
	})
	return out, nil
}

// CleanCache removes every fingerprint JSON under root's cache dir
// plus the matching output trees. Returns the number of bytes
// reclaimed (summed file sizes) so `osty cache clean` can report
// actionable numbers. Missing directories are treated as "nothing
// to clean", not errors.
func CleanCache(root string) (int64, error) {
	var total int64
	cacheDir := filepath.Join(root, CacheDirName)
	if size, err := sumAndRemove(cacheDir); err == nil {
		total += size
	} else if !os.IsNotExist(err) {
		return total, err
	}
	outDir := filepath.Join(root, OutDirName)
	if size, err := sumAndRemove(outDir); err == nil {
		total += size
	} else if !os.IsNotExist(err) {
		return total, err
	}
	return total, nil
}

// sumAndRemove totals the size of every file beneath path and then
// removes the tree. Used by CleanCache — factored out so it can be
// called on both the cache + output dirs without duplicating the
// walk.
func sumAndRemove(path string) (int64, error) {
	var total int64
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		total = info.Size()
		return total, os.Remove(path)
	}
	err = filepath.WalkDir(path, func(_ string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		fi, ferr := d.Info()
		if ferr == nil {
			total += fi.Size()
		}
		return nil
	})
	if err != nil {
		return total, err
	}
	return total, os.RemoveAll(path)
}

func stringSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
