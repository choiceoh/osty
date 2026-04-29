package osty

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/resolve"
)

// SeededPackage describes the package inputs written into an Engine.
type SeededPackage struct {
	Dir     string
	DotPath string
	Name    string
	Files   []string
	Sources map[string][]byte
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
	e.seedPackageInputs(dir, files, sources)
	return SeededPackage{
		Dir:     dir,
		Name:    packageNameFromDir(dir),
		Files:   files,
		Sources: sources,
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
		e.seedPackageInputs(member.Dir, files, sources)
		for path, src := range sources {
			out.Sources[path] = src
		}
		out.Packages = append(out.Packages, SeededPackage{
			Dir:     member.Dir,
			DotPath: member.DotPath,
			Name:    member.Name,
			Files:   files,
			Sources: sources,
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
		e.seedPackageInputs(dir, files, sources)
		out.Packages = append(out.Packages, SeededPackage{
			Dir:     dir,
			DotPath: key,
			Name:    pkg.Name,
			Files:   files,
			Sources: sources,
		})
	}
	members = normalizeWorkspacePackages(root, members)
	e.Inputs.WorkspacePackages.Set(e.DB, root, members)
	return out, nil
}

func (e *Engine) seedPackageInputs(dir string, files []string, sources map[string][]byte) {
	for _, path := range files {
		e.Inputs.SourceText.Set(e.DB, path, sources[path])
	}
	e.Inputs.PackageFiles.Set(e.DB, dir, files)
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
