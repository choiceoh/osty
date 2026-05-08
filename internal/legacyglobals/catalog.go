package legacyglobals

// legacyRule maps one v0.5 legacy global call to its v0.6 capability
// replacement. The catalog mirrors `BREAKING_v0.6.md` §3 (the broader
// list) and `LANG_SPEC_v0.6/20-capabilities.md` §20.15 (the canonical
// rewrite table). The Osty side at
// `internal/stdlib/modules/capability.osty::legacyGlobalRewriteRules`
// keeps a sample of the same data — we deliberately keep both surfaces
// because:
//
//   - The Osty catalog drives docgen / migration tooling and is the
//     authority for *user-facing* migration messages.
//   - This Go-side catalog drives the front-end W0750 detector and
//     stays small enough to pin without an interpreter shim, so the
//     compiler boundary keeps working before the LLVM self-host path
//     can re-emit the lookup table.
//
// Drift between the two is policed by the parity test in
// `internal/stdlib/capability_test.go` (it exercises the Osty-side
// helper names) and the unit tests in this package (which exercise
// the Go-side lookup behaviour). When a new entry is added one place,
// add it to the other in the same PR.
type legacyRule struct {
	// module is the bare-Ident head of the selector at the call site
	// (e.g., `time` in `time.now()`).
	module string
	// method is the method name on the legacy module (e.g., `now`).
	method string
	// capability is the canonical v0.6 capability that should now be
	// passed as a parameter (e.g., `Clock`).
	capability string
	// replacement is the canonical migration form for the user. It
	// reads as the body of the `Hint:` line — terse, no period.
	replacement string
}

// legacyRules is the static catalog. Indexed via lookupRule by
// (module, method) lower-case match.
var legacyRules = []legacyRule{
	// time → Clock
	{module: "time", method: "now", capability: "Clock", replacement: "clock.now()"},
	{module: "time", method: "monotonic", capability: "Clock", replacement: "clock.monotonic()"},
	{module: "time", method: "sleep", capability: "Clock", replacement: "clock.sleep(d)"},

	// random → Rng
	{module: "random", method: "next", capability: "Rng", replacement: "rng.next()"},
	{module: "random", method: "nextBytes", capability: "Rng", replacement: "rng.nextBytes(n)"},
	{module: "random", method: "default", capability: "Rng", replacement: "rng parameter"},

	// env → Env
	{module: "env", method: "args", capability: "Env", replacement: "env.args()"},
	{module: "env", method: "get", capability: "Env", replacement: "env.get(k)"},
	{module: "env", method: "require", capability: "Env", replacement: "env.require(k)"},
	{module: "env", method: "set", capability: "Env", replacement: "env.set(k, v)"},
	{module: "env", method: "unset", capability: "Env", replacement: "env.unset(k)"},
	{module: "env", method: "vars", capability: "Env", replacement: "env.vars()"},

	// fs → Fs
	{module: "fs", method: "read", capability: "Fs", replacement: "fs.read(p)"},
	{module: "fs", method: "readToString", capability: "Fs", replacement: "fs.readToString(p)"},
	{module: "fs", method: "write", capability: "Fs", replacement: "fs.write(p, b)"},
	{module: "fs", method: "writeString", capability: "Fs", replacement: "fs.writeString(p, s)"},
	{module: "fs", method: "exists", capability: "Fs", replacement: "fs.exists(p)"},
	{module: "fs", method: "create", capability: "Fs", replacement: "fs.create(p)"},
	{module: "fs", method: "remove", capability: "Fs", replacement: "fs.remove(p)"},
	{module: "fs", method: "mkdir", capability: "Fs", replacement: "fs.mkdir(p)"},
	{module: "fs", method: "mkdirAll", capability: "Fs", replacement: "fs.mkdirAll(p)"},

	// net → Net
	{module: "net", method: "dial", capability: "Net", replacement: "net.connect(addr)"},
	{module: "net", method: "connect", capability: "Net", replacement: "net.connect(addr)"},
	{module: "net", method: "listen", capability: "Net", replacement: "net.listen(addr)"},

	// os/process → Process
	{module: "os", method: "exec", capability: "Process", replacement: "process.exec(c, a)"},
	{module: "os", method: "exit", capability: "Process", replacement: "process.exit(code)"},
	{module: "os", method: "hostname", capability: "Process", replacement: "process.hostname()"},
	{module: "os", method: "pid", capability: "Process", replacement: "process.pid()"},
}

// lookupRule returns the catalog entry that matches a (module, method)
// pair, or zero+false when no entry applies. The first matching rule
// wins; the catalog is small enough that linear scan is the simplest
// correct answer and avoids the init-order coupling a map would add.
func lookupRule(module, method string) (legacyRule, bool) {
	for _, r := range legacyRules {
		if r.module == module && r.method == method {
			return r, true
		}
	}
	return legacyRule{}, false
}
