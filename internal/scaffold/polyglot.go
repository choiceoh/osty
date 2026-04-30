package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// PolyglotOptions configures the `osty scaffold polyglot` generator.
//
// The generated file is a small project-local rail around std.polyglot:
// it names the two languages, picks the strongest known preset when one
// exists, and exposes a doctorText helper that runs the static + executable
// integration checks from one place.
type PolyglotOptions struct {
	Name      string
	Primary   string
	Secondary string
	Protocol  string
}

func RenderPolyglot(opts PolyglotOptions) (string, *diag.Diagnostic) {
	if d := ValidateName(opts.Name); d != nil {
		return "", d
	}
	primary, primaryName, d := polyglotLanguageLiteral(defaultString(opts.Primary, "osty"))
	if d != nil {
		return "", d
	}
	secondary, secondaryName, d := polyglotLanguageLiteral(defaultString(opts.Secondary, "rust"))
	if d != nil {
		return "", d
	}
	if primary == secondary {
		return "", polyglotDiag("primary and secondary languages must differ",
			"pick two languages, for example --primary osty --secondary rust")
	}
	protocol, protocolName, d := polyglotProtocolLiteral(defaultString(opts.Protocol, "json"))
	if d != nil {
		return "", d
	}

	fnName := identForName(opts.Name)
	contractName := strings.ToLower(primaryName + "-" + secondaryName + "-" + fnName)
	call := polyglotPresetCall(primary, secondary, protocol, contractName)

	var b strings.Builder
	fmt.Fprintf(&b, "// %s_polyglot.osty - two-language boundary rail.\n", opts.Name)
	b.WriteString("//\n")
	fmt.Fprintf(&b, "// Primary: %s. Secondary: %s. Protocol: %s.\n", primaryName, secondaryName, protocolName)
	b.WriteString("// Keep this file near the repository root and make CI call doctorText\n")
	b.WriteString("// when the cross-language boundary must stay healthy.\n\n")
	b.WriteString("use std.polyglot\n\n")
	fmt.Fprintf(&b, "pub fn %sWorkspace(primaryRoot: String, secondaryRoot: String) -> Result<polyglot.Workspace, Error> {\n", fnName)
	b.WriteString(call)
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "pub fn %sDoctorText(primaryRoot: String, secondaryRoot: String) -> Result<String, Error> {\n", fnName)
	fmt.Fprintf(&b, "    let ws = %sWorkspace(primaryRoot, secondaryRoot)?\n", fnName)
	b.WriteString("    Ok(doctorRun(ws).render())\n")
	b.WriteString("}\n")
	return b.String(), nil
}

func WritePolyglot(dir string, opts PolyglotOptions) (string, *diag.Diagnostic) {
	src, d := RenderPolyglot(opts)
	if d != nil {
		return "", d
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", ioErr(dir, err)
	}
	base := strings.ToLower(identForName(opts.Name))
	path := filepath.Join(abs, base+"_polyglot.osty")
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

func polyglotPresetCall(primary, secondary, protocol, contractName string) string {
	if primary == "Osty" && secondary == "Rust" {
		return "    ostyRustCore(primaryRoot, secondaryRoot)\n"
	}
	if primary == "Osty" && secondary == "Go" {
		return fmt.Sprintf("    ostyGoService(primaryRoot, secondaryRoot, %s)\n", protocol)
	}
	if primary == "Osty" && secondary == "Python" {
		return "    ostyPythonWorker(primaryRoot, secondaryRoot)\n"
	}
	if primary == "Osty" && secondary == "JavaScript" {
		return "    ostyNodeTooling(primaryRoot, secondaryRoot)\n"
	}
	if primary == "Go" && secondary == "Rust" {
		return "    goRustCAbi(primaryRoot, secondaryRoot)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "    let primary = component(\"%s-primary\", %s, Gateway, primaryRoot)?\n", strings.ToLower(primary), primary)
	fmt.Fprintf(&b, "    let secondary = component(\"%s-secondary\", %s, Worker, secondaryRoot)?\n", strings.ToLower(secondary), secondary)
	fmt.Fprintf(&b, "    let plan = buildPlan(%s, secondaryRoot, [])?\n", secondary)
	fmt.Fprintf(&b, "    let boundary = linkedBoundary(%s, %s, %s, Process, plan)?\n", primary, secondary, protocol)
	fmt.Fprintf(&b, "    let spec = contractInvariant(stableContract(\"%s\", \"1.0.0\", %s, Process)?, \"Keep ownership, schema, tests, and artifacts explicit at this language boundary.\")\n", contractName, protocol)
	b.WriteString("    let linked = boundaryWithContractSpec(boundary, spec)?\n")
	fmt.Fprintf(&b, "    workspace(%s, %s, [primary, secondary], [linked])\n", primary, secondary)
	return b.String()
}

func polyglotLanguageLiteral(s string) (literal, name string, d *diag.Diagnostic) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "osty":
		return "Osty", "osty", nil
	case "go", "golang":
		return "Go", "go", nil
	case "rust", "rs":
		return "Rust", "rust", nil
	case "python", "python3", "py":
		return "Python", "python", nil
	case "javascript", "js", "node", "nodejs":
		return "JavaScript", "javascript", nil
	case "typescript", "ts":
		return "TypeScript", "typescript", nil
	case "c":
		return "C", "c", nil
	case "cpp", "c++", "cc", "cxx":
		return "Cpp", "cpp", nil
	case "shell", "sh", "bash":
		return "Shell", "shell", nil
	default:
		return "", "", polyglotDiag(fmt.Sprintf("unknown language %q", s),
			"known languages: osty, go, rust, python, javascript, typescript, c, cpp, shell")
	}
}

func polyglotProtocolLiteral(s string) (literal, name string, d *diag.Diagnostic) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "json":
		return "Json", "json", nil
	case "json-lines", "jsonlines", "jsonl":
		return "JsonLines", "json-lines", nil
	case "stdio", "stdio-text", "text":
		return "StdioText", "stdio-text", nil
	case "files", "file":
		return "Files", "files", nil
	case "args", "command-args", "commandargs":
		return "CommandArgs", "command-args", nil
	default:
		return "", "", polyglotDiag(fmt.Sprintf("unknown protocol %q", s),
			"known protocols: json, json-lines, stdio-text, files, command-args")
	}
}

func polyglotDiag(message, hint string) *diag.Diagnostic {
	return diag.New(diag.Error, message).
		Code(diag.CodeScaffoldInvalidName).
		PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
		Hint(hint).
		Build()
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
