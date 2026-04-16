package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type selectedPackageEntry struct {
	sourcePath string
	pkg        *resolve.Package
	res        *resolve.PackageResult
	chk        *check.Result
	file       *resolve.PackageFile
}

func loadSelectedPackageEntry(path string) (*selectedPackageEntry, bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, false, err
	}
	files := toolchainPackageInputFiles(absPath)
	if len(files) == 0 {
		return nil, false, nil
	}
	entry, err := loadSelectedPackageFiles(absPath, files)
	if err != nil {
		return nil, true, err
	}
	return entry, true, nil
}

func loadSelectedPackageFiles(sourcePath string, files []string) (*selectedPackageEntry, error) {
	pkg, err := resolve.LoadPackageFiles(files, stdlib.LoadCached())
	if err != nil {
		return nil, err
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res, checkOpts())
	var entryFile *resolve.PackageFile
	for _, pf := range pkg.Files {
		if pf != nil && pf.Path == sourcePath {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		return nil, fmt.Errorf("%s is not part of the selected package slice", sourcePath)
	}
	return &selectedPackageEntry{
		sourcePath: sourcePath,
		pkg:        pkg,
		res:        res,
		chk:        chk,
		file:       entryFile,
	}, nil
}

func toolchainPackageInputFiles(sourcePath string) []string {
	dir := filepath.Dir(sourcePath)
	if filepath.Base(dir) != "toolchain" {
		return nil
	}
	base := filepath.Base(sourcePath)
	if strings.HasSuffix(base, "_test.osty") || base == "ast_lower.osty" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".osty") {
			continue
		}
		if strings.HasSuffix(name, "_test.osty") || name == "ast_lower.osty" {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files
}

func (e *selectedPackageEntry) fileResult() *resolve.Result {
	if e == nil || e.file == nil {
		return nil
	}
	return &resolve.Result{
		Refs:      e.file.Refs,
		TypeRefs:  e.file.TypeRefs,
		FileScope: e.file.FileScope,
	}
}
