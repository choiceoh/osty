package query

import (
	"fmt"
	"strings"
)

// CycleError is returned (via panic) when a query transitively
// depends on itself. The message lists the full chain for
// debuggability.
type CycleError struct {
	Chain []string
}

func (e *CycleError) Error() string {
	var b strings.Builder
	b.WriteString("query cycle detected: ")
	for i, name := range e.Chain {
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(name)
	}
	return b.String()
}

// checkCycle walks the current stack and returns a CycleError if
// (qid, key) is already being computed. Must be called under db.mu.
func (db *Database) checkCycle(qid QueryID, key any) *CycleError {
	for _, f := range db.stack {
		if f.qid == qid && keysEqual(f.key, key) {
			chain := make([]string, 0, len(db.stack)+1)
			for _, s := range db.stack {
				chain = append(chain, fmt.Sprintf("%s(%v)", db.names[s.qid], s.key))
			}
			chain = append(chain, fmt.Sprintf("%s(%v)", db.names[qid], key))
			return &CycleError{Chain: chain}
		}
	}
	return nil
}

// keysEqual compares two query keys for equality. The key type is
// erased to `any` in the slot store, so we rely on Go's built-in
// equality — valid since keys are constrained to comparable types at
// query registration (Query[K comparable, V any]).
func keysEqual(a, b any) bool {
	return a == b
}
