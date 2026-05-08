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
`pub use <path>` re-exports the imported symbol from the current
package; re-export cycles are `E0552`, re-exporting a private source
symbol is `E0553`, and introducing the same local/exported name through
two imports or an import plus a local declaration is `E0554`. `use go
"<path>" { ... }` imports a Go package via FFI (see §12).

#### 5.2.1 Re-export identity, stability, and shadowing

A `pub use` creates an alias in the current package's public surface;
it does not create a new declaration. By default the alias inherits the
source symbol's `#[since]`, `#[stability]`, `#[error_contract]`,
capability parameters, and information-flow annotations. `osty doc`
may render the alias path, but `osty publish` hashes the resolved
source symbol plus the alias name.

An alias may restate `#[since]` or `#[stability]` only to make the
current package's promise stricter:

- `experimental` source re-exported as `stable` is allowed, but the
  current package now owns the stable promise and future upstream drift
  can break its publish gate.
- `stable` source re-exported as `experimental` or `internal` is
  rejected at publish time (`E2100`) because it weakens the imported
  contract.
- `deprecated` metadata may be added at the alias to steer users to a
  different path; it does not remove deprecation metadata from the
  source.

Public names do not shadow each other. If a package contains both
`pub use a.X` and `pub fn X(...)`, or two `pub use` aliases that export
the same final name, the module is ambiguous and compilation emits
`E0554`. Authors must rename one import with `as`.

#### 5.2.1 Import resolution order

When the compiler resolves `use <path>`, it searches in this
order:

1. **Workspace local** — sibling packages declared in
   `[workspace] members`.
2. **Path dependencies** — `[dependencies]` entries with `path =
   "..."`.
3. **Git dependencies** — entries with `git = "..."`.
4. **Registry dependencies** — entries pointing to a registry
   (default or named). Resolution uses `osty.lock` for the exact
   version.
5. **Standard library** — `std.*` paths resolved from the toolchain.

Ambiguity is `E0550` — two paths resolve the same name. The fix
is to use `as` to rename one or to remove the duplicate.

#### 5.2.2 `pub use` re-export and SemVer

A `pub use` re-export is part of the package's public API surface.
Adding or removing a `pub use` is a SemVer event:

| Change | Stability |
|---|---|
| Add `pub use ...` | minor bump (additive — new exported name) |
| Remove `pub use ...` | major bump (caller's import breaks) |
| Change re-export target (`pub use a.X` → `pub use b.X`) | major bump (semantics may differ) |

The third row is the trap — even if `a.X` and `b.X` look
identical, callers' transitive imports may differ. SemVer treats
the change as breaking by default.

#### 5.2.3 Capability re-exports

A package may re-export the canonical capability interfaces:

```osty
pub use std.capability.Clock
pub use std.capability.Net
```

Downstream consumers can `use mypkg.Clock` instead of
`use std.capability.Clock`. The re-export does not change
identity — `mypkg.Clock` and `std.capability.Clock` are the same
interface; structural typing means a value satisfying one
satisfies the other.

The pattern is uncommon (most packages don't need to re-export
stdlib capabilities) but supported for facade modules.

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

#### 5.3.1 Visibility and v0.6 surfaces

Visibility composes with each v0.6 annotation surface:

**`pub` + `#[stability]`** — every `pub` declaration participates
in the public API surface (§3.14.3). The default `[stability]
default = "stable"` requires every `pub` symbol to either carry
explicit `#[stability(...)]` or be considered stable by virtue of
the manifest default.

**`pub` + sealed construct** — a `pub struct
#[sealed_construct(parse)]` exports the name and the constructor;
external literal construction is rejected even on `pub` fields
(§3.4.5). The pattern of "exported type with private state" is
the canonical sealed-construct shape.

**`pub` + `#[error_contract]`** — a `pub fn` returning
`Result<T, E>` may carry `#[error_contract]`, which is part of the
public API. The contract variants are visible to callers; adding
or removing variants is SemVer-relevant.

**`pub` + capability parameters** — a `pub fn` taking `Net` etc.
exports its capability requirements as part of the public
contract. Adding or removing a capability parameter is breaking
(§5.6.3).

#### 5.3.2 Internal-use only declarations

Declarations marked `#[stability("internal")]` are part of the
package's compile-time surface but excluded from the SemVer
contract. They are intended for sibling-package use within a
workspace; downstream packages should not import them.

```osty
#[stability("internal")]
pub fn _internalHelper(x: T) -> U { ... }
```

`osty publish` reports `internal` symbols in the manifest but does
not include them in the API-surface diff. `osty audit
--internal-deps` warns when external code imports an `internal`
symbol — a soft signal rather than a hard error.

### 5.4 Circular Imports

Forbidden. The compiler enforces a strict DAG. Diamond imports through
distinct paths (A→B→D and A→C→D) are allowed — each package is resolved
once per version.

Version conflicts for the same external package (two dependency paths
bringing in different versions of the same package) are resolved by
the package manager per `osty.lock` rules; semantics are implementation-
defined beyond "one version of a given package is visible in a single
build" — see §13.2.

#### 5.4.1 Cycle detection algorithm

The compiler builds the import graph during the resolve pass
(§13). Cycles are detected via depth-first traversal:

1. Start from every package root in the workspace.
2. Walk transitive `use` edges, marking visited packages.
3. A back-edge (visiting a package already on the current
   recursion stack) is a cycle — report `E0551` with the cycle
   path.

Cycles within a single package (file A imports from file B in the
same package) are *not* import cycles — the package is one
namespace. Only inter-package cycles are reported.

Re-export cycles (`pub use a.X` where `a` re-exports back) are
detected by the same algorithm but reported with `E0552` to
distinguish from regular import cycles.

#### 5.4.2 Diamond dependency consistency

When `A → B → D` and `A → C → D` resolve `D` to the same version,
the build sees one copy of D shared between B and C. When they
resolve to different versions, the package manager:

1. Picks the *highest compatible version* per SemVer rules.
2. If both versions satisfy each constraint, uses the higher.
3. If neither satisfies, fails with a constraint conflict.

The result is recorded in `osty.lock`. Subsequent builds use
exactly that resolution unless `osty update` is invoked.

The "highest-compatible" policy means a package depending on `D
^1.0` and another on `D ^1.5` resolves to whichever 1.x is
highest available (e.g. `1.7.3`). Both packages see the same
`D` instance.

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
| Rename a public function parameter | major bump | warning (`W2100`) |
| Change a public default argument value | major bump | warning (`W2100`) |
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

### 5.7 Conditional compilation — `#[cfg(...)]`

`#[cfg(...)]` is a declaration filter evaluated before name resolution
and type checking. The parser still parses cfg-disabled declarations so
syntax errors do not hide behind feature flags, but disabled
declarations do not introduce names, do not participate in `pub use`,
and do not need to type-check.

Allowed keys are:

| Key | Values |
|---|---|
| `os` | target operating-system name such as `"linux"`, `"darwin"`, `"windows"` |
| `arch` | target architecture such as `"amd64"`, `"arm64"` |
| `target` | full target triple/profile name from §13.2 |
| `feature` | a feature declared in `[features]` (§13.2) |

Composition uses `all(...)`, `any(...)`, and `not(...)`. Multiple
`#[cfg]` annotations on the same declaration are conjoined. Unknown
keys are `E0405`; unknown feature names are also `E0405` because they
usually indicate a misspelled manifest feature.

```osty
#[cfg(all(os = "linux", feature = "epoll"))]
pub fn poller() -> Poller { ... }

#[cfg(any(os = "darwin", os = "linux"))]
pub use unix.NetPoller as PlatformPoller
```

For public API, the exported surface is computed after cfg filtering
for the selected target/features. `osty publish` records cfg
conditions in the manifest so a stable symbol cannot silently disappear
from a supported target without the appropriate SemVer bump.

---
