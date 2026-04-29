package selfhost

import "sort"

// ResolveSurface is the resolver-owned, consumer-oriented view of a
// ResolveResult. Compiler adapters can still consume ResolveResult directly,
// while LSP, docgen, inspect, and snapshot tests can depend on this stable
// symbol/reference/diagnostic surface without walking Go compatibility scopes.
type ResolveSurface struct {
	PackageID   string
	Symbols     []ResolveSymbolSurfaceRecord
	Refs        []ResolveReferenceSurfaceRecord
	TypeRefs    []ResolveTypeReferenceSurfaceRecord
	Diagnostics []ResolveDiagnosticSurfaceRecord
}

type ResolveSymbolSurfaceRecord struct {
	SymbolID  string
	PackageID string
	DeclID    string
	Name      string
	Kind      string
	File      string
	Start     int
	End       int
	Public    bool
}

type ResolveReferenceSurfaceRecord struct {
	RefID          string
	PackageID      string
	BindingID      string
	TargetSymbolID string
	Name           string
	File           string
	Start          int
	End            int
	TargetFile     string
	TargetStart    int
	TargetEnd      int
}

type ResolveTypeReferenceSurfaceRecord struct {
	TypeRefID      string
	PackageID      string
	TargetSymbolID string
	Name           string
	File           string
	Start          int
	End            int
	TargetFile     string
	TargetStart    int
	TargetEnd      int
}

type ResolveDiagnosticSurfaceRecord struct {
	DiagnosticID string
	PackageID    string
	Code         string
	Message      string
	Name         string
	File         string
	Start        int
	End          int
	SourceFileID string
	SpanID       string
}

func ResolveSurfaceFromResult(result ResolveResult) ResolveSurface {
	surface := ResolveSurface{
		PackageID:   result.PackageID,
		Symbols:     make([]ResolveSymbolSurfaceRecord, 0, len(result.Symbols)),
		Refs:        make([]ResolveReferenceSurfaceRecord, 0, len(result.Refs)),
		TypeRefs:    make([]ResolveTypeReferenceSurfaceRecord, 0, len(result.TypeRefs)),
		Diagnostics: make([]ResolveDiagnosticSurfaceRecord, 0, len(result.Diagnostics)),
	}
	for _, sym := range result.Symbols {
		surface.Symbols = append(surface.Symbols, ResolveSymbolSurfaceRecord{
			SymbolID:  sym.ID,
			PackageID: sym.PackageID,
			DeclID:    sym.DeclID,
			Name:      sym.Name,
			Kind:      sym.Kind,
			File:      sym.File,
			Start:     sym.Start,
			End:       sym.End,
			Public:    sym.Public,
		})
	}
	for _, ref := range result.Refs {
		surface.Refs = append(surface.Refs, ResolveReferenceSurfaceRecord{
			RefID:          ref.ID,
			PackageID:      ref.PackageID,
			BindingID:      ref.BindingID,
			TargetSymbolID: ref.TargetSymbolID,
			Name:           ref.Name,
			File:           ref.File,
			Start:          ref.Start,
			End:            ref.End,
			TargetFile:     ref.TargetFile,
			TargetStart:    ref.TargetStart,
			TargetEnd:      ref.TargetEnd,
		})
	}
	for _, ref := range result.TypeRefs {
		surface.TypeRefs = append(surface.TypeRefs, ResolveTypeReferenceSurfaceRecord{
			TypeRefID:      ref.ID,
			PackageID:      ref.PackageID,
			TargetSymbolID: ref.TargetSymbolID,
			Name:           ref.Name,
			File:           ref.File,
			Start:          ref.Start,
			End:            ref.End,
			TargetFile:     ref.TargetFile,
			TargetStart:    ref.TargetStart,
			TargetEnd:      ref.TargetEnd,
		})
	}
	for _, d := range result.Diagnostics {
		surface.Diagnostics = append(surface.Diagnostics, ResolveDiagnosticSurfaceRecord{
			DiagnosticID: d.ID,
			PackageID:    d.PackageID,
			Code:         d.Code,
			Message:      d.Message,
			Name:         d.Name,
			File:         d.File,
			Start:        d.Start,
			End:          d.End,
			SourceFileID: d.SourceFileID,
			SpanID:       d.SpanID,
		})
	}
	sort.Slice(surface.Symbols, func(i, j int) bool {
		return resolveSurfaceSymbolKey(surface.Symbols[i]) < resolveSurfaceSymbolKey(surface.Symbols[j])
	})
	sort.Slice(surface.Refs, func(i, j int) bool {
		return resolveSurfaceRefKey(surface.Refs[i]) < resolveSurfaceRefKey(surface.Refs[j])
	})
	sort.Slice(surface.TypeRefs, func(i, j int) bool {
		return resolveSurfaceTypeRefKey(surface.TypeRefs[i]) < resolveSurfaceTypeRefKey(surface.TypeRefs[j])
	})
	sort.Slice(surface.Diagnostics, func(i, j int) bool {
		return resolveSurfaceDiagnosticKey(surface.Diagnostics[i]) < resolveSurfaceDiagnosticKey(surface.Diagnostics[j])
	})
	return surface
}

func resolveSurfaceSymbolKey(s ResolveSymbolSurfaceRecord) string {
	return resolveSnapshotKey(s.File, s.Start, s.End, s.Kind, s.Name, s.SymbolID)
}

func resolveSurfaceRefKey(r ResolveReferenceSurfaceRecord) string {
	return resolveSnapshotKey(r.File, r.Start, r.End, r.Name, r.TargetFile, r.TargetStart, r.TargetEnd)
}

func resolveSurfaceTypeRefKey(r ResolveTypeReferenceSurfaceRecord) string {
	return resolveSnapshotKey(r.File, r.Start, r.End, r.Name, r.TargetFile, r.TargetStart, r.TargetEnd)
}

func resolveSurfaceDiagnosticKey(d ResolveDiagnosticSurfaceRecord) string {
	return resolveSnapshotKey(d.File, d.Start, d.End, d.Code, d.Name)
}
