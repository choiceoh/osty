package selfhost

// PackageFunctionRef is the small, stable view of a top-level function that
// host-side tools need for selection logic without lowering the public AST.
type PackageFunctionRef struct {
	Name              string
	ParamCount        int
	HasReceiver       bool
	HasReturn         bool
	HasBody           bool
	GenericParamCount int
	Annotations       []string
	Start             int
	End               int
}

// PackageFunctionsFromRun walks run's arena and returns one ref for each
// top-level function declaration. Methods nested in struct/enum declarations
// are intentionally excluded, matching the public-AST package test discovery
// path that only scanned file.Decls.
func PackageFunctionsFromRun(run *FrontendRun) []PackageFunctionRef {
	if run == nil || run.parser == nil || run.parser.arena == nil {
		return nil
	}
	arena := run.parser.arena
	out := make([]PackageFunctionRef, 0, len(arena.decls))
	for _, declIdx := range arena.decls {
		if declIdx < 0 || declIdx >= len(arena.nodes) {
			continue
		}
		n := arena.nodes[declIdx]
		if n == nil {
			continue
		}
		if _, ok := n.kind.(*AstNodeKind_AstNFnDecl); !ok {
			continue
		}
		out = append(out, packageFunctionRefFromNode(arena, n))
	}
	return out
}

func packageFunctionRefFromNode(arena *AstArena, n *AstNode) PackageFunctionRef {
	ref := PackageFunctionRef{
		Name:              n.text,
		HasReturn:         n.left >= 0,
		HasBody:           n.right >= 0,
		GenericParamCount: len(n.children2),
		Annotations:       arenaAnnotationNames(arena, n.extra),
		Start:             n.start,
		End:               n.end,
	}
	for i, childIdx := range n.children {
		if childIdx < 0 || childIdx >= len(arena.nodes) {
			continue
		}
		child := arena.nodes[childIdx]
		if child == nil {
			continue
		}
		if _, ok := child.kind.(*AstNodeKind_AstNParam); !ok {
			continue
		}
		if i == 0 && child.text == "self" {
			ref.HasReceiver = true
			continue
		}
		ref.ParamCount++
	}
	return ref
}

func arenaAnnotationNames(arena *AstArena, idx int) []string {
	if arena == nil || idx < 0 || idx >= len(arena.nodes) {
		return nil
	}
	n := arena.nodes[idx]
	if n == nil {
		return nil
	}
	if _, ok := n.kind.(*AstNodeKind_AstNAnnotation); !ok {
		return nil
	}
	if n.text == "__group" {
		out := make([]string, 0, len(n.children))
		for _, childIdx := range n.children {
			out = append(out, arenaAnnotationNames(arena, childIdx)...)
		}
		return out
	}
	if n.text == "" {
		return nil
	}
	return []string{n.text}
}
