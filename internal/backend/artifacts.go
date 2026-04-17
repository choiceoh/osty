package backend

import "path/filepath"

// SourcePath returns a clean source path for diagnostics. Kept as a helper so
// callers do not need to know the artifact struct's field names.
func (a Artifacts) SourcePath() string {
	if a.LLVMIR != "" {
		return filepath.Clean(a.LLVMIR)
	}
	return ""
}
