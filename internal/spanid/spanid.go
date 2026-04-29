// Package spanid defines stable source/span identity primitives shared by
// diagnostics, source maps, LSP, and host/selfhost adapter boundaries.
package spanid

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
)

// SourceFileID identifies one logical source file across compiler stages.
type SourceFileID string

// SpanID identifies one source span within a SourceFileID.
type SpanID string

// ProvenanceKind classifies why a span identity was derived from another span.
type ProvenanceKind string

const (
	ProvenanceOriginal      ProvenanceKind = "original"
	ProvenanceCanonical     ProvenanceKind = "canonical"
	ProvenanceExpansion     ProvenanceKind = "expansion"
	ProvenanceInjection     ProvenanceKind = "injection"
	ProvenancePackageMerge  ProvenanceKind = "package_merge"
	ProvenanceSelfhostShift ProvenanceKind = "selfhost_offset"
)

// Provenance links a derived span back to the span that produced it.
type Provenance struct {
	Kind         ProvenanceKind
	SourceFileID SourceFileID
	SpanID       SpanID
	Detail       string
}

// ProvenanceList is referenced from comparable span structs via pointer.
type ProvenanceList []Provenance

// AppendProvenance returns a list containing prior plus entries.
func AppendProvenance(prior *ProvenanceList, entries ...Provenance) *ProvenanceList {
	if len(entries) == 0 {
		return prior
	}
	var out ProvenanceList
	if prior != nil {
		out = append(out, (*prior)...)
	}
	out = append(out, entries...)
	return &out
}

// ProvenanceEntries returns a nil-safe immutable view of list.
func ProvenanceEntries(list *ProvenanceList) []Provenance {
	if list == nil {
		return nil
	}
	return *list
}

// SourceFileIDFor returns a stable, path-derived source file identity.
func SourceFileIDFor(path string) SourceFileID {
	clean := normalizePath(path)
	if clean == "" {
		clean = "<input>"
	}
	return SourceFileID("sf:" + shortHash(clean))
}

// DerivedSourceFileID returns an identity for generated source tied to parent.
func DerivedSourceFileID(parent SourceFileID, kind ProvenanceKind, detail string) SourceFileID {
	if parent == "" {
		if detail == "" {
			detail = string(kind)
		}
		return SourceFileIDFor(detail)
	}
	return SourceFileID("sf:" + shortHash(string(parent)+"|"+string(kind)+"|"+detail))
}

// SpanIDFor returns a stable identity for [start, end) inside fileID.
func SpanIDFor(fileID SourceFileID, start, end int) SpanID {
	if end < start {
		end = start
	}
	return SpanID("sp:" + shortHash(string(fileID)+"|"+strconv.Itoa(start)+"|"+strconv.Itoa(end)))
}

// DerivedSpanID returns a stable identity for a generated span.
func DerivedSpanID(fileID SourceFileID, kind ProvenanceKind, start, end int, parents ...SpanID) SpanID {
	if end < start {
		end = start
	}
	var b strings.Builder
	b.WriteString(string(fileID))
	b.WriteByte('|')
	b.WriteString(string(kind))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(start))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(end))
	for _, parent := range parents {
		b.WriteByte('|')
		b.WriteString(string(parent))
	}
	return SpanID("sp:" + shortHash(b.String()))
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if clean == "." && path != "." {
		clean = path
	}
	return filepath.ToSlash(clean)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
