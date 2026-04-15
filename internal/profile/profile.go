package profile

import (
	"fmt"
	"sort"
	"strings"
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
//   - OptLevel is the optimization level passed as `-gcflags=-l=<n>`
//     to the Go backend (0 = disable inlining, 2 = default release).
//   - Debug toggles retention of DWARF symbols. `-ldflags=-w -s`
//     strips them when false.
//   - Overflow toggles integer-overflow runtime checks where the
//     emitted Go code supports them (Phase 4 of the transpiler).
//   - Inlining/LTO are per-call-site hints consumed by gen; they do
//     not affect today's Phase 1 output but are honoured by the
//     manifest parser so users can pin them ahead of time.
//   - GoFlags is an ordered list of raw flags appended after the
//     built-in derivation so users can override individual settings
//     (e.g. `-trimpath`) without recomputing the whole flag slice.
//   - Env is process-env key/value overrides threaded into the
//     `go build` invocation (GOARCH, GOOS, CGO_ENABLED, …).
//   - Inherits names another profile whose fields fill in unset
//     values at merge time. Limited to one level of indirection so
//     cycles are structurally impossible.
type Profile struct {
	Name      string
	OptLevel  int
	Debug     bool
	Strip     bool
	Overflow  bool
	Inlining  bool
	LTO       bool
	GoFlags   []string
	Env       map[string]string
	Inherits  string

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
	return &cp
}

// ParseTriple splits "<arch>-<os>" into (arch, os). Returns an error
// if the triple isn't in the expected form. The toolchain does not
// try to validate against Go's internal GOOS/GOARCH table — unknown
// combinations are surfaced by `go build` itself.
func ParseTriple(triple string) (arch, os string, err error) {
	parts := strings.SplitN(triple, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid target triple %q (want <arch>-<os>)", triple)
	}
	return parts[0], parts[1], nil
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

// OptLevelFlag returns the `-gcflags=-l=<N>` string corresponding to
// p.OptLevel. Kept as a helper so the backend can substitute its own
// convention (e.g. switching to LLVM gcc-style `-O<n>` if a future
// native backend is wired in) without rewriting every call site.
func (p *Profile) OptLevelFlag() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("-gcflags=-l=%d", p.OptLevel)
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
// config. Order: profile-derived flags, then any feature-derived
// `-tags` invocation synthesized from r.Features. Feature names map
// 1:1 to Go build tags so conditional gen output (future phase 5)
// can gate via `//go:build feat_<name>`.
func (r *Resolved) GoFlags() []string {
	if r == nil || r.Profile == nil {
		return nil
	}
	flags := append([]string(nil), r.Profile.GoFlags...)
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

// expandFeatures computes the transitive closure of requested + (if
// useDefaults is true) default-feature names through c.Features.
// Unknown feature names are passed through unchanged — feature
// cross-linking to deps is the concern of the resolver layer, not
// this package.
func (c *Config) expandFeatures(requested []string, useDefaults bool) []string {
	seed := map[string]struct{}{}
	if useDefaults {
		for _, f := range c.DefaultFeatures {
			seed[f] = struct{}{}
		}
	}
	for _, f := range requested {
		seed[f] = struct{}{}
	}
	// BFS through c.Features.
	out := map[string]struct{}{}
	queue := make([]string, 0, len(seed))
	for f := range seed {
		queue = append(queue, f)
	}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if _, done := out[f]; done {
			continue
		}
		out[f] = struct{}{}
		for _, child := range c.Features[f] {
			// "<dep>/<feat>" references are left intact — a future
			// pkgmgr hook will interpret them. For the local
			// closure we only recurse on bare feature names.
			if !strings.Contains(child, "/") {
				queue = append(queue, child)
			}
		}
	}
	names := make([]string, 0, len(out))
	for f := range out {
		names = append(names, f)
	}
	sort.Strings(names)
	return names
}
