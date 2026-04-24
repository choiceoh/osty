package lint

// isUnderscore reports whether name starts with `_`, the convention for
// "intentionally unused" bindings (excluded from unused-name rules).
func isUnderscore(name string) bool {
	return len(name) > 0 && name[0] == '_'
}
