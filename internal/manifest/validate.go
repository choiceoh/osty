package manifest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/pkgmgr/semver"
	"github.com/osty/osty/internal/token"
)

// CurrentEdition is the Osty spec version that the toolchain targets.
// Manifests pinning a different edition are accepted only if the
// version appears in KnownEditions.
const CurrentEdition = "0.3"

// KnownEditions is the set of spec versions the toolchain understands.
// Adding a new entry is the last step of graduating a spec draft —
// older toolchains reject it with CodeManifestBadEdition so users
// upgrade deliberately.
var KnownEditions = map[string]bool{
	"0.3": true,
}

// packageNameRE mirrors scaffold.nameRE: a leading letter or
// underscore followed by letters, digits, underscores, or hyphens. The
// parser accepts any string, but Validate rejects names that fail this
// pattern so they cannot appear in user-visible use paths and directory
// names (spec §5.1 — package == directory).
var packageNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// semverRE matches the canonical MAJOR.MINOR.PATCH form with an
// optional pre-release (`-foo.bar`) and build metadata (`+sha.123`)
// segment — a strict subset of SemVer 2.0. Version requirement
// strings in [dependencies] are checked by a laxer rule (versionReqRE)
// because requirements commonly use operators (`^1.2`, `>=0.3, <1`).
var semverRE = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
		`(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?` +
		`(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

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
// diagnostic-encoded violations. Parse covers TOML syntax and structural
// well-formedness; Validate additionally enforces:
//
//   - [package] is present (unless [workspace] is) — duplicated from
//     Parse for defense-in-depth when callers construct a Manifest by
//     other means.
//   - package.name matches the identifier-ish regex.
//   - package.version is a strict semver x.y.z[-pre][+build].
//   - package.edition is one of KnownEditions.
//   - [workspace] has at least one member when present.
//
// Nothing here mutates m; the returned slice may be empty.
func Validate(m *Manifest) []*diag.Diagnostic {
	if m == nil {
		return nil
	}
	var out []*diag.Diagnostic
	add := func(code, msg string, pos token.Pos) {
		out = append(out, diag.New(diag.Error, msg).
			Code(code).
			PrimaryPos(pos, "").
			Build())
	}

	if !m.HasPackage && m.Workspace == nil {
		add(diag.CodeManifestMissingPackage,
			"manifest defines neither [package] nor [workspace]",
			token.Pos{Line: 1, Column: 1})
		return out
	}

	if m.HasPackage {
		// package.name identifier check.
		if m.Package.Name == "" {
			add(diag.CodeManifestMissingField,
				"[package] missing required field `name`",
				m.Package.TablePos)
		} else if !packageNameRE.MatchString(m.Package.Name) {
			add(diag.CodeManifestBadName,
				fmt.Sprintf("package.name `%s` is not a valid identifier (must match [A-Za-z_][A-Za-z0-9_-]*)",
					m.Package.Name),
				m.Package.NamePos)
		}
		// version must be strict semver.
		if m.Package.Version == "" {
			add(diag.CodeManifestMissingField,
				"[package] missing required field `version`",
				m.Package.TablePos)
		} else if !semverRE.MatchString(m.Package.Version) {
			add(diag.CodeManifestBadVersion,
				fmt.Sprintf("package.version `%s` is not a valid semver (want X.Y.Z)",
					m.Package.Version),
				m.Package.VersionPos)
		}
		// edition must be a known spec version; blank edition is a soft
		// miss (manifests that predate the field), not a validate-time
		// error — but we warn so users upgrade deliberately.
		if m.Package.Edition == "" {
			out = append(out, diag.New(diag.Warning,
				"[package] missing `edition`; defaulting to "+CurrentEdition).
				Code(diag.CodeManifestMissingField).
				PrimaryPos(m.Package.TablePos, "").
				Hint(`add edition = "`+CurrentEdition+`" to pin the spec version`).
				Build())
		} else if !KnownEditions[m.Package.Edition] {
			add(diag.CodeManifestBadEdition,
				fmt.Sprintf("unknown edition `%s` (known: %s)",
					m.Package.Edition, knownEditionsList()),
				m.Package.EditionPos)
		}
	}

	if m.Workspace != nil && len(m.Workspace.Members) == 0 {
		// Virtual workspaces need members; otherwise the root is
		// effectively empty.
		add(diag.CodeManifestWorkspaceEmpty,
			"[workspace] has no `members` — a workspace must declare at least one member package",
			token.Pos{Line: 1, Column: 1})
	}

	// Dependency version-requirement grammar. Registry deps (those
	// with a non-empty VersionReq and no Path/Git) must parse as
	// semver requirements. Path and git deps bypass this check
	// because they don't use the `^X.Y` operator form.
	for _, d := range m.Dependencies {
		validateDepVersionReq(d, "dependencies", &out)
	}
	for _, d := range m.DevDependencies {
		validateDepVersionReq(d, "dev-dependencies", &out)
	}

	return out
}

// validateDepVersionReq parses d.VersionReq through pkgmgr/semver.ParseReq
// when the dep is a registry dep; emits CodeManifestBadDepSpec otherwise.
// Path and git deps are ignored — their version comes from their source.
func validateDepVersionReq(d Dependency, section string, out *[]*diag.Diagnostic) {
	if d.Path != "" || d.Git != nil {
		return
	}
	if d.VersionReq == "" {
		return // missing source is handled by the parser (E2017)
	}
	if d.VersionReq == "*" {
		return // "*" is a valid wildcard, bypasses the strict parser
	}
	if _, err := semver.ParseReq(d.VersionReq); err != nil {
		*out = append(*out, diag.New(diag.Error,
			fmt.Sprintf("%s.%s has invalid version requirement %q: %v",
				section, d.Name, d.VersionReq, err)).
			Code(diag.CodeManifestBadDepSpec).
			PrimaryPos(d.Pos, "").
			Hint("examples: \"1.2.3\", \"^1.0\", \">=1.0, <2\", \"*\"").
			Build())
	}
}

// knownEditionsList returns the sorted edition list as a human-readable
// string for error messages. Small helper — the map isn't large enough
// to warrant a cache.
func knownEditionsList() string {
	var ks []string
	for k := range KnownEditions {
		ks = append(ks, k)
	}
	// Sort to keep messages stable across runs.
	for i := 1; i < len(ks); i++ {
		for j := i; j > 0 && ks[j-1] > ks[j]; j-- {
			ks[j-1], ks[j] = ks[j], ks[j-1]
		}
	}
	return strings.Join(ks, ", ")
}

// errToDiagnostic decodes one of the `osty.toml:LINE: message` errors
// produced by Parse into a structured Diagnostic. The code is picked
// from the message text using a small lookup table of phrase →
// code — imperfect but better than a blanket CodeManifestSyntax, and
// scoped to messages this package actually emits.
func errToDiagnostic(err error) *diag.Diagnostic {
	msg := err.Error()
	line, body := splitLinePrefix(msg)
	code := pickCodeFromMessage(body)
	return diag.New(diag.Error, body).
		Code(code).
		PrimaryPos(token.Pos{Line: line, Column: 1}, "").
		Build()
}

// splitLinePrefix parses the leading `osty.toml:LINE: body` and returns
// the line number and the trimmed body. If the prefix is missing or
// malformed the whole message is returned and line defaults to 0 —
// diag.Formatter handles line == 0 by skipping the source snippet.
func splitLinePrefix(msg string) (int, string) {
	const prefix = "osty.toml:"
	if !strings.HasPrefix(msg, prefix) {
		return 0, msg
	}
	rest := msg[len(prefix):]
	// Accept either `N: ...` or `N:C: ...` — our errors use the former
	// but be tolerant of future extensions.
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return 0, msg
	}
	numPart := rest[:colon]
	body := strings.TrimSpace(rest[colon+1:])
	// A plain number → line 1. Try parsing; fall back to 0 on failure.
	if n, err := strconv.Atoi(numPart); err == nil {
		return n, body
	}
	return 0, msg
}

// pickCodeFromMessage routes a Parse message to the most specific
// E2xxx code. The mapping is string-based rather than typed because
// Parse emits plain errors; when we re-home Parse onto diagnostics
// natively this function can go away.
func pickCodeFromMessage(body string) string {
	switch {
	case strings.Contains(body, "missing [package]"):
		return diag.CodeManifestMissingPackage
	case strings.Contains(body, "missing required key"):
		return diag.CodeManifestMissingField
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
