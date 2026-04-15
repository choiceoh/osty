package pkgmgr

import (
	"errors"
	"io/fs"
	"os"
)

// The filesystem helpers live here as injectable variables so unit
// tests can drive resolver / vendor paths without touching the real
// filesystem. Defaults forward to the os package.

var (
	lstatFunc   = os.Lstat
	readDirFunc = func(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
	removeFunc  = os.Remove
	symlinkFunc = os.Symlink

	// osModeSymlink is exposed as a variable so tests can flip it
	// when emulating filesystems without symlink support. Production
	// code just reads it as a constant.
	osModeSymlink = os.ModeSymlink
)

// isNotExistErr tolerates both the os.ErrNotExist sentinel and the
// wrapped variants fs ops sometimes return.
func isNotExistErr(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist)
}
