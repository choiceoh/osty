package selfhost

import (
	"strings"

	"github.com/osty/osty/internal/selfhost/api"
)

// typeReprFromRenderedName is the legacy parser for generated paths that
// still expose rendered type names instead of FrontTypeRepr, currently
// resolver and inspect records. Checker results use FrontTypeRepr directly.
func typeReprFromRenderedName(raw string) *api.TypeRepr {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	switch text {
	case "Invalid":
		return &api.TypeRepr{Kind: "error", Name: text}
	case "Poison":
		return &api.TypeRepr{Kind: "poison", Name: text}
	case "()", "Unit":
		return &api.TypeRepr{Kind: "unit"}
	case "Never":
		return &api.TypeRepr{Kind: "never", Name: "Never"}
	case "UntypedInt":
		return &api.TypeRepr{Kind: "primitive", Name: "UntypedInt"}
	case "UntypedFloat":
		return &api.TypeRepr{Kind: "primitive", Name: "UntypedFloat"}
	}
	if strings.HasSuffix(text, "?") {
		inner := typeReprFromRenderedName(strings.TrimSuffix(text, "?"))
		return &api.TypeRepr{Kind: "optional", Return: inner}
	}
	if strings.HasPrefix(text, "fn(") {
		return fnTypeReprFromRenderedName(text)
	}
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "("), ")"))
		if inner == "" {
			return &api.TypeRepr{Kind: "unit"}
		}
		parts := splitRenderedTypeReprList(inner)
		if len(parts) == 1 {
			return typeReprFromRenderedName(parts[0])
		}
		args := make([]api.TypeRepr, 0, len(parts))
		for _, part := range parts {
			if tr := typeReprFromRenderedName(part); tr != nil {
				args = append(args, *tr)
			}
		}
		return &api.TypeRepr{Kind: "tuple", Args: args}
	}
	head, argText, hasArgs := splitRenderedGenericRepr(text)
	if hasArgs {
		parts := splitRenderedTypeReprList(argText)
		typeArgs := make([]api.TypeRepr, 0, len(parts))
		for _, a := range parts {
			if tr := typeReprFromRenderedName(a); tr != nil {
				typeArgs = append(typeArgs, *tr)
			}
		}
		return &api.TypeRepr{Kind: "named", Name: head, Args: typeArgs}
	}
	if len(head) == 1 && head[0] >= 'A' && head[0] <= 'Z' {
		return &api.TypeRepr{Kind: "typevar", Name: head}
	}
	return &api.TypeRepr{Kind: "primitive", Name: head}
}

func fnTypeReprFromRenderedName(text string) *api.TypeRepr {
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return &api.TypeRepr{Kind: "error", Name: text}
	}
	close := matchingRenderedTypeReprParen(text, open)
	if close < 0 {
		return &api.TypeRepr{Kind: "error", Name: text}
	}
	paramText := strings.TrimSpace(text[open+1 : close])
	var params []api.TypeRepr
	if paramText != "" {
		for _, part := range splitRenderedTypeReprList(paramText) {
			if tr := typeReprFromRenderedName(part); tr != nil {
				params = append(params, *tr)
			}
		}
	}
	var ret *api.TypeRepr
	rest := strings.TrimSpace(text[close+1:])
	if strings.HasPrefix(rest, "->") {
		ret = typeReprFromRenderedName(strings.TrimSpace(strings.TrimPrefix(rest, "->")))
	}
	if ret == nil {
		ret = &api.TypeRepr{Kind: "unit"}
	}
	return &api.TypeRepr{Kind: "fn", Args: params, Return: ret}
}

func splitRenderedGenericRepr(text string) (head, args string, ok bool) {
	depth := 0
	start := -1
	for i, r := range text {
		switch r {
		case '<':
			if depth == 0 {
				start = i
			}
			depth++
		case '>':
			depth--
			if depth == 0 && i == len(text)-1 && start >= 0 {
				return strings.TrimSpace(text[:start]), strings.TrimSpace(text[start+1 : i]), true
			}
		}
	}
	return strings.TrimSpace(text), "", false
}

func splitRenderedTypeReprList(text string) []string {
	var out []string
	start := 0
	angle := 0
	paren := 0
	for i, r := range text {
		switch r {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case ',':
			if angle == 0 && paren == 0 {
				part := strings.TrimSpace(text[start:i])
				if part != "" {
					out = append(out, part)
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(text[start:]); part != "" {
		out = append(out, part)
	}
	return out
}

func matchingRenderedTypeReprParen(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
