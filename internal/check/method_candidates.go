package check

import "github.com/osty/osty/internal/types"

// methodCandidates returns a list of nearby method names on a type,
// used for typo suggestions in "unknown method" diagnostics. Stubbed
// while the real type-aware analysis is being reworked.
func (c *checker) methodCandidates(_ types.Type) []string { return nil }
