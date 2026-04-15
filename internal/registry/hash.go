package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// hashPrefix is the algorithm tag used in IndexEntry.Checksum values.
// Kept local so the server package does not depend on pkgmgr (which
// would create an import cycle: pkgmgr → registry → pkgmgr).
const hashPrefix = "sha256:"

// hashWriter is an io.Writer that accumulates a running sha256.
// Used by Storage.Publish to compute the tarball checksum while the
// body streams through MultiWriter to disk — no second read of the
// file is needed.
type hashWriter struct {
	h hash.Hash
}

func newHashWriter() *hashWriter { return &hashWriter{h: sha256.New()} }

func (h *hashWriter) Write(p []byte) (int, error) { return h.h.Write(p) }

func (h *hashWriter) checksum() string {
	return hashPrefix + hex.EncodeToString(h.h.Sum(nil))
}
