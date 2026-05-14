package profile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/runner"
)

// Built-in profile names. Any additional profile declared in the
// manifest under `[profile.<name>]` may inherit from one of these via
// `inherits = "<name>"`.
const (
	NameDebug   = "debug"
	NameRelease = "release"
	NameProfile = "profile"
	NameTest    = "test"
)

// Profile is the set of knobs the compiler + linker consume when
// producing an artifact. Field semantics:
//
//   - OptLevel is the optimization level carried through profile resolution;
//     legacy bootstrap builds map it to Go compiler flags.
//   - Debug toggles retention of DWARF symbols. `-ldflags=-w -s`
//     strips them when false.
//   - Overflow toggles integer-overflow runtime checks where the selected
//     backend supports them.
//   - Inlining/LTO are per-call-site hints carried through the manifest parser
//     so users can pin them ahead of time.
//   - GoFlags is a legacy bootstrap-only list of raw Go compiler flags.
//   - Env is process-env key/value overrides threaded into backend toolchain
//     invocations.
//   - Inherits names another profile whose fields fill in unset
//     values at merge time. Limited to one level of indirection so
//     cycles are structurally impossible.
type Profile struct {
	Name     string
	OptLevel int
	Debug    bool
	Strip    bool
	Overflow bool
	Inlining bool
	LTO      bool
	GoFlags  []string
	Env      map[string]string
	Inherits string

	// UserDefined is true when the profile came from the manifest
	// (as opposed to the built-in defaults). Used by `osty profiles`
	// to tag custom entries in its listing.
	UserDefined bool
}

// Clone returns a deep copy of p so callers can mutate without
// aliasing the defaults.
func (p *Profile) Clone() *Profile {
	if p == nil {
		return nil
	}
	cp := *p
	cp.GoFlags = append([]string(nil), p.GoFlags...)
	if p.Env != nil {
		cp.Env = make(map[string]string, len(p.Env))
		for k, v := range p.Env {
			cp.Env[k] = v
		}
	}
	return &cp
}

// Target describes a (GOOS, GOARCH) pair plus an optional CGO toggle.
// The Triple name is the user-facing handle — the convention is the
// `<arch>-<os>` form (e.g. `amd64-linux`, `arm64-darwin`) so it
// composes nicely with cache paths. Either/both of OS/Arch may be
// empty, in which case the host value is used at resolve time.
type Target struct {
	Triple string
	OS     string
	Arch   string
	CGO    *bool // nil = leave CGO_ENABLED untouched
	Env    map[string]string
	Link   []string

	// UserDefined is set for manifest-declared targets.
	UserDefined bool
}

// Clone produces a deep copy of t.
func (t *Target) Clone() *Target {
	if t == nil {
		return nil
	}
	cp := *t
	if t.CGO != nil {
		b := *t.CGO
		cp.CGO = &b
	}
	if t.Env != nil {
		cp.Env = make(map[string]string, len(t.Env))
		for k, v := range t.Env {
			cp.Env[k] = v
		}
	}
	if t.Link != nil {
		cp.Link = append([]string(nil), t.Link...)
	}
	return &cp
}

// ParseTriple splits "<arch>-<os>" into (arch, os). Returns an error
// if the triple isn't in the expected form. The toolchain does not
// try to validate against Go's internal GOOS/GOARCH table — unknown
// combinations are surfaced by `go build` itself.
// ParseTriple delegates to toolchain/profile_pragma.osty. The host
// wrapper preserves the legacy (arch, os, err) shape by lifting the
// policy's `Ok` flag into a formatted error message.
func ParseTriple(triple string) (arch, os string, err error) {
	r := runner.ParseTriple(triple)
	if !r.Ok {
		return "", "", fmt.Errorf("invalid target triple %q (want <arch>-<os>)", triple)
	}
	return r.Arch, r.OS, nil
}

// Config is the merged view of built-in defaults + manifest-declared
// profile/target/feature tables. Callers acquire one via
// BuildConfig(m) and then look profiles/targets up by name.
type Config struct {
	Profiles map[string]*Profile
	Targets  map[string]*Target

	// Features maps feature-name → list of feature names (or
	// "<dep>/<feat>" references) it transitively enables.
	Features map[string][]string

	// DefaultFeatures is the set of feature names enabled out of the
	// box. `--no-default-features` on the CLI drops this list to
	// empty; `--features foo,bar` unions onto whatever remains.
	DefaultFeatures []string
}

// Profile looks up a profile by name. Returns (nil, false) when the
// name is unknown. The built-in profiles (debug, release, profile,
// test) are always present.
func (c *Config) Profile(name string) (*Profile, bool) {
	p, ok := c.Profiles[name]
	return p, ok
}

// Target looks up a target by triple. The empty string resolves to
// the implicit host target (nil returned; callers treat nil as "use
// host").
func (c *Config) Target(triple string) (*Target, bool) {
	if triple == "" {
		return nil, true
	}
	t, ok := c.Targets[triple]
	return t, ok
}

// ProfileNames returns the sorted list of known profile names, used
// by `osty profiles` to pretty-print and by the CLI layer to validate
// `--profile <name>` inputs.
func (c *Config) ProfileNames() []string {
	out := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TargetNames returns the sorted list of known target triples.
func (c *Config) TargetNames() []string {
	out := make([]string, 0, len(c.Targets))
	for name := range c.Targets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// FeatureNames returns the sorted list of declared feature names.
func (c *Config) FeatureNames() []string {
	out := make([]string, 0, len(c.Features))
	for name := range c.Features {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Defaults returns a Config populated with the four built-in profiles
// and no custom targets or features. Callers that don't need manifest
// merging (e.g. `osty profiles` against a dir with no osty.toml) use
// this directly.
func Defaults() *Config {
	return &Config{
		Profiles: map[string]*Profile{
			NameDebug: {
				Name:     NameDebug,
				OptLevel: 0,
				Debug:    true,
				Strip:    false,
				Overflow: true,
				Inlining: false,
				LTO:      false,
				GoFlags:  []string{"-gcflags=all=-N -l"},
			},
			NameRelease: {
				Name:     NameRelease,
				OptLevel: 2,
				Debug:    false,
				Strip:    true,
				Overflow: false,
				Inlining: true,
				LTO:      true,
				GoFlags:  []string{"-ldflags=-s -w", "-trimpath"},
			},
			NameProfile: {
				Name:     NameProfile,
				OptLevel: 2,
				Debug:    true,
				Strip:    false,
				Overflow: false,
				Inlining: true,
				LTO:      false,
				GoFlags:  []string{"-gcflags=all=-l"},
			},
			NameTest: {
				Name:     NameTest,
				OptLevel: 0,
				Debug:    true,
				Strip:    false,
				Overflow: true,
				Inlining: false,
				LTO:      false,
				GoFlags:  []string{"-gcflags=all=-N -l"},
			},
		},
		Targets:         map[string]*Target{},
		Features:        map[string][]string{},
		DefaultFeatures: nil,
	}
}

// Resolved is an effective profile — the named profile after inherits
// expansion + any target-specific env overlay + the chosen feature
// set. It is the type downstream build invocations consume.
type Resolved struct {
	Profile  *Profile
	Target   *Target
	Features []string
}

// OptLevelFlags returns the `go build` flags implied by p.OptLevel.
// Mapping:
//
//	0  →  -gcflags=all=-N -l        (no opt, no inline — debuggable)
//	1  →  -gcflags=all=-l           (inline off, optimizations on)
//	2  →  (empty — Go's default: inline on, opts on)
//	3  →  (empty; LTO/strip live on Profile.LTO / Profile.Strip)
//
// Returned as a slice so the caller can splice it into a larger argv
// without post-processing. Kept decoupled from GoFlags so a user
// pinning `opt-level = 0` in the manifest deterministically gets
// debug flags regardless of what's in the built-in GoFlags.
func (p *Profile) OptLevelFlags() []string {
	if p == nil {
		return nil
	}
	return runner.OptLevelFlags(p.OptLevel)
}

// GoEnv collects the environment overrides this resolved config
// implies: GOOS, GOARCH, CGO_ENABLED from the target, plus any user
// env from the profile / target env maps. The returned map is a
// fresh copy; callers may mutate it freely.
func (r *Resolved) GoEnv() map[string]string {
	out := map[string]string{}
	if r == nil {
		return out
	}
	if r.Profile != nil {
		for k, v := range r.Profile.Env {
			out[k] = v
		}
	}
	if r.Target != nil {
		if r.Target.OS != "" {
			out["GOOS"] = r.Target.OS
		}
		if r.Target.Arch != "" {
			out["GOARCH"] = r.Target.Arch
		}
		if r.Target.CGO != nil {
			if *r.Target.CGO {
				out["CGO_ENABLED"] = "1"
			} else {
				out["CGO_ENABLED"] = "0"
			}
		}
		for k, v := range r.Target.Env {
			out[k] = v
		}
	}
	return out
}

// GoFlags returns the flat `go build` flag list for this resolved
// config. Order:
//
//  1. OptLevel-derived `-gcflags` (only when OptLevel ∈ {0, 1});
//     skipped at default levels so the user's GoFlags control alone.
//  2. Profile.GoFlags from the merged manifest (built-ins + user).
//     Entries that collide with the OptLevel flag are filtered so
//     `-gcflags=all=-N -l` doesn't appear twice.
//  3. `-ldflags=-s -w` when Profile.Strip is true.
//  4. `-tags=feat_*` synthesized from r.Features.
//
// Feature names map 1:1 to Go build tags so conditional gen output
// can gate via `//go:build feat_<name>`.
func (r *Resolved) GoFlags() []string {
	if r == nil || r.Profile == nil {
		return nil
	}
	var flags []string
	optFlags := r.Profile.OptLevelFlags()
	flags = append(flags, optFlags...)
	seen := map[string]bool{}
	for _, f := range optFlags {
		seen[f] = true
	}
	for _, f := range r.Profile.GoFlags {
		if seen[f] {
			continue
		}
		flags = append(flags, f)
		seen[f] = true
	}
	stripFlag := "-ldflags=-s -w"
	if r.Profile.Strip && !seen[stripFlag] {
		flags = append(flags, stripFlag)
		seen[stripFlag] = true
	}
	if len(r.Features) > 0 {
		tags := make([]string, 0, len(r.Features))
		for _, f := range r.Features {
			tags = append(tags, "feat_"+f)
		}
		sort.Strings(tags)
		flags = append(flags, "-tags="+strings.Join(tags, ","))
	}
	return flags
}

// Resolve builds a Resolved from a profile name, an optional target
// triple, and the user-requested feature set. Returns an error when
// the profile or target isn't declared. An empty triple means "host".
//
// requestedFeatures is the union of the manifest's default features
// (when useDefaults is true) and the user's `--features` list; this
// function then expands each transitively via c.Features.
func (c *Config) Resolve(profileName, triple string, requestedFeatures []string, useDefaults bool) (*Resolved, error) {
	if c == nil {
		return nil, fmt.Errorf("nil profile config")
	}
	p, ok := c.Profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (known: %s)",
			profileName, strings.Join(c.ProfileNames(), ", "))
	}
	var t *Target
	if triple != "" {
		tt, ok := c.Targets[triple]
		if !ok {
			return nil, fmt.Errorf("unknown target %q", triple)
		}
		t = tt
	}
	feats := c.expandFeatures(requestedFeatures, useDefaults)
	return &Resolved{Profile: p, Target: t, Features: feats}, nil
}

// expandFeatures delegates to toolchain/profile_features.osty for
// the deterministic BFS closure over c.Features. Unknown feature
// names pass through unchanged — feature cross-linking to deps is
// the concern of the resolver layer.
func (c *Config) expandFeatures(requested []string, useDefaults bool) []string {
	return runner.ExpandFeatures(c.Features, c.DefaultFeatures, requested, useDefaults)
}

// ReadFeaturePragma scans the first ~32 lines of src for a line of
// the form `// @feature: NAME[, NAME...]` and returns the list of
// feature names the file requires. Returns nil when no pragma is
// present.
//
// The grammar is intentionally terse — a single-line comment that
// starts with "@feature:" — so the check doesn't need a parser pass.
// Callers (build driver, gen emitter) combine this with the active
// feature set to decide whether the file participates in the build.
// ReadFeaturePragma delegates to toolchain/profile_pragma.osty.
func ReadFeaturePragma(src []byte) []string {
	return runner.ReadFeaturePragma(src)
}

// FileNeedsFeatures reads src's feature pragma and reports whether
// every required feature is present in `active`. Files without a
// pragma are unconditionally included. Returns (ok, missing) where
// missing is the first feature that wasn't active.
func FileNeedsFeatures(src []byte, active map[string]bool) (bool, string) {
	needed := ReadFeaturePragma(src)
	for _, f := range needed {
		if !active[f] {
			return false, f
		}
	}
	return true, ""
}
