package cli

import "testing"

// TestParseArgs_LegacyGlobalsFlagPropagates pins that
// `--legacy-globals` is recognised at the global pre-subcommand
// position and lifts into CliFlags.LegacyGlobals = true. The flag is
// the v0.6.x → v0.7 transition handle for v0.5 effect-global
// deprecations (LANG_SPEC_v0.6 §20.15, BREAKING_v0.6.md §3) — every
// front-end command needs to see it.
func TestParseArgs_LegacyGlobalsFlagPropagates(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"check FILE", []string{"--legacy-globals", "check", "main.osty"}},
		{"resolve DIR", []string{"--legacy-globals", "resolve", "."}},
		{"lint DIR", []string{"--legacy-globals", "lint", "."}},
		{"typecheck FILE", []string{"--legacy-globals", "typecheck", "main.osty"}},
		{"build", []string{"--legacy-globals", "build"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := ParseArgs(tc.args)
			if !parsed.IsOk() {
				t.Fatalf("ParseArgs returned errors: %v", parsed.Errors)
			}
			if !parsed.Flags.LegacyGlobals {
				t.Fatalf("Flags.LegacyGlobals = false, want true; raw args = %v", tc.args)
			}
		})
	}
}

// TestParseArgs_LegacyGlobalsDefaultsFalse keeps the default behaviour
// honest: the flag must be explicitly opted into; v0.6.0 users that
// don't pass it should see no W0750 noise.
func TestParseArgs_LegacyGlobalsDefaultsFalse(t *testing.T) {
	parsed := ParseArgs([]string{"check", "main.osty"})
	if !parsed.IsOk() {
		t.Fatalf("ParseArgs returned errors: %v", parsed.Errors)
	}
	if parsed.Flags.LegacyGlobals {
		t.Fatalf("Flags.LegacyGlobals = true without --legacy-globals; want false (opt-in only)")
	}
}
