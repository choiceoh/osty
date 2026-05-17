// Package mirjson defines the JSON boundary for shipping MIR modules to
// managed backend subprocesses without re-running the frontend.
package mirjson

import (
	"fmt"
	"sort"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

const Version = 1

type Module struct {
	Version     int         `json:"version"`
	PackageName string      `json:"packageName,omitempty"`
	Functions   []Function  `json:"functions,omitempty"`
	Globals     []Global    `json:"globals,omitempty"`
	Uses        []Use       `json:"uses,omitempty"`
	Layouts     LayoutTable `json:"layouts,omitempty"`
	Span        Span        `json:"span,omitempty"`
}

type Span struct {
	Start int `json:"start,omitempty"`
	End   int `json:"end,omitempty"`
}

type Type struct {
	Kind    string `json:"kind"`
	Display string `json:"display,omitempty"`

	Prim    string `json:"prim,omitempty"`
	Package string `json:"package,omitempty"`
	Name    string `json:"name,omitempty"`
	Builtin bool   `json:"builtin,omitempty"`
	Owner   string `json:"owner,omitempty"`

	Args   []Type `json:"args,omitempty"`
	Inner  *Type  `json:"inner,omitempty"`
	Elems  []Type `json:"elems,omitempty"`
	Params []Type `json:"params,omitempty"`
	Return *Type  `json:"return,omitempty"`
}

type Global struct {
	Name       string `json:"name,omitempty"`
	Type       *Type  `json:"type,omitempty"`
	Mut        bool   `json:"mut,omitempty"`
	HasInit    bool   `json:"hasInit,omitempty"`
	InitSymbol string `json:"initSymbol,omitempty"`
	Span       Span   `json:"span,omitempty"`
}

type Use struct {
	Path         []string `json:"path,omitempty"`
	RawPath      string   `json:"rawPath,omitempty"`
	Alias        string   `json:"alias,omitempty"`
	IsGoFFI      bool     `json:"isGoFFI,omitempty"`
	IsRuntimeFFI bool     `json:"isRuntimeFFI,omitempty"`
	GoPath       string   `json:"goPath,omitempty"`
	RuntimePath  string   `json:"runtimePath,omitempty"`
	Span         Span     `json:"span,omitempty"`
}

type Function struct {
	Name        string  `json:"name,omitempty"`
	Params      []int   `json:"params,omitempty"`
	ReturnType  *Type   `json:"returnType,omitempty"`
	ReturnLocal int     `json:"returnLocal,omitempty"`
	Locals      []Local `json:"locals,omitempty"`
	Blocks      []Block `json:"blocks,omitempty"`
	Entry       int     `json:"entry,omitempty"`
	IsExternal  bool    `json:"isExternal,omitempty"`
	IsIntrinsic bool    `json:"isIntrinsic,omitempty"`
	Exported    bool    `json:"exported,omitempty"`
	Span        Span    `json:"span,omitempty"`

	ExportSymbol string `json:"exportSymbol,omitempty"`
	CABI         bool   `json:"cAbi,omitempty"`

	Vectorize          bool     `json:"vectorize,omitempty"`
	NoVectorize        bool     `json:"noVectorize,omitempty"`
	VectorizeWidth     int      `json:"vectorizeWidth,omitempty"`
	VectorizeScalable  bool     `json:"vectorizeScalable,omitempty"`
	VectorizePredicate bool     `json:"vectorizePredicate,omitempty"`
	Parallel           bool     `json:"parallel,omitempty"`
	Unroll             bool     `json:"unroll,omitempty"`
	UnrollCount        int      `json:"unrollCount,omitempty"`
	InlineMode         int      `json:"inlineMode,omitempty"`
	Hot                bool     `json:"hot,omitempty"`
	Cold               bool     `json:"cold,omitempty"`
	TargetFeatures     []string `json:"targetFeatures,omitempty"`
	NoaliasAll         bool     `json:"noaliasAll,omitempty"`
	NoaliasParams      []string `json:"noaliasParams,omitempty"`
	Pure               bool     `json:"pure,omitempty"`
}

type Local struct {
	ID       int    `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     *Type  `json:"type,omitempty"`
	Mut      bool   `json:"mut,omitempty"`
	IsParam  bool   `json:"isParam,omitempty"`
	IsReturn bool   `json:"isReturn,omitempty"`
	Span     Span   `json:"span,omitempty"`
}

type Block struct {
	ID     int     `json:"id,omitempty"`
	Instrs []Instr `json:"instrs,omitempty"`
	Term   *Term   `json:"term,omitempty"`
	Span   Span    `json:"span,omitempty"`
}

type Instr struct {
	Kind string `json:"kind"`
	Span Span   `json:"span,omitempty"`

	Dest         *Place    `json:"dest,omitempty"`
	Src          *RValue   `json:"src,omitempty"`
	Callee       *Callee   `json:"callee,omitempty"`
	Args         []Operand `json:"args,omitempty"`
	Intrinsic    int       `json:"intrinsic,omitempty"`
	StorageLocal int       `json:"storageLocal,omitempty"`
}

type Callee struct {
	Kind    string   `json:"kind"`
	Symbol  string   `json:"symbol,omitempty"`
	Type    *Type    `json:"type,omitempty"`
	Operand *Operand `json:"operand,omitempty"`
}

type Term struct {
	Kind    string       `json:"kind"`
	Target  int          `json:"target,omitempty"`
	Cond    *Operand     `json:"cond,omitempty"`
	Then    int          `json:"then,omitempty"`
	Else    int          `json:"else,omitempty"`
	Cases   []SwitchCase `json:"cases,omitempty"`
	Default int          `json:"default,omitempty"`
	Span    Span         `json:"span,omitempty"`
}

type SwitchCase struct {
	Value  int64  `json:"value,omitempty"`
	Target int    `json:"target,omitempty"`
	Label  string `json:"label,omitempty"`
}

type Place struct {
	Local       int          `json:"local"`
	Projections []Projection `json:"projections,omitempty"`
}

type Projection struct {
	Kind     string   `json:"kind"`
	Index    int      `json:"index,omitempty"`
	Name     string   `json:"name,omitempty"`
	FieldIdx int      `json:"fieldIdx,omitempty"`
	IndexOp  *Operand `json:"indexOp,omitempty"`
	Type     *Type    `json:"type,omitempty"`
}

type Operand struct {
	Kind  string `json:"kind"`
	Place *Place `json:"place,omitempty"`
	Const *Const `json:"const,omitempty"`
	Type  *Type  `json:"type,omitempty"`
}

type Const struct {
	Kind   string  `json:"kind"`
	Type   *Type   `json:"type,omitempty"`
	Int    int64   `json:"int,omitempty"`
	Bool   bool    `json:"bool,omitempty"`
	Float  float64 `json:"float,omitempty"`
	String string  `json:"string,omitempty"`
	Char   int32   `json:"char,omitempty"`
	Byte   byte    `json:"byte,omitempty"`
	Symbol string  `json:"symbol,omitempty"`
}

type RValue struct {
	Kind string `json:"kind"`

	Op     *Operand `json:"op,omitempty"`
	Unary  int      `json:"unary,omitempty"`
	Arg    *Operand `json:"arg,omitempty"`
	Binary int      `json:"binary,omitempty"`
	Left   *Operand `json:"left,omitempty"`
	Right  *Operand `json:"right,omitempty"`
	Type   *Type    `json:"type,omitempty"`

	AggregateKind int       `json:"aggregateKind,omitempty"`
	Fields        []Operand `json:"fields,omitempty"`
	VariantIdx    int       `json:"variantIdx,omitempty"`
	VariantTag    string    `json:"variantTag,omitempty"`

	Place *Place `json:"place,omitempty"`

	CastKind int    `json:"castKind,omitempty"`
	From     *Type  `json:"from,omitempty"`
	To       *Type  `json:"to,omitempty"`
	Name     string `json:"name,omitempty"`
	Nullary  int    `json:"nullary,omitempty"`
}

type LayoutTable struct {
	Structs    []StructLayout    `json:"structs,omitempty"`
	Enums      []EnumLayout      `json:"enums,omitempty"`
	Tuples     []TupleLayout     `json:"tuples,omitempty"`
	Interfaces []InterfaceLayout `json:"interfaces,omitempty"`
}

type StructLayout struct {
	Key               string  `json:"key,omitempty"`
	Name              string  `json:"name,omitempty"`
	Mangled           string  `json:"mangled,omitempty"`
	Fields            []Field `json:"fields,omitempty"`
	BuiltinSource     string  `json:"builtinSource,omitempty"`
	BuiltinSourceArgs []Type  `json:"builtinSourceArgs,omitempty"`
	Size              int     `json:"size,omitempty"`
	Align             int     `json:"align,omitempty"`
}

type Field struct {
	Index int    `json:"index,omitempty"`
	Name  string `json:"name,omitempty"`
	Type  *Type  `json:"type,omitempty"`
}

type EnumLayout struct {
	Key               string          `json:"key,omitempty"`
	Name              string          `json:"name,omitempty"`
	Mangled           string          `json:"mangled,omitempty"`
	BuiltinSource     string          `json:"builtinSource,omitempty"`
	BuiltinSourceArgs []Type          `json:"builtinSourceArgs,omitempty"`
	Discriminant      *Type           `json:"discriminant,omitempty"`
	Variants          []VariantLayout `json:"variants,omitempty"`
}

type VariantLayout struct {
	Index   int     `json:"index,omitempty"`
	Name    string  `json:"name,omitempty"`
	Payload []Field `json:"payload,omitempty"`
}

type TupleLayout struct {
	Key     string  `json:"key,omitempty"`
	Mangled string  `json:"mangled,omitempty"`
	Fields  []Field `json:"fields,omitempty"`
}

type InterfaceLayout struct {
	Key     string            `json:"key,omitempty"`
	Name    string            `json:"name,omitempty"`
	Methods []InterfaceMethod `json:"methods,omitempty"`
	Impls   []InterfaceImpl   `json:"impls,omitempty"`
}

type InterfaceMethod struct {
	Name string `json:"name,omitempty"`
	Slot int    `json:"slot,omitempty"`
}

type InterfaceImpl struct {
	ImplName  string `json:"implName,omitempty"`
	VtableSym string `json:"vtableSym,omitempty"`
}

func FromModule(m *mir.Module) (*Module, error) {
	if m == nil {
		return nil, fmt.Errorf("nil MIR module")
	}
	out := &Module{
		Version:     Version,
		PackageName: m.Package,
		Span:        fromSpan(m.SpanV),
	}
	for _, u := range m.Uses {
		if u == nil {
			continue
		}
		out.Uses = append(out.Uses, Use{
			Path:         append([]string(nil), u.Path...),
			RawPath:      u.RawPath,
			Alias:        u.Alias,
			IsGoFFI:      u.IsGoFFI,
			IsRuntimeFFI: u.IsRuntimeFFI,
			GoPath:       u.GoPath,
			RuntimePath:  u.RuntimePath,
			Span:         fromSpan(u.SpanV),
		})
	}
	seenFunctions := map[string]bool{}
	for _, fn := range m.Functions {
		if fn == nil {
			continue
		}
		out.Functions = append(out.Functions, fromFunction(fn))
		seenFunctions[fn.Name] = true
	}
	for _, g := range m.Globals {
		if g == nil {
			continue
		}
		j := Global{
			Name: g.Name,
			Type: fromType(g.Type),
			Mut:  g.Mut,
			Span: fromSpan(g.SpanV),
		}
		if g.Init != nil {
			j.HasInit = true
			j.InitSymbol = g.Init.Name
			if !seenFunctions[g.Init.Name] {
				out.Functions = append(out.Functions, fromFunction(g.Init))
				seenFunctions[g.Init.Name] = true
			}
		}
		out.Globals = append(out.Globals, j)
	}
	out.Layouts = fromLayoutTable(m.Layouts)
	return out, nil
}

func ToModule(in *Module) (*mir.Module, error) {
	if in == nil {
		return nil, fmt.Errorf("nil MIR JSON module")
	}
	if in.Version != 0 && in.Version != Version {
		return nil, fmt.Errorf("unsupported MIR JSON version %d", in.Version)
	}
	out := &mir.Module{
		Package: in.PackageName,
		SpanV:   toSpan(in.Span),
	}
	layouts, err := toLayoutTable(in.Layouts)
	if err != nil {
		return nil, err
	}
	out.Layouts = layouts
	for _, u := range in.Uses {
		out.Uses = append(out.Uses, &mir.Use{
			Path:         append([]string(nil), u.Path...),
			RawPath:      u.RawPath,
			Alias:        u.Alias,
			IsGoFFI:      u.IsGoFFI,
			IsRuntimeFFI: u.IsRuntimeFFI,
			GoPath:       u.GoPath,
			RuntimePath:  u.RuntimePath,
			SpanV:        toSpan(u.Span),
		})
	}
	functions := map[string]*mir.Function{}
	for i := range in.Functions {
		fn, err := toFunction(in.Functions[i])
		if err != nil {
			return nil, err
		}
		functions[fn.Name] = fn
		out.Functions = append(out.Functions, fn)
	}
	initSymbols := map[string]bool{}
	for _, g := range in.Globals {
		glob, err := toGlobal(g, functions)
		if err != nil {
			return nil, err
		}
		if glob.Init != nil {
			initSymbols[glob.Init.Name] = true
		}
		out.Globals = append(out.Globals, glob)
	}
	if len(initSymbols) > 0 {
		kept := out.Functions[:0]
		for _, fn := range out.Functions {
			if fn != nil && initSymbols[fn.Name] {
				continue
			}
			kept = append(kept, fn)
		}
		out.Functions = kept
	}
	return out, nil
}

func fromSpan(s mir.Span) Span {
	return Span{Start: s.Start.Offset, End: s.End.Offset}
}

func toSpan(s Span) mir.Span {
	return mir.Span{
		Start: ir.Pos{Offset: s.Start},
		End:   ir.Pos{Offset: s.End},
	}
}

func fromType(t ir.Type) *Type {
	if t == nil {
		return nil
	}
	out := &Type{Display: t.String()}
	switch x := t.(type) {
	case *ir.PrimType:
		out.Kind = "prim"
		out.Prim = x.String()
	case *ir.NamedType:
		out.Kind = "named"
		out.Package = x.Package
		out.Name = x.Name
		out.Builtin = x.Builtin
		out.Args = fromTypes(x.Args)
	case *ir.OptionalType:
		out.Kind = "optional"
		out.Inner = fromType(x.Inner)
	case *ir.TupleType:
		out.Kind = "tuple"
		out.Elems = fromTypes(x.Elems)
	case *ir.FnType:
		out.Kind = "fn"
		out.Params = fromTypes(x.Params)
		out.Return = fromType(x.Return)
	case *ir.TypeVar:
		out.Kind = "typevar"
		out.Name = x.Name
		out.Owner = x.Owner
	case *ir.ErrType:
		// R3 brick 6+ graceful fallback at the JSON serializer:
		// rather than leaking `<error>` typed locals into the staged
		// MIR JSON (which osty-self's lir_proto.osty cannot lower and
		// surfaces as "unsupported local type `<error>`" declines),
		// downgrade poisoned types to a plain Int. This trades type-
		// precision in upstream recoverOperandType cover gaps for a
		// build-progress path. The lir_proto graceful fallback (R3
		// brick 4/5) still handles real type mismatches; this layer
		// just stops the propagation hint from reaching the subprocess.
		out.Kind = "prim"
		out.Prim = "Int"
		out.Display = "Int"
	default:
		out.Kind = "unknown"
	}
	return out
}

func fromTypes(ts []ir.Type) []Type {
	if len(ts) == 0 {
		return nil
	}
	out := make([]Type, 0, len(ts))
	for _, t := range ts {
		jt := fromType(t)
		if jt == nil {
			out = append(out, Type{Kind: "nil"})
			continue
		}
		out = append(out, *jt)
	}
	return out
}

func toType(t *Type) (ir.Type, error) {
	if t == nil || t.Kind == "" || t.Kind == "nil" {
		return nil, nil
	}
	switch t.Kind {
	case "prim":
		return primType(t.Prim)
	case "named":
		args, err := toTypes(t.Args)
		if err != nil {
			return nil, err
		}
		return &ir.NamedType{Package: t.Package, Name: t.Name, Args: args, Builtin: t.Builtin}, nil
	case "optional":
		inner, err := toType(t.Inner)
		if err != nil {
			return nil, err
		}
		return &ir.OptionalType{Inner: inner}, nil
	case "tuple":
		elems, err := toTypes(t.Elems)
		if err != nil {
			return nil, err
		}
		return &ir.TupleType{Elems: elems}, nil
	case "fn":
		params, err := toTypes(t.Params)
		if err != nil {
			return nil, err
		}
		ret, err := toType(t.Return)
		if err != nil {
			return nil, err
		}
		return &ir.FnType{Params: params, Return: ret}, nil
	case "typevar":
		return &ir.TypeVar{Name: t.Name, Owner: t.Owner}, nil
	case "error":
		return ir.ErrTypeVal, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON type kind %q", t.Kind)
	}
}

func toTypes(in []Type) ([]ir.Type, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]ir.Type, 0, len(in))
	for i := range in {
		t, err := toType(&in[i])
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func primType(name string) (ir.Type, error) {
	switch name {
	case "Int":
		return ir.TInt, nil
	case "Int8":
		return ir.TInt8, nil
	case "Int16":
		return ir.TInt16, nil
	case "Int32":
		return ir.TInt32, nil
	case "Int64":
		return ir.TInt64, nil
	case "UInt8":
		return ir.TUInt8, nil
	case "UInt16":
		return ir.TUInt16, nil
	case "UInt32":
		return ir.TUInt32, nil
	case "UInt64":
		return ir.TUInt64, nil
	case "Byte":
		return ir.TByte, nil
	case "Float":
		return ir.TFloat, nil
	case "Float32":
		return ir.TFloat32, nil
	case "Float64":
		return ir.TFloat64, nil
	case "Bool":
		return ir.TBool, nil
	case "Char":
		return ir.TChar, nil
	case "String":
		return ir.TString, nil
	case "Bytes":
		return ir.TBytes, nil
	case "RawPtr":
		return ir.TRawPtr, nil
	case "()", "Unit":
		return ir.TUnit, nil
	case "Never":
		return ir.TNever, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON primitive type %q", name)
	}
}

func fromFunction(fn *mir.Function) Function {
	out := Function{
		Name:               fn.Name,
		ReturnType:         fromType(fn.ReturnType),
		ReturnLocal:        int(fn.ReturnLocal),
		Entry:              int(fn.Entry),
		IsExternal:         fn.IsExternal,
		IsIntrinsic:        fn.IsIntrinsic,
		Exported:           fn.Exported,
		Span:               fromSpan(fn.SpanV),
		ExportSymbol:       fn.ExportSymbol,
		CABI:               fn.CABI,
		Vectorize:          fn.Vectorize,
		NoVectorize:        fn.NoVectorize,
		VectorizeWidth:     fn.VectorizeWidth,
		VectorizeScalable:  fn.VectorizeScalable,
		VectorizePredicate: fn.VectorizePredicate,
		Parallel:           fn.Parallel,
		Unroll:             fn.Unroll,
		UnrollCount:        fn.UnrollCount,
		InlineMode:         fn.InlineMode,
		Hot:                fn.Hot,
		Cold:               fn.Cold,
		TargetFeatures:     append([]string(nil), fn.TargetFeatures...),
		NoaliasAll:         fn.NoaliasAll,
		NoaliasParams:      append([]string(nil), fn.NoaliasParams...),
		Pure:               fn.Pure,
	}
	for _, p := range fn.Params {
		out.Params = append(out.Params, int(p))
	}
	for _, l := range fn.Locals {
		if l == nil {
			continue
		}
		out.Locals = append(out.Locals, Local{
			ID:       int(l.ID),
			Name:     l.Name,
			Type:     fromType(l.Type),
			Mut:      l.Mut,
			IsParam:  l.IsParam,
			IsReturn: l.IsReturn,
			Span:     fromSpan(l.SpanV),
		})
	}
	for _, b := range fn.Blocks {
		if b == nil {
			continue
		}
		out.Blocks = append(out.Blocks, fromBlock(b))
	}
	return out
}

func toFunction(in Function) (*mir.Function, error) {
	ret, err := toType(in.ReturnType)
	if err != nil {
		return nil, err
	}
	fn := &mir.Function{
		Name:               in.Name,
		ReturnType:         ret,
		ReturnLocal:        mir.LocalID(in.ReturnLocal),
		Entry:              mir.BlockID(in.Entry),
		IsExternal:         in.IsExternal,
		IsIntrinsic:        in.IsIntrinsic,
		Exported:           in.Exported,
		SpanV:              toSpan(in.Span),
		ExportSymbol:       in.ExportSymbol,
		CABI:               in.CABI,
		Vectorize:          in.Vectorize,
		NoVectorize:        in.NoVectorize,
		VectorizeWidth:     in.VectorizeWidth,
		VectorizeScalable:  in.VectorizeScalable,
		VectorizePredicate: in.VectorizePredicate,
		Parallel:           in.Parallel,
		Unroll:             in.Unroll,
		UnrollCount:        in.UnrollCount,
		InlineMode:         in.InlineMode,
		Hot:                in.Hot,
		Cold:               in.Cold,
		TargetFeatures:     append([]string(nil), in.TargetFeatures...),
		NoaliasAll:         in.NoaliasAll,
		NoaliasParams:      append([]string(nil), in.NoaliasParams...),
		Pure:               in.Pure,
	}
	for _, p := range in.Params {
		fn.Params = append(fn.Params, mir.LocalID(p))
	}
	for _, l := range in.Locals {
		local, err := toLocal(l)
		if err != nil {
			return nil, err
		}
		fn.Locals = append(fn.Locals, local)
	}
	for _, b := range in.Blocks {
		block, err := toBlock(b)
		if err != nil {
			return nil, err
		}
		fn.Blocks = append(fn.Blocks, block)
	}
	return fn, nil
}

func toLocal(in Local) (*mir.Local, error) {
	t, err := toType(in.Type)
	if err != nil {
		return nil, err
	}
	return &mir.Local{
		ID:       mir.LocalID(in.ID),
		Name:     in.Name,
		Type:     t,
		Mut:      in.Mut,
		IsParam:  in.IsParam,
		IsReturn: in.IsReturn,
		SpanV:    toSpan(in.Span),
	}, nil
}

func fromBlock(b *mir.BasicBlock) Block {
	out := Block{ID: int(b.ID), Span: fromSpan(b.SpanV)}
	for _, instr := range b.Instrs {
		out.Instrs = append(out.Instrs, fromInstr(instr))
	}
	if b.Term != nil {
		term := fromTerm(b.Term)
		out.Term = &term
	}
	return out
}

func toBlock(in Block) (*mir.BasicBlock, error) {
	b := &mir.BasicBlock{ID: mir.BlockID(in.ID), SpanV: toSpan(in.Span)}
	for _, ji := range in.Instrs {
		instr, err := toInstr(ji)
		if err != nil {
			return nil, err
		}
		b.Instrs = append(b.Instrs, instr)
	}
	if in.Term != nil {
		term, err := toTerm(*in.Term)
		if err != nil {
			return nil, err
		}
		b.Term = term
	}
	return b, nil
}

func fromInstr(i mir.Instr) Instr {
	switch x := i.(type) {
	case *mir.AssignInstr:
		dest := fromPlace(x.Dest)
		src := fromRValue(x.Src)
		return Instr{Kind: "assign", Dest: &dest, Src: &src, Span: fromSpan(x.SpanV)}
	case *mir.CallInstr:
		out := Instr{Kind: "call", Callee: fromCallee(x.Callee), Args: fromOperands(x.Args), Span: fromSpan(x.SpanV)}
		if x.Dest != nil {
			dest := fromPlace(*x.Dest)
			out.Dest = &dest
		}
		return out
	case *mir.IntrinsicInstr:
		out := Instr{Kind: "intrinsic", Intrinsic: int(x.Kind), Args: fromOperands(x.Args), Span: fromSpan(x.SpanV)}
		if x.Dest != nil {
			dest := fromPlace(*x.Dest)
			out.Dest = &dest
		}
		return out
	case *mir.StorageLiveInstr:
		return Instr{Kind: "storage_live", StorageLocal: int(x.Local), Span: fromSpan(x.SpanV)}
	case *mir.StorageDeadInstr:
		return Instr{Kind: "storage_dead", StorageLocal: int(x.Local), Span: fromSpan(x.SpanV)}
	default:
		return Instr{Kind: "invalid"}
	}
}

func toInstr(in Instr) (mir.Instr, error) {
	switch in.Kind {
	case "assign":
		if in.Dest == nil || in.Src == nil {
			return nil, fmt.Errorf("assign instruction missing dest or src")
		}
		dest, err := toPlace(*in.Dest)
		if err != nil {
			return nil, err
		}
		src, err := toRValue(*in.Src)
		if err != nil {
			return nil, err
		}
		return &mir.AssignInstr{Dest: dest, Src: src, SpanV: toSpan(in.Span)}, nil
	case "call":
		callee, err := toCallee(in.Callee)
		if err != nil {
			return nil, err
		}
		args, err := toOperands(in.Args)
		if err != nil {
			return nil, err
		}
		var dest *mir.Place
		if in.Dest != nil {
			p, err := toPlace(*in.Dest)
			if err != nil {
				return nil, err
			}
			dest = &p
		}
		return &mir.CallInstr{Dest: dest, Callee: callee, Args: args, SpanV: toSpan(in.Span)}, nil
	case "intrinsic":
		args, err := toOperands(in.Args)
		if err != nil {
			return nil, err
		}
		var dest *mir.Place
		if in.Dest != nil {
			p, err := toPlace(*in.Dest)
			if err != nil {
				return nil, err
			}
			dest = &p
		}
		return &mir.IntrinsicInstr{Dest: dest, Kind: mir.IntrinsicKind(in.Intrinsic), Args: args, SpanV: toSpan(in.Span)}, nil
	case "storage_live":
		return &mir.StorageLiveInstr{Local: mir.LocalID(in.StorageLocal), SpanV: toSpan(in.Span)}, nil
	case "storage_dead":
		return &mir.StorageDeadInstr{Local: mir.LocalID(in.StorageLocal), SpanV: toSpan(in.Span)}, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON instruction kind %q", in.Kind)
	}
}

func fromCallee(c mir.Callee) *Callee {
	switch x := c.(type) {
	case *mir.FnRef:
		return &Callee{Kind: "fn", Symbol: x.Symbol, Type: fromType(x.Type)}
	case *mir.IndirectCall:
		op := fromOperand(x.Callee)
		return &Callee{Kind: "indirect", Operand: &op}
	default:
		return &Callee{Kind: "invalid"}
	}
}

func toCallee(in *Callee) (mir.Callee, error) {
	if in == nil {
		return nil, fmt.Errorf("call instruction missing callee")
	}
	switch in.Kind {
	case "fn":
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.FnRef{Symbol: in.Symbol, Type: t}, nil
	case "indirect":
		if in.Operand == nil {
			return nil, fmt.Errorf("indirect call missing operand")
		}
		op, err := toOperand(*in.Operand)
		if err != nil {
			return nil, err
		}
		return &mir.IndirectCall{Callee: op}, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON callee kind %q", in.Kind)
	}
}

func fromTerm(t mir.Terminator) Term {
	switch x := t.(type) {
	case *mir.GotoTerm:
		return Term{Kind: "goto", Target: int(x.Target), Span: fromSpan(x.SpanV)}
	case *mir.BranchTerm:
		cond := fromOperand(x.Cond)
		return Term{Kind: "branch", Cond: &cond, Then: int(x.Then), Else: int(x.Else), Span: fromSpan(x.SpanV)}
	case *mir.SwitchIntTerm:
		cond := fromOperand(x.Scrutinee)
		out := Term{Kind: "switch_int", Cond: &cond, Default: int(x.Default), Span: fromSpan(x.SpanV)}
		for _, c := range x.Cases {
			out.Cases = append(out.Cases, SwitchCase{Value: c.Value, Target: int(c.Target), Label: c.Label})
		}
		return out
	case *mir.ReturnTerm:
		return Term{Kind: "return", Span: fromSpan(x.SpanV)}
	case *mir.UnreachableTerm:
		return Term{Kind: "unreachable", Span: fromSpan(x.SpanV)}
	default:
		return Term{Kind: "invalid"}
	}
}

func toTerm(in Term) (mir.Terminator, error) {
	switch in.Kind {
	case "goto":
		return &mir.GotoTerm{Target: mir.BlockID(in.Target), SpanV: toSpan(in.Span)}, nil
	case "branch":
		if in.Cond == nil {
			return nil, fmt.Errorf("branch terminator missing condition")
		}
		cond, err := toOperand(*in.Cond)
		if err != nil {
			return nil, err
		}
		return &mir.BranchTerm{Cond: cond, Then: mir.BlockID(in.Then), Else: mir.BlockID(in.Else), SpanV: toSpan(in.Span)}, nil
	case "switch_int":
		if in.Cond == nil {
			return nil, fmt.Errorf("switch_int terminator missing scrutinee")
		}
		cond, err := toOperand(*in.Cond)
		if err != nil {
			return nil, err
		}
		cases := make([]mir.SwitchCase, 0, len(in.Cases))
		for _, c := range in.Cases {
			cases = append(cases, mir.SwitchCase{Value: c.Value, Target: mir.BlockID(c.Target), Label: c.Label})
		}
		return &mir.SwitchIntTerm{Scrutinee: cond, Cases: cases, Default: mir.BlockID(in.Default), SpanV: toSpan(in.Span)}, nil
	case "return":
		return &mir.ReturnTerm{SpanV: toSpan(in.Span)}, nil
	case "unreachable":
		return &mir.UnreachableTerm{SpanV: toSpan(in.Span)}, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON terminator kind %q", in.Kind)
	}
}

func fromPlace(p mir.Place) Place {
	out := Place{Local: int(p.Local)}
	for _, proj := range p.Projections {
		out.Projections = append(out.Projections, fromProjection(proj))
	}
	return out
}

func toPlace(in Place) (mir.Place, error) {
	out := mir.Place{Local: mir.LocalID(in.Local)}
	for _, proj := range in.Projections {
		decoded, err := toProjection(proj)
		if err != nil {
			return mir.Place{}, err
		}
		out.Projections = append(out.Projections, decoded)
	}
	return out, nil
}

func fromProjection(p mir.Projection) Projection {
	switch x := p.(type) {
	case *mir.FieldProj:
		return Projection{Kind: "field", Index: x.Index, Name: x.Name, Type: fromType(x.Type)}
	case *mir.TupleProj:
		return Projection{Kind: "tuple", Index: x.Index, Type: fromType(x.Type)}
	case *mir.VariantProj:
		return Projection{Kind: "variant", Index: x.Variant, Name: x.Name, FieldIdx: x.FieldIdx, Type: fromType(x.Type)}
	case *mir.IndexProj:
		op := fromOperand(x.Index)
		return Projection{Kind: "index", IndexOp: &op, Type: fromType(x.ElemType)}
	case *mir.DerefProj:
		return Projection{Kind: "deref", Type: fromType(x.Type)}
	default:
		return Projection{Kind: "invalid"}
	}
}

func toProjection(in Projection) (mir.Projection, error) {
	t, err := toType(in.Type)
	if err != nil {
		return nil, err
	}
	switch in.Kind {
	case "field":
		return &mir.FieldProj{Index: in.Index, Name: in.Name, Type: t}, nil
	case "tuple":
		return &mir.TupleProj{Index: in.Index, Type: t}, nil
	case "variant":
		return &mir.VariantProj{Variant: in.Index, Name: in.Name, FieldIdx: in.FieldIdx, Type: t}, nil
	case "index":
		var op mir.Operand = &mir.ConstOp{Const: &mir.IntConst{Value: int64(in.Index), T: mir.TInt}, T: mir.TInt}
		if in.IndexOp != nil {
			decoded, err := toOperand(*in.IndexOp)
			if err != nil {
				return nil, err
			}
			op = decoded
		}
		return &mir.IndexProj{Index: op, ElemType: t}, nil
	case "deref":
		return &mir.DerefProj{Type: t}, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON projection kind %q", in.Kind)
	}
}

func fromOperand(op mir.Operand) Operand {
	switch x := op.(type) {
	case *mir.CopyOp:
		p := fromPlace(x.Place)
		return Operand{Kind: "copy", Place: &p, Type: fromType(x.T)}
	case *mir.MoveOp:
		p := fromPlace(x.Place)
		return Operand{Kind: "move", Place: &p, Type: fromType(x.T)}
	case *mir.ConstOp:
		c := fromConst(x.Const)
		return Operand{Kind: "const", Const: &c, Type: fromType(x.Type())}
	default:
		return Operand{Kind: "invalid"}
	}
}

func fromOperands(in []mir.Operand) []Operand {
	if len(in) == 0 {
		return nil
	}
	out := make([]Operand, 0, len(in))
	for _, op := range in {
		out = append(out, fromOperand(op))
	}
	return out
}

func toOperand(in Operand) (mir.Operand, error) {
	t, err := toType(in.Type)
	if err != nil {
		return nil, err
	}
	switch in.Kind {
	case "copy":
		if in.Place == nil {
			return nil, fmt.Errorf("copy operand missing place")
		}
		place, err := toPlace(*in.Place)
		if err != nil {
			return nil, err
		}
		return &mir.CopyOp{Place: place, T: t}, nil
	case "move":
		if in.Place == nil {
			return nil, fmt.Errorf("move operand missing place")
		}
		place, err := toPlace(*in.Place)
		if err != nil {
			return nil, err
		}
		return &mir.MoveOp{Place: place, T: t}, nil
	case "const":
		if in.Const == nil {
			return nil, fmt.Errorf("const operand missing const payload")
		}
		c, err := toConst(*in.Const)
		if err != nil {
			return nil, err
		}
		return &mir.ConstOp{Const: c, T: t}, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON operand kind %q", in.Kind)
	}
}

func toOperands(in []Operand) ([]mir.Operand, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]mir.Operand, 0, len(in))
	for _, op := range in {
		decoded, err := toOperand(op)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

func fromConst(c mir.Const) Const {
	switch x := c.(type) {
	case *mir.IntConst:
		return Const{Kind: "int", Int: x.Value, Type: fromType(x.Type())}
	case *mir.BoolConst:
		return Const{Kind: "bool", Bool: x.Value, Type: fromType(x.Type())}
	case *mir.FloatConst:
		return Const{Kind: "float", Float: x.Value, Type: fromType(x.Type())}
	case *mir.StringConst:
		return Const{Kind: "string", String: x.Value, Type: fromType(x.Type())}
	case *mir.CharConst:
		return Const{Kind: "char", Char: x.Value, Type: fromType(x.Type())}
	case *mir.ByteConst:
		return Const{Kind: "byte", Byte: x.Value, Type: fromType(x.Type())}
	case *mir.UnitConst:
		return Const{Kind: "unit", Type: fromType(x.Type())}
	case *mir.NullConst:
		return Const{Kind: "null", Type: fromType(x.Type())}
	case *mir.FnConst:
		return Const{Kind: "fn", Symbol: x.Symbol, Type: fromType(x.Type())}
	default:
		return Const{Kind: "invalid"}
	}
}

func toConst(in Const) (mir.Const, error) {
	t, err := toType(in.Type)
	if err != nil {
		return nil, err
	}
	switch in.Kind {
	case "int":
		return &mir.IntConst{Value: in.Int, T: t}, nil
	case "bool":
		return &mir.BoolConst{Value: in.Bool}, nil
	case "float":
		return &mir.FloatConst{Value: in.Float, T: t}, nil
	case "string":
		return &mir.StringConst{Value: in.String}, nil
	case "char":
		return &mir.CharConst{Value: in.Char}, nil
	case "byte":
		return &mir.ByteConst{Value: in.Byte}, nil
	case "unit":
		return &mir.UnitConst{}, nil
	case "null":
		return &mir.NullConst{T: t}, nil
	case "fn":
		return &mir.FnConst{Symbol: in.Symbol, T: t}, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON const kind %q", in.Kind)
	}
}

func fromRValue(rv mir.RValue) RValue {
	switch x := rv.(type) {
	case *mir.UseRV:
		op := fromOperand(x.Op)
		return RValue{Kind: "use", Op: &op}
	case *mir.UnaryRV:
		arg := fromOperand(x.Arg)
		return RValue{Kind: "unary", Unary: int(x.Op), Arg: &arg, Type: fromType(x.T)}
	case *mir.BinaryRV:
		left := fromOperand(x.Left)
		right := fromOperand(x.Right)
		return RValue{Kind: "binary", Binary: int(x.Op), Left: &left, Right: &right, Type: fromType(x.T)}
	case *mir.AggregateRV:
		return RValue{Kind: "aggregate", AggregateKind: int(x.Kind), Fields: fromOperands(x.Fields), Type: fromType(x.T), VariantIdx: x.VariantIdx, VariantTag: x.VariantTag}
	case *mir.DiscriminantRV:
		p := fromPlace(x.Place)
		return RValue{Kind: "discriminant", Place: &p, Type: fromType(x.T)}
	case *mir.LenRV:
		p := fromPlace(x.Place)
		return RValue{Kind: "len", Place: &p, Type: fromType(x.T)}
	case *mir.CastRV:
		arg := fromOperand(x.Arg)
		return RValue{Kind: "cast", CastKind: int(x.Kind), Arg: &arg, From: fromType(x.From), To: fromType(x.To)}
	case *mir.AddressOfRV:
		p := fromPlace(x.Place)
		return RValue{Kind: "address_of", Place: &p, Type: fromType(x.T)}
	case *mir.RefRV:
		p := fromPlace(x.Place)
		return RValue{Kind: "ref", Place: &p, Type: fromType(x.T)}
	case *mir.GlobalRefRV:
		return RValue{Kind: "global_ref", Name: x.Name, Type: fromType(x.T)}
	case *mir.NullaryRV:
		return RValue{Kind: "nullary", Nullary: int(x.Kind), Type: fromType(x.T)}
	default:
		return RValue{Kind: "invalid"}
	}
}

func toRValue(in RValue) (mir.RValue, error) {
	switch in.Kind {
	case "use":
		if in.Op == nil {
			return nil, fmt.Errorf("use rvalue missing operand")
		}
		op, err := toOperand(*in.Op)
		if err != nil {
			return nil, err
		}
		return &mir.UseRV{Op: op}, nil
	case "unary":
		if in.Arg == nil {
			return nil, fmt.Errorf("unary rvalue missing arg")
		}
		arg, err := toOperand(*in.Arg)
		if err != nil {
			return nil, err
		}
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.UnaryRV{Op: mir.UnaryOp(in.Unary), Arg: arg, T: t}, nil
	case "binary":
		if in.Left == nil || in.Right == nil {
			return nil, fmt.Errorf("binary rvalue missing operand")
		}
		left, err := toOperand(*in.Left)
		if err != nil {
			return nil, err
		}
		right, err := toOperand(*in.Right)
		if err != nil {
			return nil, err
		}
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.BinaryRV{Op: mir.BinaryOp(in.Binary), Left: left, Right: right, T: t}, nil
	case "aggregate":
		fields, err := toOperands(in.Fields)
		if err != nil {
			return nil, err
		}
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.AggregateRV{Kind: mir.AggregateKind(in.AggregateKind), Fields: fields, T: t, VariantIdx: in.VariantIdx, VariantTag: in.VariantTag}, nil
	case "discriminant":
		if in.Place == nil {
			return nil, fmt.Errorf("discriminant rvalue missing place")
		}
		place, err := toPlace(*in.Place)
		if err != nil {
			return nil, err
		}
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.DiscriminantRV{Place: place, T: t}, nil
	case "len":
		if in.Place == nil {
			return nil, fmt.Errorf("len rvalue missing place")
		}
		place, err := toPlace(*in.Place)
		if err != nil {
			return nil, err
		}
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.LenRV{Place: place, T: t}, nil
	case "cast":
		if in.Arg == nil {
			return nil, fmt.Errorf("cast rvalue missing arg")
		}
		arg, err := toOperand(*in.Arg)
		if err != nil {
			return nil, err
		}
		from, err := toType(in.From)
		if err != nil {
			return nil, err
		}
		to, err := toType(in.To)
		if err != nil {
			return nil, err
		}
		return &mir.CastRV{Kind: mir.CastKind(in.CastKind), Arg: arg, From: from, To: to}, nil
	case "address_of":
		if in.Place == nil {
			return nil, fmt.Errorf("address_of rvalue missing place")
		}
		place, err := toPlace(*in.Place)
		if err != nil {
			return nil, err
		}
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.AddressOfRV{Place: place, T: t}, nil
	case "ref":
		if in.Place == nil {
			return nil, fmt.Errorf("ref rvalue missing place")
		}
		place, err := toPlace(*in.Place)
		if err != nil {
			return nil, err
		}
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.RefRV{Place: place, T: t}, nil
	case "global_ref":
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.GlobalRefRV{Name: in.Name, T: t}, nil
	case "nullary":
		t, err := toType(in.Type)
		if err != nil {
			return nil, err
		}
		return &mir.NullaryRV{Kind: mir.NullaryRVKind(in.Nullary), T: t}, nil
	default:
		return nil, fmt.Errorf("unknown MIR JSON rvalue kind %q", in.Kind)
	}
}

func fromLayoutTable(l *mir.LayoutTable) LayoutTable {
	if l == nil {
		return LayoutTable{}
	}
	var out LayoutTable
	for _, key := range sortedKeys(l.Structs) {
		sl := l.Structs[key]
		if sl == nil {
			continue
		}
		out.Structs = append(out.Structs, StructLayout{
			Key:               key,
			Name:              sl.Name,
			Mangled:           sl.Mangled,
			Fields:            fromFields(sl.Fields),
			BuiltinSource:     sl.BuiltinSource,
			BuiltinSourceArgs: fromTypes(sl.BuiltinSourceArgs),
			Size:              sl.Size,
			Align:             sl.Align,
		})
	}
	for _, key := range sortedKeys(l.Enums) {
		en := l.Enums[key]
		if en == nil {
			continue
		}
		out.Enums = append(out.Enums, EnumLayout{
			Key:               key,
			Name:              en.Name,
			Mangled:           en.Mangled,
			BuiltinSource:     en.BuiltinSource,
			BuiltinSourceArgs: fromTypes(en.BuiltinSourceArgs),
			Discriminant:      fromType(en.Discriminant),
			Variants:          fromVariants(en.Variants),
		})
	}
	for _, key := range sortedKeys(l.Tuples) {
		tl := l.Tuples[key]
		if tl == nil {
			continue
		}
		out.Tuples = append(out.Tuples, TupleLayout{Key: tl.Key, Mangled: tl.Mangled, Fields: fromFields(tl.Fields)})
	}
	for _, key := range sortedKeys(l.Interfaces) {
		il := l.Interfaces[key]
		if il == nil {
			continue
		}
		out.Interfaces = append(out.Interfaces, InterfaceLayout{Key: key, Name: il.Name, Methods: fromInterfaceMethods(il.Methods), Impls: fromInterfaceImpls(il.Impls)})
	}
	return out
}

type keyed[T any] interface{ ~map[string]T }

func sortedKeys[M keyed[T], T any](m M) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func toLayoutTable(in LayoutTable) (*mir.LayoutTable, error) {
	out := mir.NewLayoutTable()
	for _, sl := range in.Structs {
		args, err := toTypes(sl.BuiltinSourceArgs)
		if err != nil {
			return nil, err
		}
		fields, err := toFields(sl.Fields)
		if err != nil {
			return nil, err
		}
		key := sl.Key
		if key == "" {
			key = sl.Name
		}
		out.Structs[key] = &mir.StructLayout{Name: sl.Name, Mangled: sl.Mangled, Fields: fields, BuiltinSource: sl.BuiltinSource, BuiltinSourceArgs: args, Size: sl.Size, Align: sl.Align}
	}
	for _, en := range in.Enums {
		args, err := toTypes(en.BuiltinSourceArgs)
		if err != nil {
			return nil, err
		}
		disc, err := toType(en.Discriminant)
		if err != nil {
			return nil, err
		}
		variants, err := toVariants(en.Variants)
		if err != nil {
			return nil, err
		}
		key := en.Key
		if key == "" {
			key = en.Name
		}
		out.Enums[key] = &mir.EnumLayout{Name: en.Name, Mangled: en.Mangled, BuiltinSource: en.BuiltinSource, BuiltinSourceArgs: args, Discriminant: disc, Variants: variants}
	}
	for _, tl := range in.Tuples {
		fields, err := toFields(tl.Fields)
		if err != nil {
			return nil, err
		}
		out.Tuples[tl.Key] = &mir.TupleLayout{Key: tl.Key, Mangled: tl.Mangled, Fields: fields}
	}
	for _, il := range in.Interfaces {
		key := il.Key
		if key == "" {
			key = il.Name
		}
		out.Interfaces[key] = &mir.InterfaceLayout{Name: il.Name, Methods: toInterfaceMethods(il.Methods), Impls: toInterfaceImpls(il.Impls)}
	}
	return out, nil
}

func fromFields(fields []mir.FieldLayout) []Field {
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		out = append(out, Field{Index: f.Index, Name: f.Name, Type: fromType(f.Type)})
	}
	return out
}

func toFields(in []Field) ([]mir.FieldLayout, error) {
	out := make([]mir.FieldLayout, 0, len(in))
	for _, f := range in {
		t, err := toType(f.Type)
		if err != nil {
			return nil, err
		}
		out = append(out, mir.FieldLayout{Index: f.Index, Name: f.Name, Type: t})
	}
	return out, nil
}

func fromVariants(in []mir.VariantLayout) []VariantLayout {
	out := make([]VariantLayout, 0, len(in))
	for _, v := range in {
		out = append(out, VariantLayout{Index: v.Index, Name: v.Name, Payload: fromFields(v.Payload)})
	}
	return out
}

func toVariants(in []VariantLayout) ([]mir.VariantLayout, error) {
	out := make([]mir.VariantLayout, 0, len(in))
	for _, v := range in {
		payload, err := toFields(v.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, mir.VariantLayout{Index: v.Index, Name: v.Name, Payload: payload})
	}
	return out, nil
}

func fromInterfaceMethods(in []mir.InterfaceMethod) []InterfaceMethod {
	out := make([]InterfaceMethod, 0, len(in))
	for _, m := range in {
		out = append(out, InterfaceMethod{Name: m.Name, Slot: m.Slot})
	}
	return out
}

func toInterfaceMethods(in []InterfaceMethod) []mir.InterfaceMethod {
	out := make([]mir.InterfaceMethod, 0, len(in))
	for _, m := range in {
		out = append(out, mir.InterfaceMethod{Name: m.Name, Slot: m.Slot})
	}
	return out
}

func fromInterfaceImpls(in []mir.InterfaceImpl) []InterfaceImpl {
	out := make([]InterfaceImpl, 0, len(in))
	for _, impl := range in {
		out = append(out, InterfaceImpl{ImplName: impl.ImplName, VtableSym: impl.VtableSym})
	}
	return out
}

func toInterfaceImpls(in []InterfaceImpl) []mir.InterfaceImpl {
	out := make([]mir.InterfaceImpl, 0, len(in))
	for _, impl := range in {
		out = append(out, mir.InterfaceImpl{ImplName: impl.ImplName, VtableSym: impl.VtableSym})
	}
	return out
}

func toGlobal(in Global, functions map[string]*mir.Function) (*mir.Global, error) {
	t, err := toType(in.Type)
	if err != nil {
		return nil, err
	}
	out := &mir.Global{Name: in.Name, Type: t, Mut: in.Mut, SpanV: toSpan(in.Span)}
	if in.HasInit || in.InitSymbol != "" {
		fn := functions[in.InitSymbol]
		if fn == nil {
			return nil, fmt.Errorf("global %q references missing init function %q", in.Name, in.InitSymbol)
		}
		out.Init = fn
	}
	return out, nil
}
