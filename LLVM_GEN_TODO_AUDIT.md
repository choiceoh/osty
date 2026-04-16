# LLVM Gen TODO Audit

이 문서는 LLVM 이주 25-step Phase 3의 산출물이다. 목적은 당시 Go transpiler
(`internal/gen`)의 TODO/unsupported marker를 감사해서, LLVM 1차 backend 범위가
기존 Go backend coverage gap과 섞이지 않도록 하는 것이었다. 현재는 역사 기록이며,
새 작업의 기준 문서로 취급하지 않는다.

## Static Marker Summary

Static scan command:

```sh
rg -n "TODO|unsupported" internal/gen --glob '*.go'
```

Observed output-producing marker sites:

| Site | Marker | Meaning | LLVM initial action |
|---|---|---|---|
| `internal/gen/decl.go` | `// unsupported decl: %T` | Defensive fallback for unknown/future AST declaration nodes. Current known decl kinds are dispatched explicitly. | Exclude unknown declaration forms; add an explicit LLVM unsupported diagnostic if reached. |
| `internal/gen/expr.go` | `/* TODO: expr %T */` | Defensive fallback for unknown/future AST expression nodes. Current known expression kinds are dispatched explicitly. | Exclude unknown expression forms; add an explicit LLVM unsupported diagnostic if reached. |
| `internal/gen/stmt.go` | `/* TODO: stmt %T */` | Defensive fallback for unknown/future AST statement nodes. Current known statement kinds are dispatched explicitly. | Exclude unknown statement forms; add an explicit LLVM unsupported diagnostic if reached. |
| `internal/gen/match.go` | `true /* TODO: pattern %T */` | Defensive fallback for unknown/future pattern nodes in match tests. Current AST pattern kinds are covered. | LLVM smoke excludes match lowering; later match support should reject unknown patterns explicitly. |
| `internal/gen/expr.go` | `TODO(phase3): ..spread on qualified type` | Real edge-case gap for struct spread when the struct type is package-qualified and gen cannot safely name the emitted Go type. | Exclude qualified struct spread from LLVM initial scope; classify under package/qualified type parity. |
| `internal/gen/gen.go` | `g.todo(...)` helper | Non-fatal TODO helper for unsupported constructs. Static scan found no current call sites. | No direct LLVM scope impact today. Keep equivalent helper only if diagnostics are explicit. |

Related non-output comments:

- `internal/gen/typ.go` notes that qualified `pkg.Type` currently falls back to
  `strings.Join(n.Path, ".")`.
- `internal/gen/expr.go` notes qualified struct literals and numeric duration
  shorthand as Phase 5/module-stdlib territory.
- `internal/gen/realworld_test.go` documents `word_freq.osty` as a depth probe
  because it depends on regex, json, fs, and parallel runtime coverage.

## Dynamic Probe Results

### LLVM smoke corpus

Command shape:

```sh
for f in testdata/backend/llvm_smoke/*.osty; do
  go run ./cmd/osty gen -o /tmp/out.go "$f"
  rg -n "TODO|unsupported" /tmp/out.go
done
```

Result:

- `testdata/backend/llvm_smoke/scalar_arithmetic.osty`: no generated
  TODO/unsupported markers
- `testdata/backend/llvm_smoke/control_flow.osty`: no generated
  TODO/unsupported markers
- `testdata/backend/llvm_smoke/booleans.osty`: no generated
  TODO/unsupported markers

### Phase 1 executable examples

Result:

- `examples/concurrency/main.osty`: no generated TODO/unsupported markers
- `examples/ffi/main.osty`: no generated TODO/unsupported markers

This means the currently selected LLVM smoke files do not rely on known Go gen
TODO fallbacks.

### Spec corpus audit

Command:

```sh
go test ./internal/gen -run TestSpecCorpusAudit -count=1 -v
```

Observed result:

- Fails overall because `testdata/spec/positive/05-modules.osty` has an existing
  resolve error in the single-file audit harness.
- 8 of 9 positive fixtures reached Go vet successfully.
- `07-errors.osty` and `11-testing.osty` logged checker warnings/errors but still
  generated vet-clean Go.

Implication:

- The spec corpus is useful as a broad Go-gen health probe.
- It is not a clean LLVM executable parity gate.
- LLVM should initially use the smaller smoke corpus, then graduate selected
  spec fixtures only after module/package/runtime support exists.

## LLVM Initial Exclusions

The first LLVM backend vertical slice should explicitly exclude these areas:

| Area | Why excluded initially | Later phase |
|---|---|---|
| Unknown/future AST node fallbacks | These are defensive Go-gen guards, not meaningful language support targets. | Add explicit unsupported diagnostics in backend facade. |
| Match lowering and complex patterns | Requires enum/tag representation and pattern binding layout. | Runtime/heap values and enum parity. |
| Struct/enum/user aggregate layout | Requires stable runtime/value ABI and source-compatible layout choices. | Runtime ABI phase. |
| Qualified package types and package-member codegen | Current Go path still has package/workspace emission limits. | Package/workspace parity phase. |
| Qualified struct spread | Known Go-gen edge case. Needs qualified type naming and field layout. | Package/aggregate parity phase. |
| Generic monomorphization | Needs IR to carry `check.Result.Instantiations`. | IR contract phase. |
| Closures with captures/destructuring | Needs closure environment layout and lifetime/ownership policy. | Runtime ABI plus generics/closures phase. |
| Interfaces/vtables | Needs fat pointer/vtable representation. | Interface parity phase. |
| Go FFI (`use go`) | Native backend cannot call Go packages directly. | Native FFI design; Go-only diagnostic until then. |
| Stdlib runtime bridges | Current Go backend lowers many stdlib calls to Go helpers/imports. | Module-by-module stdlib runtime port. |
| Channels and structured concurrency | Requires scheduler/channel/task runtime ABI. | Concurrency runtime phase. |
| Test harness execution | `osty test` currently has baseline fixture issues and uses generated Go harness behavior. | LLVM test harness phase. |

## LLVM Phase 3 Conclusion

The Go transpiler has only a small number of explicit emitted TODO fallback
sites, and the new LLVM smoke corpus avoids them. The important migration
decision is therefore not "match every Go-gen fallback immediately"; it is:

1. Keep the first LLVM backend scope narrow: scalar values, simple functions,
   branches, loops, and integer `println`.
2. Emit explicit unsupported diagnostics for excluded constructs rather than
   producing placeholder LLVM.
3. Promote excluded areas only when the relevant IR metadata and runtime ABI
   are in place.

## Phase 3 Done Criteria

- Static TODO/unsupported marker sites are documented.
- Dynamic probes confirm LLVM smoke fixtures generate without Go TODO markers.
- The initial LLVM exclusion list is explicit.
- Later implementation phases can decide whether a construct is in scope
  without re-auditing `internal/gen` from scratch.

The follow-up backend-aware artifact layout policy is tracked in
[`LLVM_ARTIFACT_LAYOUT.md`](./LLVM_ARTIFACT_LAYOUT.md).
