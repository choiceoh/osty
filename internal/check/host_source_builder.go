package check

import (
	"bytes"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/spanid"
)

func selfhostFileSource(file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var canonicalMap *sourcemap.Map
	if file != nil {
		if canonicalSrc, sm := canonical.SourceWithMap(src, file); len(canonicalSrc) > 0 && bytes.Equal(canonicalSrc, src) {
			canonicalMap = sm
		}
	}
	var b bytes.Buffer
	writeSelfhostImports(&b, nil, stdlib, fileUses(file))
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	base := b.Len()
	b.Write(src)
	if !bytes.HasSuffix(src, []byte("\n")) {
		b.WriteByte('\n')
	}
	var scope *resolve.Scope
	var refs map[ast.NodeID]*resolve.Symbol
	if rr != nil {
		scope = rr.FileScope
		refs = rr.RefsByID
	}
	return selfhostCheckedSource{
		source: b.Bytes(),
		files: []selfhostFileSegment{{
			file:      file,
			source:    append([]byte(nil), src...),
			scope:     scope,
			refs:      refs,
			base:      base,
			sourceMap: canonicalMap,
		}},
	}
}

func selfhostFileStructuredSource(file *ast.File, rr *resolve.Result, src []byte) selfhostCheckedSource {
	var canonicalMap *sourcemap.Map
	if file != nil {
		if canonicalSrc, sm := canonical.SourceWithMap(src, file); len(canonicalSrc) > 0 && bytes.Equal(canonicalSrc, src) {
			canonicalMap = sm
		}
	}
	var scope *resolve.Scope
	var refs map[ast.NodeID]*resolve.Symbol
	if rr != nil {
		scope = rr.FileScope
		refs = rr.RefsByID
	}
	return selfhostCheckedSource{
		source: append([]byte(nil), src...),
		files: []selfhostFileSegment{{
			file:          file,
			source:        append([]byte(nil), src...),
			scope:         scope,
			refs:          refs,
			base:          0,
			sourceMap:     canonicalMap,
			nativeNodeIDs: true,
		}},
	}
}

func selfhostPackageSource(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var b bytes.Buffer
	writeSelfhostPackageImports(&b, pkg, ws, stdlib)
	var files []selfhostFileSegment
	for _, pf := range pkg.Files {
		src := pf.CheckerSource()
		if len(src) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		base := b.Len()
		b.Write(src)
		if !bytes.HasSuffix(src, []byte("\n")) {
			b.WriteByte('\n')
		}
		files = append(files, selfhostFileSegment{
			file:      pf.File,
			path:      pf.Path,
			source:    append([]byte(nil), src...),
			sourceID:  checkSourceFileID(pf),
			scope:     pf.FileScope,
			refs:      pf.RefsByID,
			base:      base,
			sourceMap: pf.CheckerSourceMap(),
		})
	}
	return selfhostCheckedSource{source: b.Bytes(), files: files}
}

func checkSourceFileID(pf *resolve.PackageFile) spanid.SourceFileID {
	if pf == nil {
		return ""
	}
	if pf.SourceFileID != "" {
		return pf.SourceFileID
	}
	return spanid.SourceFileIDFor(pf.Path)
}
