package resolve

// preludeNames lists every name that the v0.2 prelude (LANG_SPEC §10.4)
// auto-imports into every Osty source file. Each entry produces a
// SymBuiltin in the resolver's root scope.
//
// The list is deliberately flat: the resolver does not need to know that
// `Some` and `None` are variants of `Option<T>` — that's the type
// checker's concern. We only need the names to be visible.
var preludeNames = []struct {
	name string
	kind SymbolKind
}{
	// Primitive types (§2.1).
	{"Int", SymBuiltin},
	{"Int8", SymBuiltin},
	{"Int16", SymBuiltin},
	{"Int32", SymBuiltin},
	{"Int64", SymBuiltin},
	{"UInt8", SymBuiltin},
	{"UInt16", SymBuiltin},
	{"UInt32", SymBuiltin},
	{"UInt64", SymBuiltin},
	{"Byte", SymBuiltin},
	{"Float", SymBuiltin},
	{"Float32", SymBuiltin},
	{"Float64", SymBuiltin},
	{"Bool", SymBuiltin},
	{"Char", SymBuiltin},
	{"String", SymBuiltin},
	{"Bytes", SymBuiltin},
	{"Never", SymBuiltin},

	// Runtime-only primitive (LANG_SPEC §19.3). Resolved as a concrete
	// primitive so privileged packages typecheck real `RawPtr` uses;
	// the privilege gate in `internal/check/privilege.go` (E0770)
	// rejects references from ordinary user packages.
	{"RawPtr", SymBuiltin},

	// Collection types (§2.4 / §10.6).
	{"List", SymBuiltin},
	{"Map", SymBuiltin},
	{"Set", SymBuiltin},

	// Concurrency types (§8.5).
	{"Chan", SymBuiltin},
	{"Channel", SymBuiltin},

	// Option / Result / Error (§7, §10.4).
	{"Option", SymBuiltin},
	{"Result", SymBuiltin},
	{"Error", SymBuiltin},

	// Variants of Option/Result, used as expressions and in patterns.
	{"Some", SymBuiltin},
	{"None", SymBuiltin},
	{"Ok", SymBuiltin},
	{"Err", SymBuiltin},

	// Built-in interfaces (§2.6.4).
	{"Equal", SymBuiltin},
	{"Ordered", SymBuiltin},
	{"Hashable", SymBuiltin},

	// Bool literals (treated as values via prelude — the parser already
	// emits them as BoolLit, but `true`/`false` may also appear in
	// patterns where the resolver sees them as Idents).
	{"true", SymBuiltin},
	{"false", SymBuiltin},

	// Prelude functions (§10.4).
	{"print", SymBuiltin},
	{"println", SymBuiltin},
	{"eprint", SymBuiltin},
	{"eprintln", SymBuiltin},
	{"dbg", SymBuiltin},
	{"taskGroup", SymBuiltin},
	{"parallel", SymBuiltin},
	{"spawn", SymBuiltin},

	// Concurrency module (§8). Surface as a name so `thread.chan::<T>(n)`
	// and `thread.select(...)` resolve; the generator intercepts specific
	// method calls and lowers them to Go channel primitives.
	{"thread", SymBuiltin},
	{"Handle", SymBuiltin},
	{"TaskGroup", SymBuiltin},
}

// NewPrelude returns a fresh root scope pre-populated with every prelude
// symbol. Each call returns a new scope so resolvers running in parallel
// don't share state.
func NewPrelude() *Scope {
	root := NewScope(nil, "prelude")
	for _, p := range preludeNames {
		root.DefineForce(&Symbol{
			Name: p.name,
			Kind: p.kind,
			Pub:  true,
		})
	}
	return root
}
