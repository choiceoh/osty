### 10.4 Prelude

Auto-imported in every file:

- Types: all primitives, `Option`, `Result`, `Error`, `List`, `Map`,
  `Set`, `Never`
- Interfaces: `Equal`, `Ordered`, `Hashable`
- Variants: `Some`, `None`, `Ok`, `Err`
- Functions: `print`, `println`, `eprint`, `eprintln`, `dbg`,
  `taskGroup`, `parallel`
- Constants: `true`, `false`

#### v0.6 prelude additions

The v0.6 baseline does not change the prelude — additions to the
auto-imported set would silently affect existing code, which is a
breaking change at any cadence. Two v0.6 surfaces *reach* the
prelude indirectly:

1. **Capability interfaces** (`Clock`, `Rng`, `Env`, `Fs`, `Net`,
   `Process`, `Console`) are *not* auto-imported. They live in
   `std.capability.*` and are imported explicitly. Auto-importing
   would tempt scripts to type-annotate parameters as `Clock` etc.
   without the explicit `use std.capability.Clock` that signals
   the dependency.
2. **`Cancelled`** (the cancel signal type) is *not* auto-imported.
   It's available through `std.cancel.Cancelled` for downcast
   patterns. Most code never names the type — `?`-propagation
   handles cancellation transparently.

The prelude functions `print` / `println` / `eprint` / `eprintln`
desugar to ambient `Console` calls inside `#[ambient(console)]`
entry points, and to errors elsewhere (`E0780` — implicit Console
use from non-entry-point function). This keeps script ergonomics
unchanged while preserving the capability discipline elsewhere.

#### Prelude evolution rule

Any change to the auto-import set is a major-version event. The
v0.7 outlook is to keep the prelude stable as the surface that
v0.5 and v0.6 share unchanged.
