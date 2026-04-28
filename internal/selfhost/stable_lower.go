package selfhost

// selfhostSemanticAstFile returns the parser arena shape consumed by the
// native resolver/checker. Compatibility rewrites now live in the parser core,
// so this boundary only clones the arena to keep semantic consumers from
// mutating the raw parse result.
func selfhostSemanticAstFile(file *AstFile) *AstFile {
	if file == nil || file.arena == nil {
		return file
	}
	return &AstFile{arena: selfhostCloneAstArena(file.arena)}
}

func selfhostCloneAstArena(src *AstArena) *AstArena {
	if src == nil {
		return nil
	}
	dst := &AstArena{
		nodes: make([]*AstNode, len(src.nodes)),
		decls: append([]int(nil), src.decls...),
		errors: func() []*AstParseError {
			out := make([]*AstParseError, len(src.errors))
			for i, err := range src.errors {
				if err == nil {
					continue
				}
				copy := *err
				out[i] = &copy
			}
			return out
		}(),
	}
	for i, node := range src.nodes {
		if node == nil {
			continue
		}
		copy := *node
		copy.children = append([]int(nil), node.children...)
		copy.children2 = append([]int(nil), node.children2...)
		dst.nodes[i] = &copy
	}
	return dst
}
