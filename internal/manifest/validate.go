package manifest

import (
	"sort"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// ParseDiagnostics is the diagnostic-aware variant of Parse. It parses
// src and — when the first-error returning Parse fails — converts the
// error into a diag.Diagnostic with the best-matching E2xxx code and
// a source span pointing at the offending line.
//
// The Manifest pointer is non-nil iff Parse succeeded; callers that
// need to continue on syntax errors (e.g. for IDE reflows) can still
// inspect the diagnostics slice.
//
// path is the logical manifest path used for diagnostic rendering; it
// may be empty when parsing bytes that did not come from disk.
func ParseDiagnostics(src []byte, path string) (*Manifest, []*diag.Diagnostic) {
	m, err := Parse(src)
	if err == nil {
		return m, nil
	}
	d := errToDiagnostic(err)
	return m, []*diag.Diagnostic{d}
}

// Validate runs semantic checks over a parsed Manifest and returns
// diagnostic-encoded violations. The semantic decisions are generated
// from examples/selfhost-core/manifest_validation.osty; this Go layer
// only maps the host Manifest shape into the self-hosted data model and
// wraps self-hosted diagnostic specs in the toolchain diagnostic type.
func Validate(m *Manifest) []*diag.Diagnostic {
	if m == nil {
		return nil
	}
	specs := validateManifestDiagnostics(manifestSpecFromHost(m))
	out := make([]*diag.Diagnostic, 0, len(specs))
	for _, spec := range specs {
		if spec != nil {
			out = append(out, manifestDiagnosticFromSpec(spec))
		}
	}
	return out
}

func manifestDiagnosticFromSpec(spec *ManifestDiagnosticSpec) *diag.Diagnostic {
	severity := diag.Error
	switch spec.severity {
	case "warning":
		severity = diag.Warning
	case "note":
		severity = diag.Note
	}
	b := diag.New(severity, spec.message).
		Code(spec.code).
		PrimaryPos(token.Pos{Line: manifestHostLine(spec.line), Column: 1}, "")
	if spec.hint != "" {
		b.Hint(spec.hint)
	}
	return b.Build()
}

func manifestSpecFromHost(m *Manifest) *ManifestSpec {
	if m == nil {
		return manifestSpec(
			manifestPackageSpecAt(false, "", "", "", 1, 1, 1, 1),
			manifestWorkspaceSpec(false, nil),
			nil,
			nil,
			nil,
			nil,
		)
	}
	return manifestSpecWithDeps(
		manifestPackageSpecAt(
			m.HasPackage,
			m.Package.Name,
			m.Package.Version,
			m.Package.Edition,
			manifestHostLine(m.Package.TablePos.Line),
			manifestHostLine(m.Package.NamePos.Line),
			manifestHostLine(m.Package.VersionPos.Line),
			manifestHostLine(m.Package.EditionPos.Line),
		),
		manifestWorkspaceSpec(m.Workspace != nil, workspaceMembers(m.Workspace)),
		dependencySpecs(m.Dependencies, "dependencies"),
		dependencySpecs(m.DevDependencies, "dev-dependencies"),
		profileSpecs(m.Profiles),
		targetSpecs(m.Targets),
		append([]string(nil), m.DefaultFeatures...),
		featureSpecs(m.Features),
	)
}

func workspaceMembers(ws *Workspace) []string {
	if ws == nil {
		return nil
	}
	return append([]string(nil), ws.Members...)
}

func dependencySpecs(deps []Dependency, section string) []*ManifestDependencySpec {
	out := make([]*ManifestDependencySpec, 0, len(deps))
	for _, d := range deps {
		git := ""
		if d.Git != nil {
			git = d.Git.URL
		}
		out = append(out, manifestDependencySpecAt(
			d.Name,
			d.VersionReq,
			d.Path,
			git,
			section,
			manifestHostLine(d.Pos.Line),
		))
	}
	return out
}

func profileSpecs(profiles map[string]*Profile) []*ManifestProfileSpec {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ManifestProfileSpec, 0, len(names))
	for _, name := range names {
		p := profiles[name]
		if p == nil {
			continue
		}
		out = append(out, manifestProfileSpecAt(
			name,
			p.Inherits,
			p.HasOptLevel,
			p.OptLevel,
			manifestHostLine(p.Pos.Line),
		))
	}
	return out
}

func targetSpecs(targets []*Target) []*ManifestTargetSpec {
	out := make([]*ManifestTargetSpec, 0, len(targets))
	for _, t := range targets {
		if t != nil {
			out = append(out, manifestTargetSpecAt(t.Triple, manifestHostLine(t.Pos.Line)))
		}
	}
	return out
}

func featureSpecs(features map[string][]string) []*FeatureSpec {
	names := make([]string, 0, len(features))
	for name := range features {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*FeatureSpec, 0, len(names))
	for _, name := range names {
		out = append(out, featureSpec(name, append([]string(nil), features[name]...)))
	}
	return out
}

func manifestHostLine(line int) int {
	if line > 0 {
		return line
	}
	return 1
}

func errToDiagnostic(err error) *diag.Diagnostic {
	msg := err.Error()
	line, body := splitLinePrefix(msg)
	code := pickCodeFromMessage(body)
	return diag.New(diag.Error, body).
		Code(code).
		PrimaryPos(token.Pos{Line: line, Column: 1}, "").
		Build()
}

func splitLinePrefix(msg string) (int, string) {
	const prefix = "osty.toml:"
	if !strings.HasPrefix(msg, prefix) {
		return 0, msg
	}
	rest := msg[len(prefix):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return 0, msg
	}
	numPart := rest[:colon]
	body := strings.TrimSpace(rest[colon+1:])
	if n, err := strconv.Atoi(numPart); err == nil {
		return n, body
	}
	return 0, msg
}

func pickCodeFromMessage(body string) string {
	switch {
	case strings.Contains(body, "unknown key"):
		return diag.CodeManifestUnknownKey
	case strings.Contains(body, "must be a string"),
		strings.Contains(body, "must be a bool"),
		strings.Contains(body, "must be a table"),
		strings.Contains(body, "must be an array"):
		return diag.CodeManifestFieldType
	case strings.Contains(body, "unterminated"),
		strings.Contains(body, "newline inside"):
		return diag.CodeManifestUnterminated
	case strings.Contains(body, "duplicate key"):
		return diag.CodeManifestDuplicateKey
	case strings.Contains(body, "unknown escape"),
		strings.Contains(body, "bad \\u escape"):
		return diag.CodeManifestBadEscape
	case strings.Contains(body, "must specify one of"),
		strings.Contains(body, "multiple sources"),
		strings.Contains(body, "multiple git refs"):
		return diag.CodeManifestBadDepSpec
	}
	return diag.CodeManifestSyntax
}
