package pkgmgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr/semver"
	"github.com/osty/osty/internal/registry"
)

// registrySource fetches a dependency from an osty registry. The
// selected version is the highest published version that satisfies
// the manifest's version requirement (semver.Req).
type registrySource struct {
	name         string
	packageName  string // the canonical name on the registry; may differ from `name`
	versionReq   string
	registryName string // "" = default registry
}

type registryCandidate struct {
	Version semver.Version
	Meta    registry.Version
}

func (s *registrySource) Kind() SourceKind { return SourceRegistry }
func (s *registrySource) Name() string     { return s.name }

func (s *registrySource) URI() string {
	// URL portion is resolved later (Fetch sees Env.Registries); the
	// lockfile URI embeds the declared registry name so different
	// registry hosts don't accidentally coalesce.
	if s.registryName == "" {
		return "registry+default"
	}
	return "registry+" + s.registryName
}

// Fetch queries the registry index for matching versions, selects the
// highest that satisfies s.versionReq, downloads + verifies the
// tarball, and unpacks it to the user cache. Returned LocalDir is
// that unpacked directory.
func (s *registrySource) Fetch(ctx context.Context, env *Env) (*FetchedPackage, error) {
	candidates, err := s.candidateRegistryVersions(ctx, env)
	if err != nil {
		return nil, err
	}
	return s.fetchRegistryCandidate(ctx, env, candidates[0])
}

func (s *registrySource) candidateRegistryVersions(ctx context.Context, env *Env) ([]registryCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if env.Offline {
		return nil, fmt.Errorf("registry dependency %s: offline mode forbids registry access", s.name)
	}
	regURL, ok := env.Registries[s.registryName]
	if !ok || regURL == "" {
		return nil, fmt.Errorf("registry %q not configured", s.registryName)
	}
	req, err := semver.ParseReq(s.versionReq)
	if err != nil {
		return nil, fmt.Errorf("registry dependency %s: invalid version req %q: %w",
			s.name, s.versionReq, err)
	}
	client := registry.NewClient(regURL)
	// Attach a per-registry index cache rooted under the user cache
	// dir. Conditional GETs (If-None-Match) keep repeated resolves
	// fast and let an offline machine fall back to the last known
	// view of the registry.
	client.Cache = registry.NewDirIndexCache(filepath.Join(env.CacheDir, "registry-index", sanitizeURL(regURL)))
	versions, err := client.Versions(ctx, s.packageName)
	if err != nil {
		return nil, fmt.Errorf("registry dependency %s: %w", s.name, err)
	}
	var candidates []registryCandidate
	for _, v := range versions {
		sv, err := semver.ParseVersion(v.Version)
		if err != nil {
			// Skip published versions we can't parse — log-worthy but
			// not fatal so a malformed record doesn't break otherwise
			// valid resolutions.
			continue
		}
		if !req.Match(sv) {
			continue
		}
		candidates = append(candidates, registryCandidate{Version: sv, Meta: v})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return semver.Compare(candidates[i].Version, candidates[j].Version) > 0
	})
	if len(candidates) == 0 {
		return nil, fmt.Errorf("registry dependency %s: no version matches %q",
			s.name, s.versionReq)
	}
	return candidates, nil
}

func (s *registrySource) fetchRegistryCandidate(ctx context.Context, env *Env, candidate registryCandidate) (*FetchedPackage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	regURL, ok := env.Registries[s.registryName]
	if !ok || regURL == "" {
		return nil, fmt.Errorf("registry %q not configured", s.registryName)
	}
	client := registry.NewClient(regURL)
	client.Cache = registry.NewDirIndexCache(filepath.Join(env.CacheDir, "registry-index", sanitizeURL(regURL)))
	best := candidate.Version.String()
	picked := candidate.Meta

	// Download + verify the tarball.
	cacheRoot := filepath.Join(env.CacheDir, "registry", sanitizeURL(regURL), s.packageName, best)
	if err := ensureDir(cacheRoot); err != nil {
		return nil, err
	}
	tarPath := filepath.Join(cacheRoot, "package.tgz")
	if _, err := os.Stat(tarPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := client.DownloadTarball(ctx, s.packageName, best, tarPath); err != nil {
			return nil, fmt.Errorf("registry dependency %s: download: %w", s.name, err)
		}
	}
	// Checksum verify against the registry's advertised hash.
	got, err := hashFile(tarPath)
	if err != nil {
		return nil, err
	}
	if err := VerifyChecksum(picked.Checksum, got); err != nil {
		return nil, fmt.Errorf("registry dependency %s@%s: %w", s.name, best, err)
	}

	// Unpack into a sibling dir so subsequent resolves reuse the
	// extraction.
	unpackDir := filepath.Join(cacheRoot, "unpacked")
	if _, err := os.Stat(unpackDir); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := ensureDir(unpackDir); err != nil {
			return nil, err
		}
		if err := extractTarGz(tarPath, unpackDir); err != nil {
			return nil, fmt.Errorf("registry dependency %s@%s: extract: %w",
				s.name, best, err)
		}
	}
	depManifest, err := manifest.Read(filepath.Join(unpackDir, manifest.ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("registry dependency %s@%s: no osty.toml in tarball: %w",
			s.name, best, err)
	}
	return &FetchedPackage{
		LocalDir: unpackDir,
		Manifest: depManifest,
		Version:  best,
		Checksum: picked.Checksum,
	}, nil
}

// hashFile is a thin wrapper over HashReader for files on disk.
// Keeps the registry fetch path straightforward.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return HashReader(f)
}
