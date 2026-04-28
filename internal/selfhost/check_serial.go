package selfhost

import "sync"

// The generated checker still carries small package-level recursion guards.
// Keep public checker entry points serialized until those guards move into
// CheckEnv.
var selfhostCheckMu sync.Mutex
