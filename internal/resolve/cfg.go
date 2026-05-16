package resolve

// Conditional compilation — `#[cfg(key = "value")]` environment carrier.
//
// The pre-resolve declaration filter and predicate evaluation now live
// in `toolchain/resolve.osty` (`srCfgDeclPasses` + `selfResolveAstFileWithCfg`
// + `srCheckCfgArgs`). The Go side only constructs the host-runtime CfgEnv
// and projects it onto the structured `api.CfgEnv` the bootstrapped
// resolver consumes.

import (
	"runtime"

	"github.com/osty/osty/internal/selfhost/api"
)

// CfgEnv carries the values that `#[cfg(key = "value")]` predicates
// compare against. Empty Target / Arch fall back to the Go host
// values.
type CfgEnv struct {
	// OS is the operating system class. Values match Go's
	// `runtime.GOOS` nomenclature ("linux", "darwin", "windows",
	// "freebsd", "wasm", ...).
	OS string
	// Arch is the CPU architecture class. Values match Go's
	// `runtime.GOARCH` nomenclature ("amd64", "arm64", "wasm", ...).
	Arch string
	// Target is the compilation-target label. When the build invokes
	// cross-compilation this differs from OS/Arch; otherwise the
	// build driver sets this to a string the user chose (typically
	// matching the GOOS value but potentially distinguishing, e.g.,
	// "wasm" from native "linux").
	Target string
	// Features is the set of feature flags active for the build.
	// Keys are feature names; presence means "enabled."
	Features map[string]bool
}

// DefaultCfgEnv returns a CfgEnv populated from the Go host runtime.
// The build driver overrides individual fields when cross-compiling.
func DefaultCfgEnv() *CfgEnv {
	return &CfgEnv{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Target:   runtime.GOOS,
		Features: map[string]bool{},
	}
}

// toSelfhost projects this Go-side CfgEnv onto the structured CfgEnv that the
// bootstrapped resolver consumes. Returns nil for a nil receiver so the
// native path inherits "no filtering" behaviour without extra casing.
func (c *CfgEnv) toSelfhost() *api.CfgEnv {
	if c == nil {
		return nil
	}
	features := make([]string, 0, len(c.Features))
	for name, enabled := range c.Features {
		if enabled {
			features = append(features, name)
		}
	}
	return &api.CfgEnv{
		OS:       c.OS,
		Arch:     c.Arch,
		Target:   c.Target,
		Features: features,
	}
}
