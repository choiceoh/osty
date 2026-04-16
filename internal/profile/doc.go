// Package profile models the build-time choices the user can make
// without editing source: the build *profile* (debug / release /
// profile / test), the target *triple* (GOOS/GOARCH pair governing
// cross-compilation), and the set of opt-in *features* that gate
// conditional code paths.
//
// Each concept has a first-class type here plus a helper that merges
// the manifest-declared values (from `[profile.*]`, `[target.*]`,
// `[features]`) with the toolchain's built-in defaults, so callers
// downstream can work with a single resolved Config regardless of
// whether the user customized anything.
//
// The package is also the home of the build-cache metadata types:
// when `osty build` produces a cacheable backend artifact it records a
// per-(profile, target, backend) entry under <project>/.osty/cache/ so
// subsequent builds can detect that the sources haven't changed and skip the
// expensive emit step. The schema is intentionally small (source hashes plus
// artifact paths) because the cache is advisory — a missing / corrupt entry
// just forces a rebuild, never a failure.
package profile
