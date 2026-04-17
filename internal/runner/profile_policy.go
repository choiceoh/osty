// profile_policy.go is the Go snapshot of
// toolchain/profile_flags.osty. Osty is the source of truth; the
// drift test in this package enforces parity.
package runner

import "strings"

// ProfileSelection mirrors toolchain/profile_flags.osty's
// ProfileSelection. Non-empty Conflict means the host should fail
// with that message and drop Name.
//
// Osty: toolchain/profile_flags.osty:14
type ProfileSelection struct {
	Name     string
	Conflict string
}

// SelectProfileName resolves --profile / --release / fallback
// precedence. See the Osty source for rule documentation.
//
// Osty: toolchain/profile_flags.osty:28
func SelectProfileName(profileName string, releaseShortcut bool, fallback string) ProfileSelection {
	if releaseShortcut {
		if profileName != "" && profileName != "release" {
			return ProfileSelection{
				Name:     "",
				Conflict: "--release conflicts with --profile " + profileName,
			}
		}
		return ProfileSelection{Name: "release"}
	}
	if profileName != "" {
		return ProfileSelection{Name: profileName}
	}
	if fallback != "" {
		return ProfileSelection{Name: fallback}
	}
	return ProfileSelection{Name: "debug"}
}

// ParseFeatureList splits a comma-separated --features string,
// trims whitespace, and drops empty entries. Order preserved.
//
// Osty: toolchain/profile_flags.osty:54
func ParseFeatureList(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
