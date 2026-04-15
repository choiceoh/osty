package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/profile"
)

// profileFlags captures the `--profile / --release / --target /
// --features / --no-default-features` flag set shared by `osty build`
// and `osty run`. register() attaches them to a caller-owned
// FlagSet; resolve() turns the captured state into a *profile.Resolved
// or a usage error.
type profileFlags struct {
	profileName       string
	releaseShortcut   bool
	triple            string
	features          string
	noDefaultFeatures bool
}

// register binds every flag on fs. Flag names follow the convention
// used by `cargo`: --profile for the named choice, --release as the
// shorthand alias, --features for the comma-separated opt-in list,
// --no-default-features to drop the manifest default. --target takes
// a triple in the `<arch>-<os>` form parsed by profile.ParseTriple.
func (pf *profileFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&pf.profileName, "profile", "",
		"build profile name (debug, release, ...); default: debug")
	fs.BoolVar(&pf.releaseShortcut, "release", false,
		"shorthand for --profile release")
	fs.StringVar(&pf.triple, "target", "",
		"cross-compilation target triple (e.g. amd64-linux)")
	fs.StringVar(&pf.features, "features", "",
		"comma-separated feature names to enable")
	fs.BoolVar(&pf.noDefaultFeatures, "no-default-features", false,
		"disable the manifest's [features].default list")
}

// resolve merges the parsed flag state with the manifest's profile
// declarations and returns a ready-to-use *profile.Resolved plus the
// chosen profile name (so callers can stamp it into cache paths).
//
// When --profile is unset we default to "debug" for most commands;
// callers that prefer a different default can pass it in `fallback`.
// The release shortcut always wins over --profile to preserve the
// pattern `--release --profile foo` errors out.
func (pf *profileFlags) resolve(m *manifest.Manifest, fallback string) (*profile.Resolved, string, error) {
	cfg, err := profile.BuildConfig(m)
	if err != nil {
		return nil, "", err
	}
	name := pf.profileName
	if pf.releaseShortcut {
		if name != "" && name != profile.NameRelease {
			return nil, "", fmt.Errorf("--release conflicts with --profile %s", name)
		}
		name = profile.NameRelease
	}
	if name == "" {
		name = fallback
		if name == "" {
			name = profile.NameDebug
		}
	}
	// Parse the feature list. Empty strings are dropped so
	// `--features=` behaves as a no-op.
	var feats []string
	if pf.features != "" {
		for _, f := range strings.Split(pf.features, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				feats = append(feats, f)
			}
		}
	}
	resolved, err := cfg.Resolve(name, pf.triple, feats, !pf.noDefaultFeatures)
	if err != nil {
		return nil, name, err
	}
	return resolved, name, nil
}

// announceProfile prints a one-line summary of the effective profile
// + target + feature set. Kept as a helper so both `osty build` and
// `osty run` surface the same string, which tools / CI logs can
// grep for.
func announceProfile(r *profile.Resolved) {
	if r == nil || r.Profile == nil {
		return
	}
	target := "host"
	if r.Target != nil {
		target = r.Target.Triple
	}
	feats := "none"
	if len(r.Features) > 0 {
		feats = strings.Join(r.Features, ",")
	}
	fmt.Fprintf(os.Stdout, "Profile: %s  Target: %s  Features: %s\n",
		r.Profile.Name, target, feats)
}
