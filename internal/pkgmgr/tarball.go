package pkgmgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// extractTarGz unpacks a .tgz archive at path into dir. Safety
// measures:
//
//   - Path traversal rejection: any entry whose cleaned path escapes
//     `dir` is refused.
//   - Symlinks are NOT followed — they're written as symlinks.
//   - File modes are masked to 0644 / 0755 to avoid picking up
//     surprising permissions from badly-packaged archives.
//
// Directory entries in the archive are honored but directories are
// also created on demand when a file entry needs them, so
// archive-producing tools don't have to emit explicit dir entries.
func extractTarGz(path, dir string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		dst := filepath.Join(dirAbs, filepath.Clean(hdr.Name))
		rel, rerr := filepath.Rel(dirAbs, dst)
		if rerr != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("archive entry %q escapes extraction root", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			// Only allow intra-archive symlinks.
			target := hdr.Linkname
			if filepath.IsAbs(target) {
				return fmt.Errorf("archive entry %q uses absolute symlink target", hdr.Name)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(dst), target))
			if rel, rerr := filepath.Rel(dirAbs, resolved); rerr != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("archive entry %q symlink escapes extraction root", hdr.Name)
			}
			_ = os.Remove(dst) // overwrite if present
			if err := os.Symlink(target, dst); err != nil {
				return err
			}
		default:
			// Hardlinks, character/block devices, fifos, etc. are
			// refused — osty packages must be plain source trees.
			return fmt.Errorf("archive entry %q has unsupported type %c", hdr.Name, hdr.Typeflag)
		}
	}
}

// CreateTarGz packs the contents of src into a gzipped tar archive
// written to out. Used by `osty publish` to build the upload payload.
//
// File entries are written in sorted path order so two publishes of
// the same source tree produce byte-identical archives (and so
// byte-identical sha256 checksums). Files under the skip list
// (.osty, .git, …) are excluded so we don't ship our own caches.
func CreateTarGz(src string, out io.Writer) error {
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	type entry struct {
		rel  string
		abs  string
		info os.FileInfo
	}
	var files []entry
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if skipDirEntry(rel, info) {
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return nil
		}
		files = append(files, entry{rel: rel, abs: path, info: info})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	for _, e := range files {
		if e.info.IsDir() {
			h := &tar.Header{
				Name:     filepath.ToSlash(e.rel) + "/",
				Mode:     0o755,
				Typeflag: tar.TypeDir,
			}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			continue
		}
		if e.info.Mode()&os.ModeSymlink != 0 {
			target, rerr := os.Readlink(e.abs)
			if rerr != nil {
				return rerr
			}
			h := &tar.Header{
				Name:     filepath.ToSlash(e.rel),
				Linkname: target,
				Typeflag: tar.TypeSymlink,
				Mode:     0o644,
			}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			continue
		}
		if !e.info.Mode().IsRegular() {
			// Skip sockets, devices, pipes — not part of an osty package.
			continue
		}
		buf, rerr := os.ReadFile(e.abs)
		if rerr != nil {
			return rerr
		}
		h := &tar.Header{
			Name:     filepath.ToSlash(e.rel),
			Mode:     0o644,
			Size:     int64(len(buf)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, bytes.NewReader(buf)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
