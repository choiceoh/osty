package scaffold

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// SchemaOptions configures the JSON-sample → Osty struct generator.
//
// The generator parses one JSON document and emits a `pub struct`
// whose field types are inferred from the sample's values. Nested
// objects become nested types named `<Parent><Field>`; arrays become
// `List<T>` with T inferred from the first element. Unknown / mixed
// element types fall back to `Json` (an opaque placeholder) so the
// generated source still parses.
//
// This is a starter, not a full JSON-Schema compiler — the goal is to
// turn an example payload into a struct skeleton the user can refine,
// not to encode every JSON-Schema constraint.
type SchemaOptions struct {
	// Name is the Osty type name for the root struct (e.g. "User").
	// Field types from nested objects are derived by prefixing this
	// name. Validated with ValidateName before any work happens.
	Name string
	// Sample is the raw JSON bytes to parse. Top-level value must be
	// a JSON object — arrays / scalars at the top level are rejected
	// with a targeted diagnostic so the user gets a clear hint.
	Sample []byte
}

// RenderSchema parses opts.Sample and returns the generated Osty
// source plus a diagnostic. The source contains the root struct and
// any nested struct types it transitively requires, in deterministic
// order (root first, then nested types alphabetised by name).
func RenderSchema(opts SchemaOptions) (string, *diag.Diagnostic) {
	if d := ValidateName(opts.Name); d != nil {
		return "", d
	}
	if len(opts.Sample) == 0 {
		return "", schemaErr("empty JSON sample",
			"pass a non-empty JSON object via --from FILE")
	}
	var root any
	if err := json.Unmarshal(opts.Sample, &root); err != nil {
		return "", schemaErr(fmt.Sprintf("invalid JSON sample: %v", err),
			"the file must contain a single JSON object")
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return "", schemaErr("top-level JSON value is not an object",
			"wrap your sample in `{ ... }` so it can become a struct")
	}
	rootName := titleCase(identForName(opts.Name))

	emitted := map[string]string{}
	order := []string{}
	emitStruct(rootName, obj, emitted, &order)

	var b strings.Builder
	fmt.Fprintf(&b, "// %s.osty — generated from a JSON sample.\n", strings.ToLower(rootName))
	b.WriteString("//\n")
	b.WriteString("// Field types are inferred from the example payload — refine\n")
	b.WriteString("// them by hand where the JSON is more permissive than the\n")
	b.WriteString("// inferred Osty type (e.g. an integer field that may also be\n")
	b.WriteString("// `null`, or a numeric field that should really be `Float`).\n\n")
	for i, name := range order {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(emitted[name])
	}
	return b.String(), nil
}

// WriteSchema writes the rendered struct to `<dir>/<name>.osty`.
func WriteSchema(dir string, opts SchemaOptions) (string, *diag.Diagnostic) {
	src, d := RenderSchema(opts)
	if d != nil {
		return "", d
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", ioErr(dir, err)
	}
	base := strings.ToLower(identForName(opts.Name))
	path := filepath.Join(abs, base+".osty")
	if _, err := os.Stat(path); err == nil {
		return "", existsDiag(path)
	} else if !os.IsNotExist(err) {
		return "", ioErr(path, err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return "", ioErr(path, err)
	}
	return path, nil
}

// emitStruct renders one struct definition and recurses into nested
// objects. Definitions are accumulated in `emitted` (keyed by type
// name) and `order` (preserves insertion order: root first, nested
// types sorted alphabetically below it). Recursion guards against
// re-emitting a type already produced for the same path.
func emitStruct(typeName string, obj map[string]any, emitted map[string]string, order *[]string) {
	if _, exists := emitted[typeName]; exists {
		return
	}
	// Reserve the slot first so cyclical-ish samples (rare in JSON
	// but possible if the user runs the generator on its own output)
	// don't recurse forever.
	emitted[typeName] = ""
	*order = append(*order, typeName)

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "pub struct %s {\n", typeName)
	type pendingNested struct {
		typeName string
		obj      map[string]any
	}
	var pending []pendingNested
	for _, k := range keys {
		fieldName := safeFieldName(k)
		fieldType, nested := inferType(typeName, k, obj[k])
		if nested != nil {
			pending = append(pending, pendingNested{typeName: nested.typeName, obj: nested.obj})
		}
		if fieldName != k {
			// Preserve the original wire key when the Osty
			// identifier had to be sanitised — users should not lose
			// information just because the JSON used kebab-case.
			fmt.Fprintf(&b, "    #[json(key = %q)]\n", k)
		}
		fmt.Fprintf(&b, "    pub %s: %s,\n", fieldName, fieldType)
	}
	b.WriteString("}\n")
	emitted[typeName] = b.String()

	// Defer nested emission until after the parent is registered, so
	// `order` reads top-down (parent before children) which matches
	// how a human would skim the file.
	for _, n := range pending {
		emitStruct(n.typeName, n.obj, emitted, order)
	}
}

type nestedRef struct {
	typeName string
	obj      map[string]any
}

// inferType maps one JSON value to an Osty type and (when the value
// is a nested object) returns the recursive struct that needs to be
// emitted alongside the parent.
//
// Number handling: JSON numbers are unityped, but Osty distinguishes
// Int from Float. We treat values that round-trip through int64 as
// `Int` and everything else as `Float`. `null` becomes `Json?` —
// callers usually want to refine to a real `T?`, but we can't infer
// `T` from a `null` alone.
func inferType(parent, key string, v any) (string, *nestedRef) {
	switch x := v.(type) {
	case nil:
		return "Json?", nil
	case bool:
		return "Bool", nil
	case string:
		return "String", nil
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) && math.Abs(x) < (1<<53) {
			return "Int", nil
		}
		return "Float", nil
	case []any:
		if len(x) == 0 {
			return "List<Json>", nil
		}
		// Probe the first element for the element type. Mixed-type
		// arrays would need a sum type we can't synthesise; fall
		// back to `List<Json>` if the first element is itself an
		// array of unknown shape.
		elemType, nested := inferType(parent, key+"Item", x[0])
		return "List<" + elemType + ">", nested
	case map[string]any:
		nestedTypeName := parent + titleCase(safeFieldName(key))
		return nestedTypeName, &nestedRef{typeName: nestedTypeName, obj: x}
	default:
		return "Json", nil
	}
}

// safeFieldName turns a JSON key into a valid Osty identifier. Keys
// containing characters Osty doesn't permit in identifiers (hyphens,
// dots, spaces, leading digits) are converted to camelCase with the
// invalid runes acting as word boundaries. Empty results fall back
// to "field" so the struct still parses; the original key is
// preserved via the `#[json(key = ...)]` annotation upstream.
func safeFieldName(k string) string {
	if k == "" {
		return "field"
	}
	var b strings.Builder
	upper := false
	for i, r := range k {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 {
			if isLetter {
				if upper {
					b.WriteRune(toUpper(r))
				} else {
					b.WriteRune(toLower(r))
				}
				upper = false
				continue
			}
			// Leading non-letter: prefix with `_` so the result is
			// still a valid identifier.
			b.WriteByte('_')
			if isDigit {
				b.WriteRune(r)
			}
			continue
		}
		if isLetter || isDigit {
			if upper {
				b.WriteRune(toUpper(r))
				upper = false
			} else {
				b.WriteRune(r)
			}
			continue
		}
		// Word boundary character — uppercase the next letter.
		upper = true
	}
	if b.Len() == 0 {
		return "field"
	}
	return b.String()
}

func toUpper(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 32
	}
	return r
}

func toLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

func schemaErr(msg, hint string) *diag.Diagnostic {
	return diag.New(diag.Error, msg).
		Code(diag.CodeScaffoldWriteError).
		PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
		Hint(hint).
		Build()
}
