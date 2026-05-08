## 6. Scripts

Osty v0.6 lets a single file mix declarations and top-level statements
to function as a *script*. The compiler synthesizes an implicit
`main() -> Result<(), Error>` from those statements. v0.6 scripts run
under an automatic `#[ambient(clock, rng, env, fs)]` capability binding
(§20.3), so common effectful calls work without explicit capability
parameters in the script body. Library code retains the discipline of
explicit capability arguments — scripts are the boundary where ambient
binding is acceptable.

A file is a **script file** if it contains top-level statements outside
any function or type declaration. A file with only declarations is a
**module file**.

```osty
#!/usr/bin/env osty
// hello.osty — script file

let args = env.args()
let name = args.get(1) ?? "world"
println("hello, {name}")
```

Rules:

- A script file is compiled as if its top-level statements were wrapped
  in `fn main() -> Result<(), Error>` with an implicit trailing
  `Ok(())`.
- Script files may not be imported from other packages.
- `pub` declarations in a script file are meaningless; the formatter
  warns.
- Top-level `?` inside a script propagates to the implicit `main` and
  causes process exit with a non-zero code, printing the error via
  `eprintln`.
- Top-level `fn`, `struct`, `enum`, `interface`, `type` declarations
  are permitted alongside statements; they become local to the
  script's `main`.
- A top-level `return expr` is permitted; it is a `return` from the
  implicit `main` and behaves as such. The exit code follows the
  returned `Result<(), Error>` (zero on `Ok(())`, non-zero on
  `Err(...)`). Scripts that need to pick a specific exit code should
  call `os.exit(code)` from `std.os` (§10.15) instead. Resolves G5.
- A bare `defer` at the top level of a script is a compile error —
  `defer` requires an enclosing block. Wrap top-level cleanup in
  `{ defer ... }` when it is needed.
- Top-level `let` bindings and statements are evaluated strictly
  top-to-bottom in source order. There is no hoisting of `let`
  initialization.

`osty run script.osty` compiles and executes a script. With the shebang
line present, `chmod +x script.osty && ./script.osty` also works.

### 6.1 Ambient capability binding

The synthesized `main` in a script file carries an automatic
`#[ambient(clock, rng, env, fs)]` (§20.3). The ambient binding is
*scoped to the script's main only* — it does not propagate into:

- `fn` declarations defined at the top level of the script (those
  declarations are ordinary functions; they receive only what their
  signature declares),
- closures `g.spawn(|| ...)` inside a `taskGroup` (closures capture
  the script's bindings by reference, so they can name `clock`
  etc., but the *capture* is what makes them visible — not ambient
  forwarding through the spawn boundary; see §8.7.2).

```osty
#!/usr/bin/env osty
// hello.osty — script file

let now = clock.now()                  // ambient binding visible
let user = env.get("USER") ?? "world"

println("hi {user}, {now}")

// Helper fn — ambient does NOT cross. Capability must be a parameter.
fn render(clock: Clock, prefix: String) -> String {
    "{prefix} @ {clock.now().toString()}"
}

println(render(clock, "tick"))         // explicit hand-off
```

Scripts that need additional capabilities (`net`, `process`,
`console`) must declare them via an explicit `#[ambient(...)]` at
the top of the script source. The synthesized `main`'s default set
covers the most common script needs but is intentionally limited;
broader scripts opt in:

```osty
// script with additional ambient
#[ambient(clock, rng, env, fs, net, console)]

let resp = net.get("https://api.example.com/health")?
console.println("health: {resp.status}")
```

A `#[ambient]` line at script top-level **replaces** the synthesized
default set — it is not additive. Listing only `clock` here would
remove `rng`, `env`, and `fs`. This is the design explicitly: *no
hidden defaults*. The reduced set is what you signed up for.

### 6.2 Scripts and information flow

Scripts inherit the same flow-tracking rules as library code (§21).
Tainted input flowing into a sink without sanitization is an error
even at the script level — there is no relaxed mode. The expected
pattern is:

```osty
#[ambient(env, process)]

let target = env.get("TARGET") ?? "localhost"
// `target` is tainted (env value); shell sink requires shell_safe.
// Either parameterize…
process.exec("ping", ["-c", "1", target])

// …or sanitize explicitly.
let safe = std.shell.quote(target)
process.execShell("ping -c 1 {safe}")
```

The `process.exec(cmd, args)` form already takes a list of arguments
and does not interpolate; tainted strings landing in `args` are
acceptable to `process.exec` because the spawn does not interpret
them as shell metacharacters. `process.execShell(cmdline)` *does*
interpret them and therefore requires `shell_safe` on the input.

---
