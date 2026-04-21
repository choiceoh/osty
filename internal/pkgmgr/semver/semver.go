// Package semver implements the subset of Semantic Versioning 2.0.0
// that osty's package manager needs.
//
// Supported version forms (ParseVersion):
//
//	1.2.3
//	1.2.3-alpha
//	1.2.3-alpha.1
//	1.2.3+sha.abcdef
//	1.2.3-alpha.1+sha.abcdef
//
// Supported requirement forms (ParseReq):
//
//	1.2.3          exact
//	=1.2.3         exact (explicit)
//	>=1.2.3        inclusive lower bound
//	<=1.2.3        inclusive upper bound
//	>1.2.3         exclusive lower bound
//	<1.2.3         exclusive upper bound
//	^1.2.3         caret: >=1.2.3 <2.0.0  (major-stable)
//	~1.2.3         tilde: >=1.2.3 <1.3.0  (minor-stable)
//	1.2.*          wildcard: >=1.2.0 <1.3.0
//	1.*            wildcard: >=1.0.0 <2.0.0
//	*              any
//	>=1.0 <2.0     space-separated conjunction
//
// Pre-release tagged versions (1.2.3-alpha) are considered unstable and
// are excluded from caret / tilde / wildcard ranges unless the range
// itself is tagged — the SemVer 2.0.0 recommendation.
//
// The implementation is deliberately simple: String→Version and
// Version→String roundtrips are byte-exact; requirements are compiled
// into a slice of clauses that `Match` tests in order.
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Zero-value Version is
// invalid; use ParseVersion to construct one.
type Version struct {
	Major, Minor, Patch uint64
	// Pre is the dot-separated pre-release identifier list, e.g.
	// ["alpha", "1"]. Empty for a stable release.
	Pre []string
	// Build is the dot-separated build metadata, e.g.
	// ["sha", "abcdef"]. Does NOT participate in precedence.
	Build []string
}

// String renders v in canonical 2.0.0 form.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if len(v.Build) > 0 {
		s += "+" + strings.Join(v.Build, ".")
	}
	return s
}

// IsPrerelease reports whether v has a pre-release tag. Unstable
// versions are handled specially by range matching per SemVer 2.0.0
// §11.4.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// ParseVersion parses s into a Version. Returns an error that names
// the offending portion of the string.
func ParseVersion(s string) (Version, error) {
	if s == "" {
		return Version{}, fmt.Errorf("empty version")
	}
	// Strip leading `v` per common convention (`v1.2.3`).
	if s[0] == 'v' {
		s = s[1:]
	}
	// Split off +build first.
	core := s
	var build []string
	if plus := strings.IndexByte(s, '+'); plus >= 0 {
		core = s[:plus]
		build = strings.Split(s[plus+1:], ".")
		for _, b := range build {
			if !isBuildIdent(b) {
				return Version{}, fmt.Errorf("invalid build metadata %q", b)
			}
		}
	}
	// Then -pre.
	versionPart := core
	var pre []string
	if dash := strings.IndexByte(core, '-'); dash >= 0 {
		versionPart = core[:dash]
		pre = strings.Split(core[dash+1:], ".")
		for _, p := range pre {
			if !isPreIdent(p) {
				return Version{}, fmt.Errorf("invalid pre-release identifier %q", p)
			}
		}
	}
	// The core 3-tuple must be digits-only; no wildcards.
	parts := strings.Split(versionPart, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q must have major.minor.patch", s)
	}
	nums := [3]uint64{}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || (len(p) > 1 && p[0] == '0') {
			return Version{}, fmt.Errorf("invalid %s %q", majorMinorPatch(i), p)
		}
		nums[i] = n
	}
	return Version{
		Major: nums[0],
		Minor: nums[1],
		Patch: nums[2],
		Pre:   pre,
		Build: build,
	}, nil
}

func majorMinorPatch(i int) string {
	switch i {
	case 0:
		return "major"
	case 1:
		return "minor"
	}
	return "patch"
}

// isPreIdent tests the §9 grammar for a pre-release identifier: a
// numeric identifier (no leading zeros except "0") OR an
// alphanumeric identifier (at least one letter/hyphen).
func isPreIdent(s string) bool {
	if s == "" {
		return false
	}
	// Numeric?
	allDigits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		if len(s) > 1 && s[0] == '0' {
			return false
		}
		return true
	}
	// Alphanumeric / hyphen
	for _, r := range s {
		if !(r >= '0' && r <= '9') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= 'a' && r <= 'z') &&
			r != '-' {
			return false
		}
	}
	return true
}

// isBuildIdent tests the §10 grammar for a build identifier: any
// non-empty [0-9A-Za-z-]+ sequence. Leading zeros ARE allowed.
func isBuildIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= 'a' && r <= 'z') &&
			r != '-' {
			return false
		}
	}
	return true
}

// Compare returns -1, 0, +1 using SemVer precedence (§11). Build
// metadata is ignored.
func Compare(a, b Version) int {
	if a.Major != b.Major {
		return signU(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return signU(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return signU(a.Patch, b.Patch)
	}
	// Pre-release: absence of a pre sorts higher than presence.
	if len(a.Pre) == 0 && len(b.Pre) == 0 {
		return 0
	}
	if len(a.Pre) == 0 {
		return 1
	}
	if len(b.Pre) == 0 {
		return -1
	}
	// Compare identifier-by-identifier per §11.4.
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		ai, bi := a.Pre[i], b.Pre[i]
		an, aok := parseUintMaybe(ai)
		bn, bok := parseUintMaybe(bi)
		switch {
		case aok && bok:
			if an != bn {
				return signU(an, bn)
			}
		case aok && !bok:
			// Numeric < alpha per spec.
			return -1
		case !aok && bok:
			return 1
		default:
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		}
	}
	// Longer wins on tie.
	if len(a.Pre) != len(b.Pre) {
		if len(a.Pre) < len(b.Pre) {
			return -1
		}
		return 1
	}
	return 0
}

func parseUintMaybe(s string) (uint64, bool) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func signU(a, b uint64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Less, Equal, and LessEq are convenience wrappers on Compare.
func Less(a, b Version) bool   { return Compare(a, b) < 0 }
func Equal(a, b Version) bool  { return Compare(a, b) == 0 }
func LessEq(a, b Version) bool { return Compare(a, b) <= 0 }

// ---- Requirement ----

// Req is a conjunction of constraints produced by ParseReq. The zero
// Req (empty Clauses) matches every version.
type Req struct {
	// Raw holds the original requirement text for diagnostics and
	// lockfile output.
	Raw string
	// Clauses are ANDed together: a candidate version must satisfy
	// every clause to match.
	Clauses []clause
	// allowPre reports whether a clause in this requirement explicitly
	// named a pre-release version. When false, pre-release candidates
	// are excluded from the match even if their core version fits.
	allowPre bool
}

type op int

const (
	opEQ op = iota
	opGE
	opGT
	opLE
	opLT
)

type clause struct {
	Op op
	V  Version
}

// ParseReq parses a requirement string like "^1.2.3", ">=1 <2", or
// "1.*". Returns an error on unrecognized syntax; a successful parse
// yields a Req whose Clauses are always in normalized bounds form.
func ParseReq(s string) (Req, error) {
	raw := s
	s = strings.TrimSpace(s)
	if s == "" {
		return Req{}, fmt.Errorf("empty version requirement")
	}
	if s == "*" {
		return Req{Raw: raw}, nil
	}
	// Multiple whitespace-separated clauses are AND-conjoined.
	parts := strings.Fields(s)
	out := Req{Raw: raw}
	for _, p := range parts {
		cls, allowPre, err := parseOnePart(p)
		if err != nil {
			return Req{}, err
		}
		out.Clauses = append(out.Clauses, cls...)
		if allowPre {
			out.allowPre = true
		}
	}
	return out, nil
}

// parseOnePart handles one whitespace-free requirement clause and
// returns its desugared form. Wildcards and caret/tilde operators
// expand into two clauses (inclusive lower + exclusive upper).
func parseOnePart(s string) ([]clause, bool, error) {
	// Caret: ^X.Y.Z
	if strings.HasPrefix(s, "^") {
		v, err := parsePartialVersion(s[1:])
		if err != nil {
			return nil, false, err
		}
		return caretClauses(v), v.IsPrerelease(), nil
	}
	// Tilde: ~X.Y.Z
	if strings.HasPrefix(s, "~") {
		v, err := parsePartialVersion(s[1:])
		if err != nil {
			return nil, false, err
		}
		return tildeClauses(v), v.IsPrerelease(), nil
	}
	// Wildcards: 1.*, 1.2.*
	if strings.Contains(s, "*") {
		return wildcardClauses(s)
	}
	// Relational operators
	opStr, rest := splitOp(s)
	v, err := parsePartialVersion(rest)
	if err != nil {
		return nil, false, err
	}
	switch opStr {
	case "", "=":
		return []clause{{Op: opEQ, V: v}}, v.IsPrerelease(), nil
	case ">":
		return []clause{{Op: opGT, V: v}}, v.IsPrerelease(), nil
	case ">=":
		return []clause{{Op: opGE, V: v}}, v.IsPrerelease(), nil
	case "<":
		return []clause{{Op: opLT, V: v}}, v.IsPrerelease(), nil
	case "<=":
		return []clause{{Op: opLE, V: v}}, v.IsPrerelease(), nil
	}
	return nil, false, fmt.Errorf("unknown operator %q", opStr)
}

// splitOp peels off `=`, `>`, `>=`, `<`, `<=` (longest match) from the
// front of s, returning the operator and the remainder.
func splitOp(s string) (string, string) {
	switch {
	case strings.HasPrefix(s, ">="):
		return ">=", s[2:]
	case strings.HasPrefix(s, "<="):
		return "<=", s[2:]
	case strings.HasPrefix(s, ">"):
		return ">", s[1:]
	case strings.HasPrefix(s, "<"):
		return "<", s[1:]
	case strings.HasPrefix(s, "="):
		return "=", s[1:]
	}
	return "", s
}

// parsePartialVersion parses a version string that may omit minor or
// patch (both default to 0). Used for caret / tilde / relational
// operands where `^1.2` means `^1.2.0`.
func parsePartialVersion(s string) (Version, error) {
	if s == "" {
		return Version{}, fmt.Errorf("missing version")
	}
	if s[0] == 'v' {
		s = s[1:]
	}
	core := s
	var build []string
	if plus := strings.IndexByte(s, '+'); plus >= 0 {
		core = s[:plus]
		build = strings.Split(s[plus+1:], ".")
	}
	body := core
	var pre []string
	if dash := strings.IndexByte(core, '-'); dash >= 0 {
		body = core[:dash]
		pre = strings.Split(core[dash+1:], ".")
	}
	parts := strings.Split(body, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}
	nums := [3]uint64{}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || (len(p) > 1 && p[0] == '0') {
			return Version{}, fmt.Errorf("invalid %s %q", majorMinorPatch(i), p)
		}
		nums[i] = n
	}
	return Version{
		Major: nums[0],
		Minor: nums[1],
		Patch: nums[2],
		Pre:   pre,
		Build: build,
	}, nil
}

// caretClauses expands `^X.Y.Z` to `>=X.Y.Z <next-major-or-minor`.
// Per §9: 0.x.y is treated as minor-stable (^0.1.2 == >=0.1.2 <0.2.0);
// 0.0.x is treated as exact (^0.0.1 == >=0.0.1 <0.0.2).
func caretClauses(v Version) []clause {
	lo := clause{Op: opGE, V: v}
	var hi Version
	switch {
	case v.Major > 0:
		hi = Version{Major: v.Major + 1}
	case v.Minor > 0:
		hi = Version{Minor: v.Minor + 1}
	default:
		hi = Version{Patch: v.Patch + 1}
	}
	return []clause{lo, {Op: opLT, V: hi}}
}

// tildeClauses expands `~X.Y.Z` to `>=X.Y.Z <X.(Y+1).0`. `~1` is
// equivalent to `^1` (major-stable) because no minor was fixed.
func tildeClauses(v Version) []clause {
	lo := clause{Op: opGE, V: v}
	hi := Version{Major: v.Major, Minor: v.Minor + 1}
	return []clause{lo, {Op: opLT, V: hi}}
}

// wildcardClauses expands `1.*` / `1.2.*` / `*` into bounds.
func wildcardClauses(s string) ([]clause, bool, error) {
	if s == "*" {
		return nil, false, nil
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return nil, false, fmt.Errorf("invalid wildcard %q", s)
	}
	// Find the first "*" — everything to the right must also be "*"
	// or missing.
	starAt := -1
	for i, p := range parts {
		if p == "*" || p == "x" || p == "X" {
			starAt = i
			break
		}
	}
	if starAt < 0 {
		return nil, false, fmt.Errorf("wildcard %q missing `*`", s)
	}
	for i := starAt + 1; i < len(parts); i++ {
		if !(parts[i] == "*" || parts[i] == "x" || parts[i] == "X") {
			return nil, false, fmt.Errorf("wildcard %q has fixed component after `*`", s)
		}
	}
	// Parse the fixed portion.
	var nums [3]uint64
	for i := 0; i < starAt; i++ {
		n, err := strconv.ParseUint(parts[i], 10, 64)
		if err != nil || (len(parts[i]) > 1 && parts[i][0] == '0') {
			return nil, false, fmt.Errorf("invalid %s %q in wildcard", majorMinorPatch(i), parts[i])
		}
		nums[i] = n
	}
	lo := Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}
	var hi Version
	switch starAt {
	case 0:
		// `*` handled above.
		return nil, false, nil
	case 1:
		hi = Version{Major: nums[0] + 1}
	case 2:
		hi = Version{Major: nums[0], Minor: nums[1] + 1}
	}
	return []clause{{Op: opGE, V: lo}, {Op: opLT, V: hi}}, false, nil
}

// Match reports whether v satisfies the requirement. Pre-release
// versions are accepted only if some clause in the requirement itself
// names a pre-release (per §11.4, "including a pre-release means you
// accept pre-releases of that exact version").
func (r Req) Match(v Version) bool {
	if v.IsPrerelease() && !r.allowPre {
		// Special case: an exact equal to a pre-release version
		// was already marked allowPre in ParseReq; this branch is
		// just for fully-stable requirements.
		return false
	}
	if len(r.Clauses) == 0 {
		// `*` matches any stable (pre already excluded above).
		return true
	}
	for _, c := range r.Clauses {
		if !clauseMatch(c, v) {
			return false
		}
	}
	return true
}

func clauseMatch(c clause, v Version) bool {
	cmp := Compare(v, c.V)
	switch c.Op {
	case opEQ:
		// Equal ignoring build metadata, per §10.
		return cmp == 0
	case opGE:
		return cmp >= 0
	case opGT:
		return cmp > 0
	case opLE:
		return cmp <= 0
	case opLT:
		return cmp < 0
	}
	return false
}

// String renders the requirement text the user originally supplied.
// Useful when re-emitting it into osty.toml or osty.lock.
func (r Req) String() string {
	if r.Raw != "" {
		return r.Raw
	}
	if len(r.Clauses) == 0 {
		return "*"
	}
	parts := make([]string, 0, len(r.Clauses))
	for _, c := range r.Clauses {
		var op string
		switch c.Op {
		case opEQ:
			op = "="
		case opGE:
			op = ">="
		case opGT:
			op = ">"
		case opLE:
			op = "<="
		case opLT:
			op = "<"
		}
		parts = append(parts, op+c.V.String())
	}
	return strings.Join(parts, " ")
}

// Max returns the maximum version in vs that satisfies r. Returns
// the zero Version and false when nothing matches.
func Max(r Req, vs []Version) (Version, bool) {
	var best Version
	found := false
	for _, v := range vs {
		if !r.Match(v) {
			continue
		}
		if !found || Less(best, v) {
			best = v
			found = true
		}
	}
	return best, found
}
