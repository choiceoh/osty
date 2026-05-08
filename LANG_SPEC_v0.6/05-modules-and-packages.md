## 5. Modules and Packages

Osty v0.6 organizes code by directory: a directory is a package, and
all `.osty` files in that directory share one namespace. Packages
import each other through `use` statements. The import surface —
scoped imports (`use path::{a, b as c}`), re-exports
(`pub use path.Sym`), and `#[cfg(...)]`-conditional declarations —
is the v0.6 baseline.

v0.6 layers two enforcement surfaces on this chapter's syntax,
defined elsewhere:

- **`osty publish` API-surface diff** (§13.5, §3.14.3) — a stable
  package's public surface is hashed at publish time; subsequent
  releases that drop or weaken a `#[stability("stable")]` symbol
  bump the major version automatically (G44).
- **Sealed `pub` types** (§3.4.5) — a struct annotated
  `#[sealed_construct(parse)]` exports its name through `pub` but
  rejects external literal construction; downstream packages must
  route through the named constructor.

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

### 5.6 Public surface and v0.6 evolution rules

The public surface of a package is the set of `pub` declarations
visible to importers — `pub fn`, `pub struct` (with its `pub`
fields), `pub enum` variants, `pub interface`, `pub type` aliases,
and `pub let` constants. v0.6 layers three machine-readable
attestations onto that surface:

#### 5.6.1 `#[stability]` and SemVer

A `pub` declaration carries `#[stability(level)]` (§3.14) where
`level` is one of `"stable"` / `"experimental"` / `"deprecated"` /
`"internal"`. `osty publish` (§13.5) reads the manifest's
`stability_default` and per-symbol overrides, then computes the
API-surface diff against the previous published version:

| Change | `stable` | `experimental` |
|---|---|---|
| Add a new `pub` symbol | minor bump | patch bump |
| Add a new field to a `pub struct` (without sealed_construct) | major bump | minor bump |
| Add a new variant to a `pub enum` | major bump (unless `#[match_compat]` covers it) | minor bump |
| Remove a `pub` symbol | major bump | warning (`W2100`) |
| Change the type of a `pub fn` parameter / return | major bump | warning (`W2100`) |
| Add a new `Err` variant to `#[error_contract]` | major bump | minor bump |

Bumping below the required level produces `E2100` and blocks publish.

#### 5.6.2 Sealed types in the public surface

A `pub struct` annotated `#[sealed_construct(parse)]` (§3.4.5)
appears in the public surface as a *type only* — its fields, even
the `pub` ones, do not allow external literal construction. Adding
or removing fields on a sealed type is therefore *not* a SemVer-
breaking change in itself; only the constructor signature
(`Type.parse(...)` etc.) is part of the stable contract. This
inverts the usual struct-evolution rule and is what makes the
parse-don't-validate pattern compose well with v0.6 publish gates.

#### 5.6.3 Capability surface is part of the contract

A `pub fn` that takes a `Net` parameter exposes "this function
performs a network effect" as part of its public contract. Adding,
removing, or changing the capability set of a `pub fn` is a
**breaking change** at the `stable` level — the API-surface diff
algorithm includes capability-typed parameters in the signature
hash. Authors who want to add a capability requirement to an
existing `stable` API must either bump the major version or
introduce a new function and deprecate the old one with
`#[deprecated(use = "newName")]`.

---
