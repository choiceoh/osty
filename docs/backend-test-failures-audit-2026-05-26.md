# Backend Test Failures Audit (post-#2020 baseline)

**Date**: 2026-05-26
**Reviewer-flagged followup**: PR #2020 closed the CI gate hole so future
regressions can't slip silently. This document captures the BASELINE
of what already fails — what's broken in production that the gate now
exposes.

## Methodology

```sh
OSTY_REQUIRE_REAL_LLVM_EMISSION=1 \
  go test -count=1 -timeout 20m ./internal/backend/
```

Run against `main` (commit after PR #2020), osty-self cached, clang
available, no `-short`. 61 / ~800 tests fail.

## Root cause class A — Map iteration value lookup is mis-lowered

**Count**: ~55 of 61 failures.

**Error**: `'%tN' defined with type 'ptr' but expected 'i64'` at the
second of two adjacent list_get calls inside a Map iteration body.

**Pattern**: bodied `Map.values` / `Map.entries` / `Map.keys` (and
anything calling them) iterates via `for (k, v) in self`. The current
lowering emits:

```llvm
%t10 = load ptr %l4          ; keys list
%t11 = load i64 %l6          ; loop index i
%t12 = call ptr @osty_rt_list_get_string(ptr %t10, i64 %t11)  ; keys[i] = key
store ptr %t12, ptr %l8      ; key stored
%t13 = load ptr %l3          ; the MAP (not values list!)
%t14 = load ptr %l8          ; loads the KEY string (NOT the index)
%t15 = call i64 @osty_rt_list_get_i64(ptr %t13, i64 %t14)  ; UB: map ptr + key as index
```

The "value" lookup uses `list_get_i64` on the MAP ptr with the KEY
string as the i64 index. Should be either:
1. `osty_rt_map_get_*(map, key, out)` proper map lookup, OR
2. `osty_rt_list_get_i64(values_list, i)` if iterating two parallel
   lists.

Where it lives: MIR generation for `for (k, v) in map` shape OR for
the bodied `Map.values` body. Touches `mir_lower.osty::mirLowerForIn`
which falls through to "non-List/Channel iterable not lowered yet" —
but downstream something IS producing this IR anyway.

**Blast radius**: Any test that uses Map iteration, including
indirect (e.g. tests that initialize a Map literal and then call
`println(m.len())` — `len` itself doesn't iterate but the
monomorphized Map's bodied methods may, and they all get injected
together).

## Root cause class B — bytes_get_v1 takes 4 args, called with 5

**Count**: ~3 failures (StdCrypto family).

**Error**: `'%tN' defined with type 'i64' but expected 'double'` at
specific positions in `osty_rt_list_get_bytes_v1` calls.

Less explored — likely a separate runtime signature drift between
declared shape and what the bodied byte-iteration emits.

## Affected tests (full list)

61 tests, names grouped by feature surface:

### Map + struct constructors (class A)
- TestLLVMBackendBinaryRunsMapLiteralInMain
- TestLLVMBackendBinaryRunsMapUpdateCanonicalPattern
- TestLLVMBackendBinaryBuiltinResultConstructorsTrackLocalContext
- TestLLVMBackendBinaryBuiltinResultFieldConstructors
- TestLLVMBackendBinaryKeepsMapKeysSortedAliveUnderGC
- TestLLVMBackendBinaryCollectionsUseRuntimeContainers
- TestLLVMBackendBinaryRunsCollectionToString
- TestLLVMBackendBinaryRunsResultMatchPayloadBinding

### Stdlib surfaces (class A — depends on Map iteration)
- TestLLVMBackendBinaryRunsStd{Zip,Xlsx,Image,Smtp,Kv,WatchDiff,
  Cmd,Os,Math,Random,Image,Keychain,Crypto,Compress,Term,Env*}
- TestLLVMBackendBinaryStdEnv* (10 variants)
- TestLLVMBackendBinaryStdCryptoMatchesGoReferenceMatrix

### GC / runtime (class A)
- TestBundledRuntimeDebugCollectRespectsRoots
- TestBundledRuntimeGenerationalStress
- TestBundledRuntimeIncrementalStress
- TestLLVMBackendBinaryAutoCollectsOnPressure
- TestLLVMBackendBinarySafepointsKeepManagedRootsAlive
- TestLLVMBackendBinaryManagedAggregateContainersSurvivePressureGC
- TestLLVMBackendBinaryManagedListLoopBreakAndContinueSurvivePressure
- TestLLVMBackendBinaryManagedTemporaryCallArgSurvivesPressure
- TestLLVMBackendBinaryForInOverTemporaryManagedListSurvivesPressure

### Generic / interface / language (class A)
- TestLLVMBackendBinaryRunsGenericIdentity
- TestLLVMBackendBinaryRunsGenericOwnerMethodTurbofish
- TestLLVMBackendBinaryRunsInterfaceBoxingDispatch
- TestLLVMBackendBinaryLetStructPatternDestructuring
- TestLLVMBackendBinaryMutReceiverMethodWritesBackToCaller

### Misc (class A)
- TestLLVMBackendBinaryPtrBackedListToSetAndBoolPrint
- TestLLVMBackendBinaryRunsBitwiseIntOps
- TestLLVMBackendBinaryRunsBreakAndContinueLoops
- TestLLVMBackendBinaryRunsBundledRuntime
- TestLLVMBackendBinaryRunsCharMethodInterpolation
- TestLLVMBackendBinaryRunsInjectedStdBytesFrom
- TestLLVMBackendBinaryRunsListContains
- TestLLVMBackendBinaryRunsListRemoveAt
- TestLLVMBackendBinaryMIRBackendStringCharsBytes
- TestLLVMBackendBinaryTestingPropertyGeneratorSubset

## What this means for PR #2020's CI gate

PR #2020 added `OSTY_REQUIRE_REAL_LLVM_EMISSION=1 go test -short
./internal/backend/`. `-short` excludes `parallelClangBackendTest` /
`requireClangForBackendTest` which gate every binary-link test. So
the gate runs IR-only tests and passes. Good for catching MIR-direct
emit regressions, blind to the class-A bug above (which only
surfaces at clang link).

**Future tightening (not done in this PR)**: a separate slower
workflow `requireClang + strict env` would catch class-A, but
running ALL 61 fails today would block every PR. The right
sequence is: fix class-A first, then add it to the gate.

## Why this is just a doc

Class-A is `mir_lower` / `lir_proto` deep work (likely Map iteration
intrinsic substitution or for-in lowering for non-List iterables).
Estimated 2-5 PRs to root-cause + fix without introducing new
regression. Documenting first preserves the audit trail for the
next attempt — reviewer-flagged pattern of "stop accumulating tech
debt by shipping inert PRs" applies here.

Next attempt should:
1. Reproduce the bug from `TestLLVMBackendBinaryRunsMapLiteralInMain`
   (smallest repro: just `m.len()` after `m.insert(k, v)`).
2. Trace which MIR node emits the `osty_rt_list_get_i64(map, key)`
   call — almost certainly inside a bodied stdlib Map method's
   for-loop.
3. Either special-case Map iteration in `mirLowerForIn` (currently
   bails for non-List) OR fix the bodied stdlib Map iteration to
   use proper map_get intrinsics.
