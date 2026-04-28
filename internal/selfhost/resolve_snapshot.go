package selfhost

import (
	"fmt"
	"sort"
	"strings"
)

// ResolveSnapshot renders the canonical resolver output surface in a compact,
// deterministic text form for golden tests. It intentionally snapshots the
// selfhost ResolveResult rather than the Go compatibility scopes.
func ResolveSnapshot(result ResolveResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n", shortResolveID(result.PackageID))

	symbols := append([]ResolvedSymbol(nil), result.Symbols...)
	sort.Slice(symbols, func(i, j int) bool {
		return resolveSnapshotKey(symbols[i].File, symbols[i].Start, symbols[i].End, symbols[i].Kind, symbols[i].Name) <
			resolveSnapshotKey(symbols[j].File, symbols[j].Start, symbols[j].End, symbols[j].Kind, symbols[j].Name)
	})
	b.WriteString("symbols\n")
	for _, sym := range symbols {
		fmt.Fprintf(&b, "  %s %s %s %d:%d pub=%t id=%s decl=%s\n",
			resolveSnapshotFile(sym.File), sym.Kind, sym.Name, sym.Start, sym.End, sym.Public, shortResolveID(sym.ID), shortResolveID(sym.DeclID))
	}

	refs := append([]ResolvedRef(nil), result.Refs...)
	sort.Slice(refs, func(i, j int) bool {
		return resolveSnapshotKey(refs[i].File, refs[i].Start, refs[i].End, refs[i].Name, refs[i].TargetFile) <
			resolveSnapshotKey(refs[j].File, refs[j].Start, refs[j].End, refs[j].Name, refs[j].TargetFile)
	})
	b.WriteString("refs\n")
	for _, ref := range refs {
		fmt.Fprintf(&b, "  %s %s %d:%d -> %s %d:%d target=%s binding=%s\n",
			resolveSnapshotFile(ref.File), ref.Name, ref.Start, ref.End, resolveSnapshotFile(ref.TargetFile), ref.TargetStart, ref.TargetEnd,
			shortResolveID(ref.TargetSymbolID), shortResolveID(ref.BindingID))
	}

	typeRefs := append([]ResolvedTypeRef(nil), result.TypeRefs...)
	sort.Slice(typeRefs, func(i, j int) bool {
		return resolveSnapshotKey(typeRefs[i].File, typeRefs[i].Start, typeRefs[i].End, typeRefs[i].Name, typeRefs[i].TargetFile, typeRefs[i].TargetStart, typeRefs[i].TargetEnd) <
			resolveSnapshotKey(typeRefs[j].File, typeRefs[j].Start, typeRefs[j].End, typeRefs[j].Name, typeRefs[j].TargetFile, typeRefs[j].TargetStart, typeRefs[j].TargetEnd)
	})
	b.WriteString("typeRefs\n")
	for _, ref := range typeRefs {
		fmt.Fprintf(&b, "  %s %s %d:%d -> %s %d:%d target=%s id=%s\n",
			resolveSnapshotFile(ref.File), ref.Name, ref.Start, ref.End, resolveSnapshotFile(ref.TargetFile), ref.TargetStart, ref.TargetEnd,
			shortResolveID(ref.TargetSymbolID), shortResolveID(ref.ID))
	}

	diags := append([]ResolveDiagnosticRecord(nil), result.Diagnostics...)
	sort.Slice(diags, func(i, j int) bool {
		return resolveSnapshotKey(diags[i].File, diags[i].Start, diags[i].End, diags[i].Code, diags[i].Name) <
			resolveSnapshotKey(diags[j].File, diags[j].Start, diags[j].End, diags[j].Code, diags[j].Name)
	})
	b.WriteString("diagnostics\n")
	for _, d := range diags {
		fmt.Fprintf(&b, "  %s %s %s %d:%d id=%s\n", resolveSnapshotFile(d.File), d.Code, d.Name, d.Start, d.End, shortResolveID(d.ID))
	}
	return b.String()
}

func resolveSnapshotFile(file string) string {
	if file == "" {
		return "<source>"
	}
	return file
}

func resolveSnapshotKey(parts ...any) string {
	var b strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&b, "%v\x00", part)
	}
	return b.String()
}

func shortResolveID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
