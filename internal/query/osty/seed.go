package osty

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
)

// SeededPackage describes the package inputs written into an Engine.
type SeededPackage struct {
	Dir               string
	DotPath           string
	Name              string
	RuntimeCapability bool
	Files             []string
	Sources           map[string][]byte
}

// SeededWorkspace describes the workspace inputs written into an Engine.
type SeededWorkspace struct {
	Root     string
	Packages []SeededPackage
	Sources  map[string][]byte
}

// SeedPackageDir reads a package directory from disk, applies transform before
// parsing, and writes SourceText + PackageFiles inputs for the package.
func (e *Engine) SeedPackageDir(dir string, transform resolve.SourceTransform) (SeededPackage, error) {
	dir = NormalizePath(dir)
	files, sources, err := readPackageSources(dir, transform)
	if err != nil {
		return SeededPackage{}, err
	}
	metadata := PackageMetadataForDir(dir, packageNameFromDir(dir))
	e.seedPackageInputs(dir, files, sources, metadata)
	return SeededPackage{
		Dir:               dir,
		Name:              metadataNameOrFallback(metadata, dir),
		RuntimeCapability: metadata.RuntimeCapability,
		Files:             files,
		Sources:           sources,
	}, nil
}

// SeedWorkspaceDirs reads the supplied package directories from disk and
// writes SourceText, PackageFiles, and WorkspacePackages inputs under root.
func (e *Engine) SeedWorkspaceDirs(root string, packages []WorkspacePackage, transform resolve.SourceTransform) (SeededWorkspace, error) {
	root = NormalizePath(root)
	members := normalizeWorkspacePackages(root, packages)
	out := SeededWorkspace{
		Root:    root,
		Sources: map[string][]byte{},
	}
	for _, member := range members {
		files, sources, err := readPackageSources(member.Dir, transform)
		if err != nil {
			return SeededWorkspace{}, err
		}
		metadata := PackageMetadataForDir(member.Dir, member.Name)
		e.seedPackageInputs(member.Dir, files, sources, metadata)
		for path, src := range sources {
			out.Sources[path] = src
		}
		out.Packages = append(out.Packages, SeededPackage{
			Dir:               member.Dir,
			DotPath:           member.DotPath,
			Name:              metadataNameOrFallback(metadata, member.Dir),
			RuntimeCapability: metadata.RuntimeCapability,
			Files:             files,
			Sources:           sources,
		})
	}
	e.Inputs.WorkspacePackages.Set(e.DB, root, members)
	return out, nil
}

// SeedLoadedWorkspace writes the packages already discovered by resolve into
// the query graph. This preserves dependency-manager import keys, while stdlib
// provider packages are skipped and resolved through the Engine's stdlib
// provider instead.
func (e *Engine) SeedLoadedWorkspace(ws *resolve.Workspace) (SeededWorkspace, error) {
	if ws == nil {
		return SeededWorkspace{}, fmt.Errorf("seed workspace: nil workspace")
	}
	root := NormalizePath(ws.Root)
	keys := make([]string, 0, len(ws.Packages))
	for key := range ws.Packages {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	members := make([]WorkspacePackage, 0, len(keys))
	out := SeededWorkspace{
		Root:    root,
		Sources: map[string][]byte{},
	}
	for _, key := range keys {
		if strings.HasPrefix(key, resolve.StdPrefix) {
			continue
		}
		pkg := ws.Packages[key]
		if pkg == nil || len(pkg.Files) == 0 {
			continue
		}
		dir := NormalizePath(pkg.Dir)
		member := WorkspacePackage{
			Dir:     dir,
			DotPath: key,
			Name:    pkg.Name,
		}
		members = append(members, member)

		files := make([]string, 0, len(pkg.Files))
		sources := make(map[string][]byte, len(pkg.Files))
		for _, pf := range pkg.Files {
			if pf == nil || pf.Path == "" {
				continue
			}
			path := NormalizePath(pf.Path)
			src := append([]byte(nil), pf.Source...)
			files = append(files, path)
			sources[path] = src
			out.Sources[path] = src
		}
		sort.Strings(files)
		metadata := PackageMetadata{
			Name:              pkg.Name,
			RuntimeCapability: pkg.RuntimeCapability,
		}
		e.seedPackageInputs(dir, files, sources, metadata)
		out.Packages = append(out.Packages, SeededPackage{
			Dir:               dir,
			DotPath:           key,
			Name:              metadataNameOrFallback(metadata, dir),
			RuntimeCapability: metadata.RuntimeCapability,
			Files:             files,
			Sources:           sources,
		})
	}
	members = normalizeWorkspacePackages(root, members)
	e.Inputs.WorkspacePackages.Set(e.DB, root, members)
	return out, nil
}

func (e *Engine) seedPackageInputs(dir string, files []string, sources map[string][]byte, metadata PackageMetadata) {
	for _, path := range files {
		e.Inputs.SourceText.Set(e.DB, path, sources[path])
	}
	e.Inputs.PackageMetadata.Set(e.DB, dir, metadata)
	e.Inputs.PackageFiles.Set(e.DB, dir, files)
}

// PackageMetadataForDir reads manifest-backed package metadata when present.
// It deliberately tolerates missing or malformed manifests and falls back to the
// caller's name so editor and scratch-buffer paths can keep analyzing source.
func PackageMetadataForDir(dir string, fallbackName string) PackageMetadata {
	dir = NormalizePath(dir)
	metadata := PackageMetadata{Name: fallbackName}
	if metadata.Name == "" {
		metadata.Name = packageNameFromDir(dir)
	}
	src, err := os.ReadFile(filepath.Join(dir, manifest.ManifestFile))
	if err != nil {
		return metadata
	}
	m, err := manifest.Parse(src)
	if err != nil || m == nil {
		return metadata
	}
	if m.HasPackage && m.Package.Name != "" {
		metadata.Name = m.Package.Name
	}
	if m.Capabilities != nil {
		metadata.RuntimeCapability = m.Capabilities.Runtime
	}
	return metadata
}

func metadataNameOrFallback(metadata PackageMetadata, dir string) string {
	if metadata.Name != "" {
		return metadata.Name
	}
	return packageNameFromDir(dir)
}

// PackageRuntimeCapabilityFromManifest preserves the older narrow helper in
// terms of the fuller package metadata input.
func PackageRuntimeCapabilityFromManifest(dir string) bool {
	return PackageMetadataForDir(dir, "").RuntimeCapability
}

func readPackageSources(dir string, transform resolve.SourceTransform) ([]string, map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []string
	sources := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		path := NormalizePath(filepath.Join(dir, name))
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		if transform != nil {
			src = transform(path, src)
		}
		files = append(files, path)
		sources[path] = src
	}
	sort.Strings(files)
	return files, sources, nil
}
