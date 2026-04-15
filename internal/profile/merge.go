package profile

import (
	"fmt"
	"sort"

	"github.com/osty/osty/internal/manifest"
)

// BuildConfig merges manifest-declared profiles, targets, and features
// on top of Defaults() and returns a ready-to-use Config. When m is
// nil only the built-in defaults are returned.
//
// Overlay rules:
//   - A profile with the same name as a built-in replaces its fields
//     only where the manifest explicitly set them (tracked by Has*
//     flags on manifest.Profile). Unset fields keep the built-in
//     value.
//   - A profile with a new name inherits from `inherits` (or "debug"
//     when unset) before applying its own overrides. This gives
//     users a compact way to declare e.g. `[profile.bench]` that
//     extends release.
//   - Targets are additive: a manifest triple that doesn't shadow a
//     built-in just appears in c.Targets. Built-ins currently empty.
//   - Features + default-features come straight from the manifest —
//     the toolchain has no built-in features to merge against.
//
// Inheritance is capped at one level of indirection. If the declared
// parent is itself a user profile that inherits, the chain is walked
// up to 8 steps before the merge gives up with an error to guard
// against pathological manifests.
func BuildConfig(m *manifest.Manifest) (*Config, error) {
	c := Defaults()
	if m == nil {
		return c, nil
	}

	// Phase 1: gather every manifest profile keyed by name.
	mps := m.Profiles
	// Phase 2: apply in order — built-in names first (so user tweaks
	// of debug/release land deterministically), then any new names.
	seen := map[string]bool{}
	ordered := make([]string, 0, len(mps))
	for _, name := range []string{NameDebug, NameRelease, NameProfile, NameTest} {
		if _, ok := mps[name]; ok {
			ordered = append(ordered, name)
			seen[name] = true
		}
	}
	// Sort the rest for determinism.
	extras := make([]string, 0, len(mps))
	for name := range mps {
		if !seen[name] {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	ordered = append(ordered, extras...)

	for _, name := range ordered {
		mp := mps[name]
		base, err := resolveBase(c, mps, mp, 0)
		if err != nil {
			return nil, err
		}
		merged := base.Clone()
		merged.Name = name
		merged.UserDefined = true
		applyManifestProfile(merged, mp)
		c.Profiles[name] = merged
	}

	// Targets.
	for _, mt := range m.Targets {
		arch, os, err := ParseTriple(mt.Triple)
		if err != nil {
			return nil, err
		}
		t := &Target{
			Triple:      mt.Triple,
			OS:          os,
			Arch:        arch,
			UserDefined: true,
		}
		if mt.HasCGO {
			b := mt.CGO
			t.CGO = &b
		}
		if len(mt.Env) > 0 {
			t.Env = map[string]string{}
			for k, v := range mt.Env {
				t.Env[k] = v
			}
		}
		c.Targets[mt.Triple] = t
	}

	// Features + default features.
	if len(m.Features) > 0 {
		c.Features = map[string][]string{}
		for name, deps := range m.Features {
			c.Features[name] = append([]string(nil), deps...)
		}
	}
	c.DefaultFeatures = append([]string(nil), m.DefaultFeatures...)
	return c, nil
}

// resolveBase walks the inherits chain starting from mp and returns
// the Profile to clone from. For manifest profiles overriding a
// built-in (same name) the base is the built-in with that name. For
// new names the base is the one named by mp.Inherits, or debug when
// that field is unset.
func resolveBase(c *Config, mps map[string]*manifest.Profile, mp *manifest.Profile, depth int) (*Profile, error) {
	if depth > 8 {
		return nil, fmt.Errorf("profile inheritance too deep starting at %q", mp.Name)
	}
	// Same-name override of a built-in.
	if builtin, ok := c.Profiles[mp.Name]; ok && !builtin.UserDefined {
		return builtin, nil
	}
	parentName := mp.Inherits
	if parentName == "" {
		parentName = NameDebug
	}
	if parentName == mp.Name {
		return nil, fmt.Errorf("profile %q cannot inherit from itself", mp.Name)
	}
	// Look up parent: prefer already-merged config, fall back to the
	// manifest map when the parent hasn't been merged yet.
	if p, ok := c.Profiles[parentName]; ok {
		return p, nil
	}
	parentMp, ok := mps[parentName]
	if !ok {
		return nil, fmt.Errorf("profile %q inherits from unknown profile %q",
			mp.Name, parentName)
	}
	// Recurse to let the parent build itself first.
	base, err := resolveBase(c, mps, parentMp, depth+1)
	if err != nil {
		return nil, err
	}
	merged := base.Clone()
	merged.Name = parentMp.Name
	applyManifestProfile(merged, parentMp)
	return merged, nil
}

// applyManifestProfile overlays p's declared fields onto dst. Only
// fields the manifest actually set (per the Has* flags) are copied;
// everything else keeps dst's value.
func applyManifestProfile(dst *Profile, p *manifest.Profile) {
	if p.HasOptLevel {
		dst.OptLevel = p.OptLevel
	}
	if p.HasDebug {
		dst.Debug = p.Debug
	}
	if p.HasStrip {
		dst.Strip = p.Strip
	}
	if p.HasOverflow {
		dst.Overflow = p.Overflow
	}
	if p.HasInlining {
		dst.Inlining = p.Inlining
	}
	if p.HasLTO {
		dst.LTO = p.LTO
	}
	if p.Inherits != "" {
		dst.Inherits = p.Inherits
	}
	if len(p.GoFlags) > 0 {
		dst.GoFlags = append(dst.GoFlags, p.GoFlags...)
	}
	if len(p.Env) > 0 {
		if dst.Env == nil {
			dst.Env = map[string]string{}
		}
		for k, v := range p.Env {
			dst.Env[k] = v
		}
	}
}
