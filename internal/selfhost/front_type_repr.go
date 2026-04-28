package selfhost

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/selfhost/api"
)

// FrontTypeRepr mirrors toolchain/check.osty's FrontTypeRepr in the frozen
// Go seed. It keeps structured types intact until the public api.TypeRepr
// boundary instead of forcing checker results through a type-name string.
type FrontTypeRepr struct {
	kind string
	name string
	path string
	args []*FrontTypeRepr
	ret  *FrontTypeRepr
}

func tyToRepr(arena *TyArena, idx int) *FrontTypeRepr {
	if idx < 0 {
		return frontTypeRepr("error", "Invalid")
	}
	node := tyGet(arena, idx)
	if node == nil {
		return frontTypeRepr("error", "Invalid")
	}
	switch node.kind.(type) {
	case *TyKind_TkErr:
		return frontTypeRepr("error", "Invalid")
	case *TyKind_TkPoison:
		return frontTypeRepr("poison", "Poison")
	case *TyKind_TkPrim:
		return frontTypeRepr("primitive", primKindName(node.prim))
	case *TyKind_TkNamed:
		return &FrontTypeRepr{
			kind: "named",
			name: node.head,
			args: tyReprArgs(arena, node.args),
		}
	case *TyKind_TkOptional:
		return &FrontTypeRepr{kind: "optional", ret: tyToRepr(arena, node.ret)}
	case *TyKind_TkTuple:
		return &FrontTypeRepr{kind: "tuple", args: tyReprArgs(arena, node.args)}
	case *TyKind_TkFn:
		return &FrontTypeRepr{
			kind: "fn",
			args: tyReprArgs(arena, node.args),
			ret:  tyToRepr(arena, node.ret),
		}
	case *TyKind_TkVar:
		name := node.varName
		if name == "" {
			name = fmt.Sprintf("?%d", node.varId)
		}
		return frontTypeRepr("typevar", name)
	case *TyKind_TkSelf:
		return frontTypeRepr("self", "Self")
	default:
		return frontTypeRepr("error", "Invalid")
	}
}

func tyReprArgs(arena *TyArena, args []int) []*FrontTypeRepr {
	if len(args) == 0 {
		return nil
	}
	out := make([]*FrontTypeRepr, 0, len(args))
	for _, arg := range args {
		out = append(out, tyToRepr(arena, arg))
	}
	return out
}

func frontTypeRepr(kind, name string) *FrontTypeRepr {
	return &FrontTypeRepr{kind: kind, name: name}
}

func frontInvalidTypeRepr() *FrontTypeRepr {
	return frontTypeRepr("error", "Invalid")
}

func frontTypeReprToString(repr *FrontTypeRepr) string {
	apiRepr := frontTypeReprToAPI(repr)
	if apiRepr == nil {
		return ""
	}
	return apiRepr.String()
}

func frontTypeReprToAPI(repr *FrontTypeRepr) *api.TypeRepr {
	if repr == nil {
		return nil
	}
	out := &api.TypeRepr{
		Kind: repr.kind,
		Name: repr.name,
		Path: repr.path,
	}
	if len(repr.args) > 0 {
		out.Args = make([]api.TypeRepr, 0, len(repr.args))
		for _, arg := range repr.args {
			if converted := frontTypeReprToAPI(arg); converted != nil {
				out.Args = append(out.Args, *converted)
			}
		}
	}
	out.Return = frontTypeReprToAPI(repr.ret)
	return out
}

func frontTypeReprSliceToAPI(reprs []*FrontTypeRepr) []api.TypeRepr {
	if len(reprs) == 0 {
		return nil
	}
	out := make([]api.TypeRepr, 0, len(reprs))
	for _, repr := range reprs {
		if converted := frontTypeReprToAPI(repr); converted != nil {
			out = append(out, *converted)
		}
	}
	return out
}

// apiTypeReprFromLegacyName is the remaining resolver-only fallback for
// symbols that are still emitted as type-name text. Checker, package input,
// inspect, and lint surfaces now carry FrontTypeRepr directly.
func apiTypeReprFromLegacyName(raw string) *api.TypeRepr {
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
		inner := apiTypeReprFromLegacyName(strings.TrimSuffix(text, "?"))
		return &api.TypeRepr{Kind: "optional", Return: inner}
	}
	if strings.HasPrefix(text, "fn(") {
		return fnAPITypeReprFromLegacyName(text)
	}
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "("), ")"))
		if inner == "" {
			return &api.TypeRepr{Kind: "unit"}
		}
		parts := splitLegacyTypeReprList(inner)
		if len(parts) == 1 {
			return apiTypeReprFromLegacyName(parts[0])
		}
		args := make([]api.TypeRepr, 0, len(parts))
		for _, part := range parts {
			if tr := apiTypeReprFromLegacyName(part); tr != nil {
				args = append(args, *tr)
			}
		}
		return &api.TypeRepr{Kind: "tuple", Args: args}
	}
	head, argText, hasArgs := splitLegacyGenericRepr(text)
	if hasArgs {
		parts := splitLegacyTypeReprList(argText)
		typeArgs := make([]api.TypeRepr, 0, len(parts))
		for _, a := range parts {
			if tr := apiTypeReprFromLegacyName(a); tr != nil {
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

func fnAPITypeReprFromLegacyName(text string) *api.TypeRepr {
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return &api.TypeRepr{Kind: "error", Name: text}
	}
	close := matchingLegacyTypeReprParen(text, open)
	if close < 0 {
		return &api.TypeRepr{Kind: "error", Name: text}
	}
	paramText := strings.TrimSpace(text[open+1 : close])
	var params []api.TypeRepr
	if paramText != "" {
		for _, part := range splitLegacyTypeReprList(paramText) {
			if tr := apiTypeReprFromLegacyName(part); tr != nil {
				params = append(params, *tr)
			}
		}
	}
	var ret *api.TypeRepr
	rest := strings.TrimSpace(text[close+1:])
	if strings.HasPrefix(rest, "->") {
		ret = apiTypeReprFromLegacyName(strings.TrimSpace(strings.TrimPrefix(rest, "->")))
	}
	if ret == nil {
		ret = &api.TypeRepr{Kind: "unit"}
	}
	return &api.TypeRepr{Kind: "fn", Args: params, Return: ret}
}

func splitLegacyGenericRepr(text string) (head, args string, ok bool) {
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

func splitLegacyTypeReprList(text string) []string {
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

func matchingLegacyTypeReprParen(text string, open int) int {
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
