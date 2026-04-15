package lint

// memberAccess is the set of names that appear as `.field` or
// `.method(...)` accesses anywhere in the lint scope. Used by the
// unused-member rule (L0005/L0006) to confirm that an unreferenced
// declaration really is unused — a private field whose name shows up
// in any access expression is counted as used.
type memberAccess struct {
	fields  map[string]bool
	methods map[string]bool
}
