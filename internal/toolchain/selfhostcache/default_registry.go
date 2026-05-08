package selfhostcache

// DefaultRegistryURL is the registry that fresh clones consult when
// OSTY_SELF_REGISTRY_URL is unset. Maintainer-controlled — fork users
// either replace this constant or `export OSTY_SELF_REGISTRY_URL=…`.
//
// The URL is the GitHub Releases asset download root for the upstream
// `choiceoh/osty` rolling tag `osty-self-snapshots`. GitHub serves each
// release asset as `<this-url>/<asset-filename>`, which matches the
// HTTPFetcher join shape (`<base>/<key>.json`) directly.
//
// Layout of the rolling release (see docs/operations/self_host_registry_setup.md):
//   <DefaultRegistryURL>/<sha>-<triple>.json          — manifest, append-only
//   <DefaultRegistryURL>/<sha>-<triple>.json.sig      — detached ed25519 sig (when signing key is configured)
//   <DefaultRegistryURL>/<binary-url>                 — binary referenced by manifest.BinaryURL
//   <DefaultRegistryURL>/osty-self-latest-<triple>.bin — bootstrap seed for the next publish round
//
// Override on the consumer side: set OSTY_SELF_REGISTRY_URL.
// Disable network fetch entirely: set OSTY_SELF_REGISTRY_OFFLINE=1.
//
// Setting this constant to the empty string disables the default —
// useful for fork users who deliberately want fresh clones to fall
// through to the offline path instead of hitting upstream.
const DefaultRegistryURL = "https://github.com/choiceoh/osty/releases/download/osty-self-snapshots"
