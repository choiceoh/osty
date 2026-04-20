// Package speccorpus is a test-only driver that enforces the spec
// corpus contract from CLAUDE.md:
//
//   - every file under testdata/spec/positive/ parses with zero
//     error-severity diagnostics (modulo the documented waivers),
//   - every `// === CASE: Exxxx === ...` block inside
//     testdata/spec/negative/reject.osty emits a diagnostic with that
//     exact code.
//
// The corpus is discovered from the filesystem at test time, so
// dropping a new NN-<chapter>.osty into testdata/spec/positive/ (or a
// new CASE block into reject.osty) is picked up automatically with no
// Go-side registration.
package speccorpus
