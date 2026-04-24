package api

// PackageCheckFile is the per-file input accepted by the structured
// self-host check / resolve adapters.
type PackageCheckFile struct {
	Source []byte `json:"source,omitempty"`
	Base   int    `json:"base,omitempty"`
	// Name is the display filename surfaced in diagnostic telemetry (typically
	// a basename like `user.osty`). Empty when unknown; the telemetry suffix
	// falls back to `@Lnn:Cnn` without a filename prefix in that case.
	Name string `json:"name,omitempty"`
	// Path is the owning source path when known. Package-mode diagnostics carry
	// this through the native checker boundary so multi-file callers do not
	// need to guess which file owned a given span.
	Path string `json:"path,omitempty"`
}

// PackageCheckGenericBound describes one `<T: Iface>` constraint on a
// generic decl surfaced from an imported package.
type PackageCheckGenericBound struct {
	TyParam       string `json:"tyParam,omitempty"`
	InterfaceType string `json:"interfaceType,omitempty"`
}

// PackageCheckFn describes one function or method declared in an
// imported package surface.
type PackageCheckFn struct {
	Name          string                     `json:"name,omitempty"`
	Owner         string                     `json:"owner,omitempty"`
	ReceiverType  string                     `json:"receiverType,omitempty"`
	ReturnType    string                     `json:"returnType,omitempty"`
	HasBody       bool                       `json:"hasBody,omitempty"`
	ParamNames    []string                   `json:"paramNames,omitempty"`
	ParamTypes    []string                   `json:"paramTypes,omitempty"`
	ParamDefaults []bool                     `json:"paramDefaults,omitempty"`
	Generics      []string                   `json:"generics,omitempty"`
	GenericBounds []PackageCheckGenericBound `json:"genericBounds,omitempty"`
}

// PackageCheckField describes one struct field exported by an imported
// package.
type PackageCheckField struct {
	Owner      string `json:"owner,omitempty"`
	Name       string `json:"name,omitempty"`
	TypeName   string `json:"typeName,omitempty"`
	Exported   bool   `json:"exported,omitempty"`
	HasDefault bool   `json:"hasDefault,omitempty"`
}

// PackageCheckVariant describes one enum variant exported by an
// imported package.
type PackageCheckVariant struct {
	Owner      string   `json:"owner,omitempty"`
	Name       string   `json:"name,omitempty"`
	FieldTypes []string `json:"fieldTypes,omitempty"`
	Generics   []string `json:"generics,omitempty"`
}

// PackageCheckAlias describes one `type Alias = Target` declaration
// exported by an imported package.
type PackageCheckAlias struct {
	Name     string   `json:"name,omitempty"`
	Target   string   `json:"target,omitempty"`
	Generics []string `json:"generics,omitempty"`
}

// PackageCheckType describes one struct / enum / interface declaration
// exported by an imported package.
type PackageCheckType struct {
	Name          string                     `json:"name,omitempty"`
	Kind          string                     `json:"kind,omitempty"`
	Generics      []string                   `json:"generics,omitempty"`
	GenericBounds []PackageCheckGenericBound `json:"genericBounds,omitempty"`
}

// PackageCheckInterfaceExt records one interface extension
// (`impl Iface for T`) coming from an imported package.
type PackageCheckInterfaceExt struct {
	Owner         string `json:"owner,omitempty"`
	InterfaceType string `json:"interfaceType,omitempty"`
}

// PackageCheckImport is the surface of one imported package: its
// exported fns, fields, variants, aliases, type decls, interface
// extensions, and the set of interface types it satisfies via
// structural `register_as_iface` declarations.
type PackageCheckImport struct {
	Alias           string                     `json:"alias,omitempty"`
	Functions       []PackageCheckFn           `json:"functions,omitempty"`
	Fields          []PackageCheckField        `json:"fields,omitempty"`
	Variants        []PackageCheckVariant      `json:"variants,omitempty"`
	Aliases         []PackageCheckAlias        `json:"aliases,omitempty"`
	TypeDecls       []PackageCheckType         `json:"typeDecls,omitempty"`
	InterfaceExts   []PackageCheckInterfaceExt `json:"interfaceExts,omitempty"`
	RegisterAsIface []string                   `json:"registerAsIface,omitempty"`
}

// PackageCheckInput batches one or more source files plus their
// imported package surfaces into the structured self-host check call.
type PackageCheckInput struct {
	Files   []PackageCheckFile   `json:"files,omitempty"`
	Imports []PackageCheckImport `json:"imports,omitempty"`
}

// PackageResolveFile is the per-file input shape accepted by the
// structured self-host resolve adapter. Aliases PackageCheckFile so
// the checker and resolver can share file shape.
type PackageResolveFile = PackageCheckFile

// PackageResolveInput batches one or more source files into a
// synthetic package so the self-host resolver can see one shared
// top-level namespace.
type PackageResolveInput struct {
	Files []PackageResolveFile `json:"files,omitempty"`
	// Cfg, when non-nil, activates the `#[cfg(key = "value")]`
	// pre-resolve filter per LANG_SPEC v0.5 §5 / G29. A nil Cfg leaves
	// every decl alive (cfg shape validation still emits E0405/E0739
	// either way).
	Cfg *CfgEnv `json:"cfg,omitempty"`
}

// CfgEnv carries the values that `#[cfg(...)]` predicates compare
// against. Mirrors toolchain/resolve.osty::SelfResolveCfgEnv and the
// internal/resolve Go-side CfgEnv — kept as a separate type so the
// selfhost package has no cycle with internal/resolve.
type CfgEnv struct {
	OS       string   `json:"os,omitempty"`
	Arch     string   `json:"arch,omitempty"`
	Target   string   `json:"target,omitempty"`
	Features []string `json:"features,omitempty"`
}

// UseEdge is one `use <target>` edge in the workspace import graph.
// Pos / EndPos are source offsets pointing at the use site; callers
// render them into line/column via their own source-map when emitting
// diagnostics.
type UseEdge struct {
	Target string
	Pos    int
	EndPos int
	File   string
}

// PackageUses groups every non-FFI use edge emitted by one package.
// Path is the dotted package key (same format as
// internal/resolve::UseKey).
type PackageUses struct {
	Path string
	Uses []UseEdge
}

// WorkspaceUses is the cross-package input accepted by
// DetectImportCycles. Callers should sort Packages lexicographically
// by Path so the diagnostic emission order stays deterministic — the
// detector respects the given order verbatim.
type WorkspaceUses struct {
	Packages []PackageUses
}

// CycleDiag is one cyclic-import diagnostic record carrying the edge
// that closed the cycle. Callers convert this to a rich
// diag.Diagnostic by rendering Pos / EndPos through their source map.
type CycleDiag struct {
	Importer string
	Target   string
	Pos      int
	EndPos   int
	File     string
	Message  string
}

// MemberLookupStatus is the outcome of a cross-package `pkg.member`
// lookup. OK means the caller should return the looked-up symbol
// with no diagnostic; Private and Missing both carry a rendered
// diagnostic in the accompanying MemberLookupResult.
type MemberLookupStatus int

const (
	MemberLookupOK      MemberLookupStatus = 0
	MemberLookupPrivate MemberLookupStatus = 1
	MemberLookupMissing MemberLookupStatus = 2
)

// MemberLookupResult carries the diagnostic wording decided by the
// selfhost `pkg.member` access policy. When Status is MemberLookupOK
// the string fields are all empty and the caller emits nothing.
// Otherwise Code is the E0507 (private) or E0508 (missing) diagnostic
// code and Message / Primary / Note / Hint are ready to hand to
// diag.New.
type MemberLookupResult struct {
	Status  MemberLookupStatus
	Code    string
	Message string
	Primary string
	Note    string
	Hint    string
}
