// airepair_python_policy.go is the Go snapshot of
// toolchain/airepair_python.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// PythonScopeKind enumerates the three scope variants the
// rewriter threads through its stack. Encoded as int (with named
// constants instead of a dedicated type) so the host can mirror
// them as their iota analogue without a wire-format translation
// step.
//
// Osty: toolchain/airepair_python.osty:32
const (
	PythonScopeBlock    = 0
	PythonScopeMatch    = 1
	PythonScopeMatchArm = 2
)

// PythonColonHeader mirrors toolchain/airepair_python.osty's
// PythonColonHeader — the structured outcome of recognising a
// Python-style block header. Ok == false leaves all the string
// fields empty.
//
// Osty: toolchain/airepair_python.osty:49
type PythonColonHeader struct {
	Rewritten     string
	ChangeKind    string
	Message       string
	ScopeKind     int
	Elseish       bool
	RequiresMatch bool
	Ok            bool
}

// RewritePythonColonHeader classifies a single trimmed line as
// one of 12 recognised Python-block headers. Returns
// {Ok: false} when no rule matches; the host falls back to
// emitting the line verbatim.
//
// Osty: toolchain/airepair_python.osty:62
func RewritePythonColonHeader(trimmed string) PythonColonHeader {
	if strings.HasSuffix(trimmed, "->") {
		head := strings.TrimSpace(strings.TrimSuffix(trimmed, "->"))
		if head == "" {
			return airepairPyColonNone()
		}
		return PythonColonHeader{
			Rewritten:     head + " -> {",
			ChangeKind:    "python_arrow_arm_block",
			Message:       "wrap a multiline match arm body in Osty braces",
			ScopeKind:     PythonScopeMatchArm,
			Elseish:       false,
			RequiresMatch: true,
			Ok:            true,
		}
	}
	if !strings.HasSuffix(trimmed, ":") {
		return airepairPyColonNone()
	}
	head := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
	if head == "else" {
		return airepairPyColonBlock("else {", "python_else_block",
			"replace Python-style `else:` block with Osty braces", true)
	}
	if strings.HasPrefix(head, "elif ") {
		rest := strings.TrimPrefix(head, "elif ")
		return airepairPyColonBlock("else if "+rest+" {", "python_elif_block",
			"replace Python-style `elif:` block with Osty braces", true)
	}
	if strings.HasPrefix(head, "elseif ") {
		rest := strings.TrimPrefix(head, "elseif ")
		return airepairPyColonBlock("else if "+rest+" {", "python_elif_block",
			"replace alternate `elseif:` block with Osty braces", true)
	}
	if strings.HasPrefix(head, "else if ") {
		return airepairPyColonBlock(head+" {", "python_else_if_block",
			"replace Python-style `else if:` block with Osty braces", true)
	}
	if strings.HasPrefix(head, "if ") {
		return airepairPyColonBlock(head+" {", "python_if_block",
			"replace Python-style `if:` block with Osty braces", false)
	}
	if strings.HasPrefix(head, "for ") {
		return airepairPyColonBlock(head+" {", "python_for_block",
			"replace Python-style `for:` block with Osty braces", false)
	}
	if strings.HasPrefix(head, "while ") {
		rest := strings.TrimPrefix(head, "while ")
		return airepairPyColonBlock("for "+rest+" {", "python_while_block",
			"replace Python-style `while:` block with Osty braces", false)
	}
	if strings.HasPrefix(head, "match ") {
		return PythonColonHeader{
			Rewritten:     head + " {",
			ChangeKind:    "python_match_block",
			Message:       "replace Python-style `match:` block with Osty braces",
			ScopeKind:     PythonScopeMatch,
			Elseish:       false,
			RequiresMatch: false,
			Ok:            true,
		}
	}
	if strings.HasPrefix(head, "case ") {
		rest := strings.TrimPrefix(head, "case ")
		return PythonColonHeader{
			Rewritten:     rest + " -> {",
			ChangeKind:    "python_case_arm",
			Message:       "replace Python-style `case:` arm with Osty match syntax",
			ScopeKind:     PythonScopeMatchArm,
			Elseish:       false,
			RequiresMatch: true,
			Ok:            true,
		}
	}
	if head == "default" {
		return PythonColonHeader{
			Rewritten:     "_ -> {",
			ChangeKind:    "python_default_arm",
			Message:       "replace Python-style `default:` arm with Osty match syntax",
			ScopeKind:     PythonScopeMatchArm,
			Elseish:       false,
			RequiresMatch: true,
			Ok:            true,
		}
	}
	if strings.HasPrefix(head, "fn ") {
		return airepairPyColonBlock(head+" {", "python_fn_block",
			"replace Python-style function block with Osty braces", false)
	}
	if strings.HasPrefix(head, "pub fn ") {
		return airepairPyColonBlock(head+" {", "python_fn_block",
			"replace Python-style function block with Osty braces", false)
	}
	if strings.HasPrefix(head, "struct ") {
		return airepairPyColonBlock(head+" {", "python_struct_block",
			"replace Python-style struct block with Osty braces", false)
	}
	if strings.HasPrefix(head, "interface ") {
		return airepairPyColonBlock(head+" {", "python_interface_block",
			"replace Python-style interface block with Osty braces", false)
	}
	return airepairPyColonNone()
}

func airepairPyColonBlock(rewritten, changeKind, message string, elseish bool) PythonColonHeader {
	return PythonColonHeader{
		Rewritten:     rewritten,
		ChangeKind:    changeKind,
		Message:       message,
		ScopeKind:     PythonScopeBlock,
		Elseish:       elseish,
		RequiresMatch: false,
		Ok:            true,
	}
}

func airepairPyColonNone() PythonColonHeader {
	return PythonColonHeader{
		Rewritten:     "",
		ChangeKind:    "",
		Message:       "",
		ScopeKind:     PythonScopeBlock,
		Elseish:       false,
		RequiresMatch: false,
		Ok:            false,
	}
}
