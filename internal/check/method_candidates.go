//go:build selfhostgen

package check

import (
	"sort"

	"github.com/osty/osty/internal/types"
)

// methodCandidates returns a list of nearby method names on a type,
// used for typo suggestions in "unknown method" diagnostics.
func (c *checker) methodCandidates(t types.Type) []string {
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" {
			seen[name] = true
		}
	}
	switch v := t.(type) {
	case *types.Named:
		if desc, ok := c.result.Descs[v.Sym]; ok {
			for name := range desc.Methods {
				add(name)
			}
			for name := range c.interfaceMethodSet(v) {
				add(name)
			}
		}
		if v.Sym != nil {
			switch v.Sym.Name {
			case "List":
				for _, name := range []string{
					"len", "isEmpty", "first", "last", "get", "contains", "indexOf", "find",
					"map", "filter", "fold", "sorted", "sortedBy", "reversed", "appended",
					"concat", "zip", "enumerate", "push", "pop", "insert", "removeAt",
					"sort", "reverse", "clear",
				} {
					add(name)
				}
			case "Map":
				for _, name := range []string{
					"len", "isEmpty", "get", "containsKey", "keys", "values", "entries",
					"insert", "remove", "clear",
				} {
					add(name)
				}
			case "Set":
				for _, name := range []string{
					"len", "isEmpty", "contains", "union", "intersect", "difference",
					"insert", "remove", "clear",
				} {
					add(name)
				}
			case "Result":
				if len(c.resultMethods) > 0 {
					for name := range c.resultMethods {
						add(name)
					}
				} else {
					for _, name := range []string{
						"isOk", "isErr", "unwrap", "expect", "unwrapErr", "expectErr", "unwrapOr", "unwrapOrElse",
						"ok", "err", "and", "andThen", "or", "orElse", "inspect", "inspectErr", "map", "mapErr", "toString",
					} {
						add(name)
					}
				}
			case "Chan", "Channel":
				for _, name := range []string{"recv", "send", "close"} {
					add(name)
				}
			case "Handle":
				add("join")
			case "TaskGroup":
				for _, name := range []string{"spawn", "cancel", "isCancelled"} {
					add(name)
				}
			}
		}
	case *types.Primitive:
		for name := range c.primMethods[v.Kind] {
			add(name)
		}
	case *types.Optional:
		for _, name := range []string{
			"isSome", "isNone", "unwrap", "expect", "unwrapOr", "unwrapOrElse",
			"and", "andThen", "or", "orElse", "xor", "filter", "inspect", "map", "orError", "okOr", "toString",
		} {
			add(name)
		}
	case *types.TypeVar:
		for _, b := range v.Bounds {
			if n, ok := b.(*types.Named); ok {
				for name := range c.interfaceMethodSet(n) {
					add(name)
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
