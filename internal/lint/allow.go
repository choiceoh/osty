package lint

import (
	"github.com/osty/osty/internal/runner"
)

// Lint name expansion shared by project-level config. Declaration-level
// #[allow(...)] suppression is implemented by the self-hosted lint pass.
//
// Accepted NAMEs:
//   - A concrete code: `L0001`, `L0040`, ...
//   - A category alias: `unused`, `shadow`, `dead_code`, `naming`,
//     `simplify`, `complexity`, `docs`
//   - A rule alias: `unused_let`, `missing_doc`, ...
//   - The wildcards `lint` or `all`

// resolveAllowName maps a single argument name to one or more lint
// codes. Unknown names resolve to an empty slice (silently ignored).
func resolveAllowName(name string) []string {
	// Direct code reference: L0001, L0040, etc.
	if isLintCode(name) {
		return []string{name}
	}
	if name == "dead_code" || name == "unreachable" {
		return []string{"L0020"}
	}
	category := name
	switch name {
	case "shadow":
		category = string(CategoryShadowing)
	case "suspicious":
		category = string(CategorySimplify)
	}
	var codes []string
	for _, rule := range allRules {
		if string(rule.Category) == category {
			codes = append(codes, rule.Code)
		}
	}
	if len(codes) > 0 {
		return codes
	}
	for _, rule := range allRules {
		if rule.Name == name {
			return []string{rule.Code}
		}
	}
	return nil
}

// isLintCode bridges to runner.IsLintCode (mirror of
// toolchain/lint_codes.osty). The Lxxxx namespace rule lives in
// toolchain.
func isLintCode(code string) bool {
	return runner.IsLintCode(code)
}
