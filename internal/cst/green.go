package cst

import "fmt"

// GreenKind classifies a Green node or leaf. The enum covers three
// categories:
//
//  1. Structural node kinds mirroring ast.* (one per Osty grammar construct).
//  2. Token and trivia kinds — these are leaf kinds. See TriviaKind for the
//     trivia sub-classification carried by tokens' leading/trailing arrays.
//  3. Error kinds for recovery. Unlike panic-mode sync, the Green parser
//     wraps every salvaged token inside a GreenKind of GkError / GkErrorExtra
//     / GkErrorMissing so downstream consumers keep a complete tree.
//
// Kind values are stable integers so they can be serialized in snapshots and
// stored in the arena with compact representation.
type GreenKind int

const (
	// ---- Meta ----

	GkNone GreenKind = iota // unused sentinel
	GkToken                 // a terminal with text + trivia
	GkTrivia                // a trivia leaf (rarely used; trivia usually lives on tokens)

	// ---- File / entry points ----

	GkFile
	GkModule // placeholder for future multi-file module node

	// ---- Declarations ----

	GkFnDecl
	GkStructDecl
	GkEnumDecl
	GkInterfaceDecl
	GkTypeAlias
	GkUseDecl
	GkLetDecl
	GkPub // modifier wrapper when needed

	// ---- Declaration helpers ----

	GkParam
	GkParamList
	GkField
	GkFieldList
	GkVariant
	GkVariantList
	GkMatchArm
	GkMatchArmList
	GkAnnotation
	GkAnnotationArg
	GkGenericParam
	GkGenericParamList
	GkGenericBound

	// ---- Use-decl helpers ----

	GkUsePath
	GkUseAlias
	GkUseFFIBody

	// ---- Statements ----

	GkLetStmt
	GkReturnStmt
	GkBreakStmt
	GkContinueStmt
	GkDeferStmt
	GkForStmt
	GkAssignStmt
	GkChanSendStmt
	GkExprStmt
	GkFreeStmt
	GkBlock

	// ---- Expressions: literals ----

	GkIdent
	GkIntLit
	GkFloatLit
	GkStringLit
	GkRawStringLit
	GkBoolLit
	GkCharLit
	GkByteLit
	GkStringPart // one text segment of a STRING/RAWSTRING
	GkStringInterp // one {expr} interpolation

	// ---- Expressions: operators ----

	GkBinary
	GkUnary
	GkCall
	GkFieldAccess
	GkIndex
	GkRangeExcl // ..
	GkRangeIncl // ..=
	GkQuestion
	GkTurbofish
	GkIf
	GkIfLet
	GkElse
	GkMatch
	GkClosure
	GkList
	GkTuple
	GkMap
	GkMapEntry
	GkStructLit
	GkStructLitField
	GkParen

	// ---- Types ----

	GkNamedType
	GkTupleType
	GkFunctionType
	GkOptionalType
	GkListType
	GkMapType
	GkUnitType
	GkSelfType

	// ---- Patterns ----

	GkWildcardPat
	GkLiteralPat
	GkIdentPat
	GkTuplePat
	GkStructPat
	GkStructPatField
	GkVariantPat
	GkRangePat
	GkOrPat
	GkBindingPat

	// ---- Error recovery ----

	GkError        // a region the parser salvaged with at least one diagnostic
	GkErrorMissing // zero-width "expected X here" marker
	GkErrorExtra   // well-formed-looking tokens that were in the wrong place

	// ---- File-tail sentinel ----

	// GkEndOfFile is a zero-width leaf that carries file-tail trivia
	// (trailing whitespace/comments after the last real token). It is
	// structurally a leaf like GkErrorMissing but is NOT an error — it
	// exists so every source byte remains reachable from the tree even
	// when no real token owns the bytes.
	GkEndOfFile
)

// String returns a stable label for the kind — used in snapshots, test
// failure messages, and future golden files.
func (k GreenKind) String() string {
	if name, ok := greenKindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("GreenKind(%d)", int(k))
}

// IsError reports whether k is one of the error-recovery kinds.
func (k GreenKind) IsError() bool {
	switch k {
	case GkError, GkErrorMissing, GkErrorExtra:
		return true
	}
	return false
}

// IsLeaf reports whether k is a leaf (token or trivia); non-leaf kinds carry
// children.
func (k GreenKind) IsLeaf() bool {
	switch k {
	case GkToken, GkTrivia, GkErrorMissing, GkEndOfFile:
		return true
	}
	return false
}

// greenKindNames backs String(). Keep in sync with the const block above.
// Missing entries render as GreenKind(N) which is fine for forward-compat
// when new kinds are added before the table is updated.
var greenKindNames = map[GreenKind]string{
	GkNone:             "None",
	GkToken:            "Token",
	GkTrivia:           "Trivia",
	GkFile:             "File",
	GkModule:           "Module",
	GkFnDecl:           "FnDecl",
	GkStructDecl:       "StructDecl",
	GkEnumDecl:         "EnumDecl",
	GkInterfaceDecl:    "InterfaceDecl",
	GkTypeAlias:        "TypeAlias",
	GkUseDecl:          "UseDecl",
	GkLetDecl:          "LetDecl",
	GkPub:              "Pub",
	GkParam:            "Param",
	GkParamList:        "ParamList",
	GkField:            "Field",
	GkFieldList:        "FieldList",
	GkVariant:          "Variant",
	GkVariantList:      "VariantList",
	GkMatchArm:         "MatchArm",
	GkMatchArmList:     "MatchArmList",
	GkAnnotation:       "Annotation",
	GkAnnotationArg:    "AnnotationArg",
	GkGenericParam:     "GenericParam",
	GkGenericParamList: "GenericParamList",
	GkGenericBound:     "GenericBound",
	GkUsePath:          "UsePath",
	GkUseAlias:         "UseAlias",
	GkUseFFIBody:       "UseFFIBody",
	GkLetStmt:          "LetStmt",
	GkReturnStmt:       "ReturnStmt",
	GkBreakStmt:        "BreakStmt",
	GkContinueStmt:     "ContinueStmt",
	GkDeferStmt:        "DeferStmt",
	GkForStmt:          "ForStmt",
	GkAssignStmt:       "AssignStmt",
	GkChanSendStmt:     "ChanSendStmt",
	GkExprStmt:         "ExprStmt",
	GkFreeStmt:         "FreeStmt",
	GkBlock:            "Block",
	GkIdent:            "Ident",
	GkIntLit:           "IntLit",
	GkFloatLit:         "FloatLit",
	GkStringLit:        "StringLit",
	GkRawStringLit:     "RawStringLit",
	GkBoolLit:          "BoolLit",
	GkCharLit:          "CharLit",
	GkByteLit:          "ByteLit",
	GkStringPart:       "StringPart",
	GkStringInterp:     "StringInterp",
	GkBinary:           "Binary",
	GkUnary:            "Unary",
	GkCall:             "Call",
	GkFieldAccess:      "FieldAccess",
	GkIndex:            "Index",
	GkRangeExcl:        "RangeExcl",
	GkRangeIncl:        "RangeIncl",
	GkQuestion:         "Question",
	GkTurbofish:        "Turbofish",
	GkIf:               "If",
	GkIfLet:            "IfLet",
	GkElse:             "Else",
	GkMatch:            "Match",
	GkClosure:          "Closure",
	GkList:             "List",
	GkTuple:            "Tuple",
	GkMap:              "Map",
	GkMapEntry:         "MapEntry",
	GkStructLit:        "StructLit",
	GkStructLitField:   "StructLitField",
	GkParen:            "Paren",
	GkNamedType:        "NamedType",
	GkTupleType:        "TupleType",
	GkFunctionType:     "FunctionType",
	GkOptionalType:     "OptionalType",
	GkListType:         "ListType",
	GkMapType:          "MapType",
	GkUnitType:         "UnitType",
	GkSelfType:         "SelfType",
	GkWildcardPat:      "WildcardPat",
	GkLiteralPat:       "LiteralPat",
	GkIdentPat:         "IdentPat",
	GkTuplePat:         "TuplePat",
	GkStructPat:        "StructPat",
	GkStructPatField:   "StructPatField",
	GkVariantPat:       "VariantPat",
	GkRangePat:         "RangePat",
	GkOrPat:            "OrPat",
	GkBindingPat:       "BindingPat",
	GkError:            "Error",
	GkErrorMissing:     "ErrorMissing",
	GkErrorExtra:       "ErrorExtra",
	GkEndOfFile:        "EndOfFile",
}

// GreenToken is an immutable leaf in the Green tree. It carries its raw
// lexeme plus indices into the arena's trivia list for leading/trailing
// trivia. Absolute source positions are NOT stored — the Red wrapper
// computes them lazily from ancestor offsets and sibling widths.
//
// Widths are split into three fields so parents can sum children's total
// source extent (TotalWidth) while consumers that only care about the token
// text can still use Width:
//
//   TotalWidth = LeadingWidth + Width + TrailingWidth
//
// Structural sharing: two GreenTokens with identical fields dedupe to a
// single id in the arena, saving memory for common keyword/punctuation
// tokens.
type GreenToken struct {
	Kind           GreenKind // GkToken for real terminals; GkErrorMissing / GkEndOfFile for zero-width sentinels
	TokenKind      int       // underlying token.Kind, kept as int to avoid import cycles
	Text           string    // raw source text for this token (length == Width for real tokens)
	Width          int       // byte width of the token text only
	LeadingWidth   int       // byte width of the leading trivia run
	TrailingWidth  int       // byte width of the trailing trivia run
	LeadingTrivia  []int     // indices into GreenArena.Trivias
	TrailingTrivia []int     // indices into GreenArena.Trivias
}

// TotalWidth returns the full source extent this token occupies including
// its attached trivia.
func (t GreenToken) TotalWidth() int {
	return t.LeadingWidth + t.Width + t.TrailingWidth
}

// GreenNode is an immutable interior node. It has a kind, a total byte width
// (sum of children widths including their trivia), and a child list. Each
// child is either a node id or a token id, distinguished by GreenChildTag.
type GreenNode struct {
	Kind     GreenKind
	Width    int
	Children []GreenChild
}

// GreenChildTag discriminates the two kinds of children a GreenNode may have.
type GreenChildTag int

const (
	GctNode GreenChildTag = iota
	GctToken
)

// GreenChild is a tagged id pointing into GreenArena.Nodes or .Tokens.
type GreenChild struct {
	Tag GreenChildTag
	ID  int
}
