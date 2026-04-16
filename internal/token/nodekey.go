package token

// NodeKey is a stable, content-addressable identifier for an AST node's
// source position. Unlike a `*ast.Node` pointer, NodeKey is equal
// across re-parses of the same source: the byte offset is a property
// of the source text, not of the parser run.
//
// Callers building cross-run indexes (incremental query caches,
// diff-aware LSP features, persistent on-disk indexes) should key by
// NodeKey rather than by AST pointer. Within a single-file resolution
// context [Offset] alone is unique; package-level consumers that
// combine multiple files should use [PackageNodeKey] instead.
type NodeKey struct {
	// Offset is the byte offset into the source file at which the
	// node's first token starts. Matches [Pos.Offset] of the node's
	// Pos() value.
	Offset int
}

// NodeKeyOf extracts a NodeKey from any positioned value. Callers
// typically pass an AST node's Pos() result, but any token.Pos works.
func NodeKeyOf(p Pos) NodeKey { return NodeKey{Offset: p.Offset} }

// PackageNodeKey extends [NodeKey] with a file path, so package-level
// maps whose files share overlapping offset ranges can key without
// collisions. The path is typically the [PackageFile.Path] that owned
// the node at parse time.
type PackageNodeKey struct {
	Path   string
	Offset int
}

// PackageNodeKeyOf combines a file path and a position into a
// [PackageNodeKey]. The path is caller-normalized (absolute path,
// forward slashes) so different callers keying the same file hit the
// same bucket.
func PackageNodeKeyOf(path string, p Pos) PackageNodeKey {
	return PackageNodeKey{Path: path, Offset: p.Offset}
}
