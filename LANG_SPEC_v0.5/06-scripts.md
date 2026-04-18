## 6. Scripts

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

---
