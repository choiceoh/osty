## 5. Modules and Packages

- **Scope**: Osty language spec — 5. Modules and Packages
- **Type**: Language specification
### 5.1 Package = Directory

A directory is a package. All `.osty` files in a directory belong to the
same package and share a namespace.

```
myapp/
├── main.osty           // package myapp
├── config.osty         // package myapp
├── auth/
│   ├── login.osty      // package myapp.auth
│   └── token.osty      // package myapp.auth
└── db/
    └── postgres.osty   // package myapp.db
```

### 5.2 Import

```osty
use std.fs
use std.http
use std::{fs, http as web}
use github.com/user/lib
use github.com/user/lib as mylib
pub use myapp.db.Conn
use go "net/http" {
    fn Get(url: String) -> Result<Response, Error>
    struct Response {
        StatusCode: Int,
        Body: Reader,
    }
}
```

`use <path>` imports an Osty package. Scoped import form
`use path::{A, B as C}` imports several names from the same package.
`pub use <path>` re-exports the imported symbol from the current package;
re-export cycles are `E0552`. `use go "<path>" { ... }` imports a Go
package via FFI (see §12).

**Module access path resolution.** Given `use dep_or_pkg`, the imported
name binds the *package itself*. To access a top-level item from a
sub-module file within that package, write `dep_or_pkg.module.item`
(e.g., `use toolchain; toolchain.elab.elabFile(cx)`). Equivalently, the
shorter form `use dep_or_pkg.module` binds the *module file*, and items
are accessed as `module.item` (e.g., `use toolchain.elab; elab.elabFile(cx)`).
`use dep_or_pkg.module as alias` binds the alias to the module. A
single-segment alias to a *package* (`use toolchain as tc`) is permitted
and accesses items as `tc.module.item`; the alias never collapses module
boundaries. Implementations must distinguish module bindings from type
bindings — applying `<module>.<item>` to a non-module produces `E0703`
(method-on-type) which currently leaks for cross-package `use` and is
tracked in `SPEC_GAPS.md` under `cross-pkg-module-resolution`.

### 5.3 Visibility

Declarations are package-private by default. `pub` exports:

```osty
pub fn login(user: String) -> Result<Token, Error> { ... }
fn hashPassword(p: String) -> String { ... }         // private

pub struct User {
    pub name: String,       // exported field
    email: String,          // private field
}

pub let MAX_USERS = 10000
```

`pub` may appear on:
- Top-level `fn`, `struct`, `enum`, `interface`, `type`, `let`
- Fields inside `struct`
- Methods inside `struct` or `enum`

Enum variants inherit the enum's visibility. Interface methods are
visible wherever the interface is. It is a compile error to mark an
enum variant `pub` when the enclosing enum is package-private — a
variant's visibility cannot exceed its enum's.

**Partial declarations** (§3.4). All declarations of the same type
must agree on visibility: if one decl writes `pub struct U`, every
other decl of `U` in the same package must also write `pub struct U`.
Inconsistent visibility across decls is a compile error.

**Type alias visibility.** A `type` alias is transparent: `pub type A = X`
exports the name `A`, not any additional methods on `X`. The alias's
`pub`-ness belongs to the alias declaration itself, independent of
whether `X` is `pub`.

### 5.4 Circular Imports

Forbidden. The compiler enforces a strict DAG. Diamond imports through
distinct paths (A→B→D and A→C→D) are allowed — each package is resolved
once per version.

Version conflicts for the same external package (two dependency paths
bringing in different versions of the same package) are resolved by
the package manager per `osty.lock` rules; semantics are implementation-
defined beyond "one version of a given package is visible in a single
build" — see §13.2.

### 5.5 One Package Per Directory

Each directory is exactly one package. Sub-packages live in
subdirectories.

---
