package selfhost

import (
	"fmt"

	"github.com/osty/osty/internal/selfhost/api"
)

// FrontTypeRepr mirrors toolchain/check.osty's FrontTypeRepr in the frozen
// Go seed. It keeps structured types intact until the public api.TypeRepr
// boundary instead of forcing a rendered typeName string round-trip.
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
