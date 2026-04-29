package lsp

import (
	"strings"

	"github.com/osty/osty/internal/resolve"
)

type structuredReference struct {
	name           string
	uri            string
	rng            Range
	targetSymbolID string
	targetName     string
	targetKind     string
	targetType     string
	targetURI      string
	targetRange    Range
	builtin        bool
}

type structuredSymbol struct {
	id             string
	name           string
	kind           string
	typeText       string
	uri            string
	rng            Range
	selectionRange Range
	depth          int
	builtin        bool
}

type structuredImportSurface struct {
	alias   string
	symbols []structuredImportSymbol
}

type structuredImportSymbol struct {
	name     string
	kind     string
	typeText string
}

func buildStructuredReferences(pkgs []*resolve.Package) []structuredReference {
	var out []structuredReference
	for _, pkg := range pkgs {
		out = append(out, buildStructuredReferencesForPackage(pkg)...)
	}
	return out
}

func buildStructuredReferencesForPackage(pkg *resolve.Package) []structuredReference {
	if pkg == nil {
		return nil
	}
	facts, err := resolve.NativeResolveFacts(pkg)
	if err != nil {
		return nil
	}
	files := make(map[string][]byte, len(pkg.Files))
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		files[pf.Path] = pf.Source
	}
	out := make([]structuredReference, 0, len(facts.Refs))
	for _, ref := range facts.Refs {
		src, ok := files[ref.File]
		if !ok {
			continue
		}
		li := newLineIndex(src)
		targetURI := ""
		var targetRange Range
		if ref.TargetFile != "" {
			targetURI = uriForSourcePath(ref.TargetFile)
			if targetSrc, ok := files[ref.TargetFile]; ok {
				targetStart, targetEnd := ref.TargetStart, ref.TargetEnd
				if ref.TargetName != "" {
					if nameOff := LSPFindNameOffset(targetSrc, ref.TargetStart, ref.TargetEnd, ref.TargetName); nameOff >= 0 {
						targetStart = nameOff
						targetEnd = nameOff + len(ref.TargetName)
					}
				}
				targetRange = newLineIndex(targetSrc).rangeFromOffsets(targetStart, targetEnd)
			}
		}
		out = append(out, structuredReference{
			name:           ref.Name,
			uri:            uriForSourcePath(ref.File),
			rng:            li.rangeFromOffsets(ref.Start, ref.End),
			targetSymbolID: ref.TargetSymbolID,
			targetName:     ref.TargetName,
			targetKind:     ref.TargetKind,
			targetType:     ref.TargetType,
			targetURI:      targetURI,
			targetRange:    targetRange,
			builtin:        ref.Builtin,
		})
	}
	return out
}

func buildStructuredSymbols(pkgs []*resolve.Package) []structuredSymbol {
	var out []structuredSymbol
	for _, pkg := range pkgs {
		out = append(out, buildStructuredSymbolsForPackage(pkg)...)
	}
	return out
}

func buildStructuredSymbolsForPackage(pkg *resolve.Package) []structuredSymbol {
	if pkg == nil {
		return nil
	}
	facts, err := resolve.NativeResolveFacts(pkg)
	if err != nil {
		return nil
	}
	files := make(map[string][]byte, len(pkg.Files))
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		files[pf.Path] = pf.Source
	}
	out := make([]structuredSymbol, 0, len(facts.Symbols))
	for _, sym := range facts.Symbols {
		if sym.ID == "" || sym.File == "" {
			continue
		}
		src, ok := files[sym.File]
		if !ok {
			continue
		}
		li := newLineIndex(src)
		rng := li.rangeFromOffsets(sym.Start, sym.End)
		nameStart, nameEnd := sym.Start, sym.End
		if nameOff := LSPFindNameOffset(src, sym.Start, sym.End, sym.Name); nameOff >= 0 {
			nameStart = nameOff
			nameEnd = nameOff + len(sym.Name)
		}
		out = append(out, structuredSymbol{
			id:             sym.ID,
			name:           sym.Name,
			kind:           sym.Kind,
			typeText:       sym.Type,
			uri:            uriForSourcePath(sym.File),
			rng:            rng,
			selectionRange: li.rangeFromOffsets(nameStart, nameEnd),
			depth:          sym.Depth,
			builtin:        sym.Builtin,
		})
	}
	return out
}

func buildStructuredImports(pkgs []*resolve.Package) []structuredImportSurface {
	var out []structuredImportSurface
	seenAlias := map[string]bool{}
	for _, pkg := range pkgs {
		for _, surface := range buildStructuredImportsForPackage(pkg) {
			if surface.alias == "" || seenAlias[surface.alias] {
				continue
			}
			seenAlias[surface.alias] = true
			out = append(out, surface)
		}
	}
	return out
}

func buildStructuredImportsForPackage(pkg *resolve.Package) []structuredImportSurface {
	if pkg == nil {
		return nil
	}
	facts, err := resolve.NativeResolveFacts(pkg)
	if err != nil {
		return nil
	}
	out := make([]structuredImportSurface, 0, len(facts.Imports))
	for _, imp := range facts.Imports {
		surface := structuredImportSurface{
			alias:   imp.Alias,
			symbols: make([]structuredImportSymbol, 0, len(imp.Symbols)),
		}
		for _, sym := range imp.Symbols {
			surface.symbols = append(surface.symbols, structuredImportSymbol{
				name:     sym.Name,
				kind:     sym.Kind,
				typeText: sym.Type,
			})
		}
		out = append(out, surface)
	}
	return out
}

func uriForSourcePath(path string) string {
	if strings.Contains(path, ":") && !strings.HasPrefix(path, "/") {
		return path
	}
	return pathToURI(path)
}

func structuredReferenceAt(doc *document, lspPos Position) *structuredReference {
	if doc == nil || doc.analysis == nil {
		return nil
	}
	for i := range doc.analysis.structuredRefs {
		ref := &doc.analysis.structuredRefs[i]
		if ref.uri != doc.uri {
			continue
		}
		if rangeContainsPosition(ref.rng, lspPos) {
			return ref
		}
	}
	return nil
}

func structuredSymbolAt(doc *document, lspPos Position) *structuredSymbol {
	if doc == nil || doc.analysis == nil {
		return nil
	}
	for i := range doc.analysis.structuredSymbols {
		sym := &doc.analysis.structuredSymbols[i]
		if sym.uri != doc.uri {
			continue
		}
		if rangeContainsPosition(sym.selectionRange, lspPos) {
			return sym
		}
	}
	return nil
}

func rangeContainsPosition(r Range, p Position) bool {
	return LSPContainsPosition(
		int(r.Start.Line),
		int(r.Start.Character),
		int(r.End.Line),
		int(r.End.Character),
		int(p.Line),
		int(p.Character),
	)
}

func structuredReferencesForTarget(refs []structuredReference, symbols []structuredSymbol, targetID string, includeDecl bool) []Location {
	if targetID == "" {
		return nil
	}
	out := make([]Location, 0, len(refs)+1)
	var decl *structuredReference
	for i := range refs {
		ref := &refs[i]
		if ref.targetSymbolID != targetID {
			continue
		}
		out = append(out, Location{URI: ref.uri, Range: ref.rng})
		if decl == nil && ref.targetURI != "" && !ref.builtin {
			decl = ref
		}
	}
	if includeDecl {
		if sym := structuredSymbolForTarget(symbols, targetID); sym != nil {
			out = append(out, Location{URI: sym.uri, Range: sym.selectionRange})
		} else if decl != nil {
			out = append(out, Location{URI: decl.targetURI, Range: decl.targetRange})
		}
	}
	return sortDedupLocations(out)
}

func structuredSymbolForTarget(symbols []structuredSymbol, targetID string) *structuredSymbol {
	if targetID == "" {
		return nil
	}
	for i := range symbols {
		if symbols[i].id == targetID {
			return &symbols[i]
		}
	}
	return nil
}
