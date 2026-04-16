// monomorph_snapshot.go snapshots the Osty-authored generic
// monomorphization helper surface (toolchain/monomorph.osty) into the
// IR package. The Osty sources remain the long-term owners of the
// encoding table and symbol-assembly rules; this file is a
// hand-translated seed so the Go bootstrap has something to call
// before the toolchain is itself re-translated.
//
// Keep this file in lockstep with toolchain/monomorph.osty.
// Phase 2 additions: MonomorphTypeRequest, MonomorphMangleType,
// MonomorphMangleTypeName, MonomorphUserTemplateNested, and
// MonomorphTypeDedupeKey handle the nominal-type symbol track
// (`_ZTS…`) used for generic struct/enum specializations.

package ir

import (
	"fmt"
)

// Osty: toolchain/monomorph.osty:14:5
type MonomorphRequest struct {
	pkg            string
	fnName         string
	typeArgCodes   []string
	paramTypeCodes []string
	returnTypeCode string
}

// Osty: toolchain/monomorph.osty:25:5
type MonomorphMangled struct {
	symbol string
}

// Osty: toolchain/monomorph.osty (Phase 2, MonomorphTypeRequest)
type MonomorphTypeRequest struct {
	pkg          string
	typeName     string
	typeArgCodes []string
}

// Symbol returns the mangled LLVM symbol string. Exposed so the IR
// monomorph engine (internal/ir) can consume Osty's output without
// reaching into an unexported field.
func (m *MonomorphMangled) Symbol() string {
	if m == nil {
		return ""
	}
	return m.symbol
}

// NewMonomorphRequest constructs a request with the shape Osty's
// pure helpers expect. Exposed for cross-package callers; Osty itself
// gets a struct literal.
func NewMonomorphRequest(pkg, fnName string, typeArgCodes, paramTypeCodes []string, returnTypeCode string) *MonomorphRequest {
	return &MonomorphRequest{
		pkg:            pkg,
		fnName:         fnName,
		typeArgCodes:   append([]string(nil), typeArgCodes...),
		paramTypeCodes: append([]string(nil), paramTypeCodes...),
		returnTypeCode: returnTypeCode,
	}
}

// NewMonomorphTypeRequest is the struct/enum counterpart of
// NewMonomorphRequest. Defensively copies typeArgCodes so callers can
// reuse the caller's slice without worrying about aliasing.
func NewMonomorphTypeRequest(pkg, typeName string, typeArgCodes []string) *MonomorphTypeRequest {
	return &MonomorphTypeRequest{
		pkg:          pkg,
		typeName:     typeName,
		typeArgCodes: append([]string(nil), typeArgCodes...),
	}
}

// Osty: toolchain/monomorph.osty:37:5
func MonomorphPrimCode(name string) string {
	if name == "Int" {
		return "l"
	}
	if name == "Int8" {
		return "a"
	}
	if name == "Int16" {
		return "s"
	}
	if name == "Int32" {
		return "i"
	}
	if name == "Int64" {
		return "x"
	}
	if name == "UInt8" {
		return "h"
	}
	if name == "UInt16" {
		return "t"
	}
	if name == "UInt32" {
		return "j"
	}
	if name == "UInt64" {
		return "y"
	}
	if name == "Byte" {
		return "h"
	}
	if name == "Float" {
		return "d"
	}
	if name == "Float32" {
		return "f"
	}
	if name == "Float64" {
		return "d"
	}
	if name == "Bool" {
		return "b"
	}
	if name == "Char" {
		return "w"
	}
	if name == "Unit" {
		return "v"
	}
	return ""
}

// Osty: toolchain/monomorph.osty:81:5
func MonomorphIsPrim(name string) bool {
	return MonomorphPrimCode(name) != ""
}

// Osty: toolchain/monomorph.osty:87:5
func MonomorphLengthPrefix(text string) string {
	return fmt.Sprintf("%d%s", monomorphByteLength(text), text)
}

// Osty: toolchain/monomorph.osty:94:5
func MonomorphNestedName(components []string) string {
	body := ""
	for _, c := range components {
		body += c
	}
	return "N" + body + "E"
}

// Osty: toolchain/monomorph.osty:105:5
func MonomorphBuiltinNested(name string) string {
	head := MonomorphLengthPrefix("osty")
	tail := MonomorphLengthPrefix(name)
	return "N" + head + tail + "E"
}

// Osty: toolchain/monomorph.osty:114:5
func MonomorphBuiltinTemplate(name string, argCodes string) string {
	head := MonomorphLengthPrefix("osty")
	tail := MonomorphLengthPrefix(name)
	return "N" + head + tail + "I" + argCodes + "EE"
}

// Osty: toolchain/monomorph.osty:124:5
func MonomorphUserNested(pkg, name string) string {
	head := MonomorphLengthPrefix(pkg)
	tail := MonomorphLengthPrefix(name)
	return "N" + head + tail + "E"
}

// Osty: toolchain/monomorph.osty (Phase 2, monomorphUserTemplateNested)
func MonomorphUserTemplateNested(pkg, name, argCodes string) string {
	head := MonomorphLengthPrefix(pkg)
	tail := MonomorphLengthPrefix(name)
	return "N" + head + tail + "I" + argCodes + "EE"
}

// Osty: toolchain/monomorph.osty:133:5
func MonomorphTemplateArgs(codes []string) string {
	if len(codes) == 0 {
		return ""
	}
	body := ""
	for _, c := range codes {
		body += c
	}
	return "I" + body + "E"
}

// Osty: toolchain/monomorph.osty:145:5
func MonomorphParamList(codes []string) string {
	body := ""
	for _, c := range codes {
		body += c
	}
	return body
}

// Osty: toolchain/monomorph.osty:158:5
func MonomorphMangleFn(req *MonomorphRequest) *MonomorphMangled {
	if req == nil {
		return &MonomorphMangled{}
	}
	nameEncoded := MonomorphMangleFnName(req.pkg, req.fnName)
	targs := MonomorphTemplateArgs(req.typeArgCodes)
	params := MonomorphParamList(req.paramTypeCodes)
	return &MonomorphMangled{symbol: "_Z" + nameEncoded + targs + params}
}

// Osty: toolchain/monomorph.osty:168:5
func MonomorphMangleFnName(pkg, name string) string {
	if pkg == "" || pkg == "main" {
		return MonomorphLengthPrefix(name)
	}
	pkgPart := MonomorphLengthPrefix(pkg)
	namePart := MonomorphLengthPrefix(name)
	return "N" + pkgPart + namePart + "E"
}

// Osty: toolchain/monomorph.osty (Phase 2, monomorphMangleType)
func MonomorphMangleType(req *MonomorphTypeRequest) *MonomorphMangled {
	if req == nil {
		return &MonomorphMangled{}
	}
	body := MonomorphMangleTypeName(req.pkg, req.typeName, req.typeArgCodes)
	return &MonomorphMangled{symbol: "_ZTS" + body}
}

// Osty: toolchain/monomorph.osty (Phase 2, monomorphMangleTypeName)
func MonomorphMangleTypeName(pkg, name string, typeArgCodes []string) string {
	// Type symbols always carry a package segment so demanglers can
	// print a qualified name; fall back to "main" for script files that
	// leave the package blank.
	actualPkg := pkg
	if actualPkg == "" {
		actualPkg = "main"
	}
	pkgPart := MonomorphLengthPrefix(actualPkg)
	namePart := MonomorphLengthPrefix(name)
	args := ""
	for _, c := range typeArgCodes {
		args += c
	}
	return "N" + pkgPart + namePart + "I" + args + "EE"
}

// Osty: toolchain/monomorph.osty:182:5
func MonomorphDedupeKey(fnName, pkg string, typeArgCodes []string) string {
	// Using the ASCII Unit Separator (0x1f) as a delimiter — it cannot
	// appear in Osty source identifiers, so the concatenation stays
	// injective over the inputs.
	sep := "\x1f"
	key := pkg + sep + fnName
	for _, c := range typeArgCodes {
		key += sep + c
	}
	return key
}

// Osty: toolchain/monomorph.osty (Phase 2, monomorphTypeDedupeKey)
//
// Separate namespace from MonomorphDedupeKey so a generic function and
// a generic struct sharing a source name cannot collide in the engine's
// seen-map.
func MonomorphTypeDedupeKey(typeName, pkg string, typeArgCodes []string) string {
	sep := "\x1f"
	key := pkg + sep + "type" + sep + typeName
	for _, c := range typeArgCodes {
		key += sep + c
	}
	return key
}

// Osty: toolchain/monomorph.osty:193:5
func MonomorphShouldInstantiate(typeArgsLen, fnGenericsLen int) bool {
	return typeArgsLen > 0 && typeArgsLen == fnGenericsLen
}

// Osty: toolchain/monomorph.osty (Phase 4, MonomorphMethodRequest)
type MonomorphMethodRequest struct {
	ownerMangled string
	methodName   string
	typeArgCodes []string
}

// NewMonomorphMethodRequest builds a Phase 4 method-specialization
// request. ownerMangled must already be the caller's final owner
// symbol (e.g. `_ZTSN4main3VecIlEE` or a non-generic source name like
// `Box`); the LLVM backend appends the returned method-local name via
// `llvmMethodIRName`.
func NewMonomorphMethodRequest(ownerMangled, methodName string, typeArgCodes []string) *MonomorphMethodRequest {
	return &MonomorphMethodRequest{
		ownerMangled: ownerMangled,
		methodName:   methodName,
		typeArgCodes: append([]string(nil), typeArgCodes...),
	}
}

// Osty: toolchain/monomorph.osty (Phase 4, monomorphMangleMethodName)
func MonomorphMangleMethodName(methodName string, typeArgCodes []string) string {
	if len(typeArgCodes) == 0 {
		return methodName
	}
	return methodName + "_Z" + MonomorphTemplateArgs(typeArgCodes)
}

// Osty: toolchain/monomorph.osty (Phase 4, monomorphMangleMethod)
func MonomorphMangleMethod(req *MonomorphMethodRequest) *MonomorphMangled {
	if req == nil {
		return &MonomorphMangled{}
	}
	return &MonomorphMangled{symbol: MonomorphMangleMethodName(req.methodName, req.typeArgCodes)}
}

// Osty: toolchain/monomorph.osty (Phase 4, monomorphMethodDedupeKey)
func MonomorphMethodDedupeKey(ownerMangled, methodName string, typeArgCodes []string) string {
	sep := "\x1f"
	key := ownerMangled + sep + "method" + sep + methodName
	for _, c := range typeArgCodes {
		key += sep + c
	}
	return key
}

// monomorphByteLength mirrors std.strings.len's UTF-8 byte counting so
// the length prefix produced by MonomorphLengthPrefix matches what an
// Itanium demangler expects. Kept unexported because the public
// surface is deliberately the Osty-authored API.
func monomorphByteLength(s string) int {
	return len(s)
}
