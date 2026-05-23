set shell := ["bash", "-cu"]

bin := ".bin/osty"
checker_bin := ".osty/bin/osty-native-checker"
front_packages := "./internal/lexer ./internal/parser ./internal/resolve ./internal/check ./internal/diag ./internal/format ./internal/lint ./internal/pipeline"
backend_packages := "./internal/ir ./internal/mir ./internal/backend ./internal/nativellvmgen ./internal/llvmabi ./internal/toolchain ./cmd/osty-native-llvmgen"
osty_test_dirs := "examples/int_control_e2e examples/int_methods_e2e examples/int_struct_e2e"
test_flags := "-count=1 -vet=off"
stdlib_matrix_fast_tests := "TestStdlibSupportMatrix"
stdlib_matrix_backend_tests := "Test(Stdlib(CheckResult|Symbol|Method)|InjectReachableStdlib|ReachableStdlib|Phase2|LLVMBackendBinaryRunsStd(Zip|Xlsx|Image|Smtp|Crypto|Random|Term|Os)|PrepareEntryRewritesStdEncoding)"
native_llvm_backend_tests := "Test(LLVMBackendBinaryStd(Io|Env)|LLVMBackendBinaryRunsStd(Term|Crypto)|LLVMBackendBinaryStdCrypto|EmitLLVMIRTextPrefersNativeOwned|LLVMBackendEmitBinaryPrefersNativeOwned|UseNativeOwnedLLVMIR|LLVMBackendDispatchTraceReportsSelectedRoute|LLVMBackendMissingMIRDoesNotRetryLegacyIRBridge)"
native_llvmgen_cmd_tests := "TestRun(EmitsLLVMIRForMIRPayload|MIRPayloadPrefersLIRProtoWhenSelected|MIRPayloadDeclinesWhenLIRProtoDeclines|RejectsInvalidJSON)"
native_llvmgen_exec_tests := "Test(TrySourceUsesEnvBinaryAndDecodesResponse|TryPackageUsesManagedBinaryWhenEnvUnset|RequestFromMIREncodesPayload)"
native_llvmabi_tests := "TestUnsupported"
toolchain_native_llvmgen_tests := "Test(EnsureNativeLLVMGen|ManagedNativeLLVMGenPath)"
selfhost_matrix_fast_tests := "Test(CheckCLIDefaultPathExitsZero|RunCheckFileDefaultPathIsAstbridgeFree|ProductionFrontendPathsDoNotCallFrontendRunFile)"
selfhost_matrix_cmd_tests := "Test(Run(Check|Typecheck|Resolve)(File|Package|Workspace).*AstbridgeFree|CheckCLI(DefaultPathExitsZero|Native.*)|TypecheckCLI.*|ResolveCLI.*)"
selfhost_matrix_core_tests := "Test(ParseSnapshot|CheckSnapshot|CheckStructuredFromRunIsAstbridgeFree|CheckPackageStructuredIsAstbridgeFree|CheckDiagnosticsAsDiagIsAstbridgeFree|ProductionFrontendPathsDoNotCallFrontendRunFile)"

# Osty-first front-end loop.
default: front

help:
    just --list

build:
    mkdir -p .bin
    go build -o {{bin}} ./cmd/osty

build-checker:
    mkdir -p .osty/bin
    go build -o {{checker_bin}} ./cmd/osty-native-checker

build-lirproto:
    mkdir -p .osty/bin
    go build -o .osty/bin/osty-native-lirproto ./cmd/osty-native-lirproto

build-all: build build-checker build-lirproto

# bootstrap performs the end-to-end fresh-clone bootstrap:
#
#   1. build host osty + native-checker + native-lirproto.
#   2. invoke `osty install-self`, which builds osty-self from
#      toolchain/ and promotes the result into the
#      `.osty/cache/self-host/` content-addressed cache.
#
# Subsequent compiles look the binary up via
# `selfhostcache.ResolveBinary` instead of re-running the slow
# toolchain build.
#
# OSTY_STAGE0_FALLBACK=1 is baked in: post-PR #1954, the production
# native-checker build path gates on a resolvable `osty-self`, which
# fresh clones do not have. The env var (introduced by #1980, wired
# through buildNativeChecker by #1988) tells install-self to take the
# stage0 source-bootstrap path AND tells buildNativeChecker to detour
# to `go build ./cmd/osty-native-checker` so the chicken-and-egg never
# triggers. Override on the command line (`OSTY_STAGE0_FALLBACK= just
# bootstrap`) when you want to verify the prebuilt-only path instead.
bootstrap: build-all
    # `OSTY_STDLIB_BODY_LOWER=0` is the escape hatch from PR #1998's
    # default-on flip. The stage0 source bootstrap path is sensitive to
    # bodied stdlib injection because the toolchain itself calls into
    # bodied methods (e.g. `Map<String, Int>.update`) whose injected
    # form collides with the builtin-receiver dispatcher's intrinsic
    # path — the symbol falls through to `mangleMethodSymbol(typeName,
    # method)` (`_ZTSN…MapISslEE__update`) without a matching `define`.
    # Disabling injection here keeps the existing bootstrap shape
    # working until the conflict between `builtinNonGenericMethods` +
    # `RewriteStdlibMethodCallsites` is resolved at the IR layer; user
    # `osty build` continues to get the default-on path.
    OSTY_STAGE0_FALLBACK=1 OSTY_STDLIB_BODY_LOWER=0 {{bin}} install-self

# cache-self prints the canonical .osty/cache/self-host/<sha>-<triple>/
# osty-self path for the current toolchain SHA + host triple. With
# `--check` it doubles as a fast cache-hit probe for shell tooling.
cache-self *args: build
    {{bin}} cache-self {{args}}

# gc-self prunes stale entries from .osty/cache/self-host/. Default
# policy keeps the current toolchain SHA plus the 5 most recently
# modified other entries. Pass `--dry-run` first to preview the plan.
gc-self *args: build
    {{bin}} gc-self {{args}}

# Cross-compile osty (+ native-checker) for every supported host triple.
# Targets: linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/{amd64,arm64}.
# Artifacts land in .bin/cross/<goos>-<goarch>/.
cross:
    #!/usr/bin/env bash
    set -euo pipefail
    targets=(
        "linux/amd64"
        "linux/arm64"
        "darwin/amd64"
        "darwin/arm64"
        "windows/amd64"
        "windows/arm64"
    )
    for t in "${targets[@]}"; do
        goos="${t%/*}"
        goarch="${t#*/}"
        out=".bin/cross/${goos}-${goarch}"
        mkdir -p "$out"
        suffix=""
        if [ "$goos" = "windows" ]; then suffix=".exe"; fi
        echo ">> building osty for $goos/$goarch"
        CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -o "$out/osty$suffix" ./cmd/osty
        echo ">> building osty-native-checker for $goos/$goarch"
        CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -o "$out/osty-native-checker$suffix" ./cmd/osty-native-checker
    done

cross-one goos goarch:
    #!/usr/bin/env bash
    set -euo pipefail
    out=".bin/cross/{{goos}}-{{goarch}}"
    mkdir -p "$out"
    suffix=""
    if [ "{{goos}}" = "windows" ]; then suffix=".exe"; fi
    CGO_ENABLED=0 GOOS="{{goos}}" GOARCH="{{goarch}}" go build -o "$out/osty$suffix" ./cmd/osty
    CGO_ENABLED=0 GOOS="{{goos}}" GOARCH="{{goarch}}" go build -o "$out/osty-native-checker$suffix" ./cmd/osty-native-checker

front:
    just osty
    go test {{test_flags}} {{front_packages}}

spec:
    go test {{test_flags}} ./internal/speccorpus -v

short:
    just osty
    go list ./... | rg -v '^github\.com/osty/osty/benchmarks/' | xargs go test {{test_flags}} -short

full:
    just osty
    go test {{test_flags}} ./...

quick: verify-fast

medium: verify-medium

verify-fast:
    just support-matrix-fast
    go test {{test_flags}} {{front_packages}}

verify-medium:
    just support-matrix-medium
    just short

verify-full:
    just full
    just support-host-matrix

support-matrix-fast:
    just support-stdlib-fast
    just support-selfhost-fast

support-matrix-medium:
    just support-stdlib-medium
    just support-selfhost-medium

support-stdlib-fast:
    go test {{test_flags}} ./internal/stdlib -run '{{stdlib_matrix_fast_tests}}' -v

support-stdlib-medium:
    just support-stdlib-fast
    go test {{test_flags}} ./internal/stdlib
    go test {{test_flags}} ./internal/backend -run '{{stdlib_matrix_backend_tests}}' -v
    go test {{test_flags}} ./internal/llvmabi -run '{{native_llvmabi_tests}}' -v
    go test {{test_flags}} ./cmd/osty-native-llvmgen -run '{{native_llvmgen_cmd_tests}}' -v
    go test {{test_flags}} ./internal/nativellvmgen -run '{{native_llvmgen_exec_tests}}' -v
    go test {{test_flags}} ./internal/toolchain -run '{{toolchain_native_llvmgen_tests}}' -v
    go test {{test_flags}} ./internal/backend -run '{{native_llvm_backend_tests}}' -v

support-selfhost-fast:
    go test {{test_flags}} ./cmd/osty ./internal/selfhost -run '{{selfhost_matrix_fast_tests}}' -v

support-selfhost-medium:
    just support-selfhost-fast
    just verify-selfhost
    go test {{test_flags}} ./cmd/osty -run '{{selfhost_matrix_cmd_tests}}' -v
    go test {{test_flags}} ./internal/selfhost -run '{{selfhost_matrix_core_tests}}' -v

support-host-matrix:
    just cross

osty: build verify-selfhost
    {{bin}} ci .
    just osty-tests

osty-tests:
    #!/usr/bin/env bash
    set -euo pipefail
    test -x {{bin}} || just build
    # Example packages compile through the LLVM + mir-direct path, which
    # requires a cached `osty-self` (see `just bootstrap` / `osty install-self`).
    # Fresh checkouts should still pass `just front` / `just short` without a
    # multi-minute toolchain build.
    if ! {{bin}} cache-self --check >/dev/null 2>&1; then
        echo "skip osty-tests: no selfhostcache osty-self (run \`just bootstrap\` to enable)" >&2
        exit 0
    fi
    for dir in {{osty_test_dirs}}; do {{bin}} test --seed 0x1 "$dir"; done

test pkg="./...":
    go test {{test_flags}} {{pkg}}

vet:
    go vet ./...

fmt:
    gofmt -w $(git ls-files '*.go')

fmt-check:
    unformatted="$(gofmt -l $(git ls-files '*.go'))"; if [ -n "$unformatted" ]; then printf '%s\n' "$unformatted"; exit 1; fi

lsp test=".":
    go test {{test_flags}} ./internal/lsp -run '{{test}}' -v

cmd test=".":
    go test {{test_flags}} ./cmd/osty -run '{{test}}' -v

gen test=".":
    go test {{test_flags}} ./cmd/osty -run '{{test}}' -v

diag test=".":
    go test {{test_flags}} ./internal/diag -run '{{test}}' -v

repair-check: build
    bash scripts/repair-check.sh {{bin}}

# Capture residual airepair cases into tmp/airepair-cases/ and rewrite
# todo-airepair.md with the ranked learning backlog. Non-blocking.
airepair-capture: build
    bash scripts/airepair-update-backlog.sh {{bin}}

ci: build
    {{bin}} ci .

verify-selfhost:
    go test {{test_flags}} -run 'SnapshotParity|CoreSnapshotParity' ./internal/ci ./internal/runner

verify-self-rebuild: build-all
    bash scripts/verify-self-rebuild --reuse-stage1 {{bin}}

verify-self-rebuild-fast: build-all
    bash scripts/verify-self-rebuild --skip-gates --reuse-stage1 {{bin}}

verify-self-rebuild-gates: build-all
    bash scripts/verify-self-rebuild --gates-only {{bin}}

verify-self-rebuild-ir: build
    bash scripts/verify-self-rebuild --skip-gates --ir-only {{bin}}

verify-self-rebuild-stage1: build
    bash scripts/verify-self-rebuild --skip-gates --stage1-only {{bin}}

backend-loop: build-all
    rm -rf toolchain/.osty
    go test {{test_flags}} {{backend_packages}}
    bash scripts/verify-self-rebuild --skip-gates --reuse-stage1 {{bin}}

backend-loop-gates: build-all
    rm -rf toolchain/.osty
    go test {{test_flags}} {{backend_packages}}
    bash scripts/verify-self-rebuild --gates-only {{bin}}
    bash scripts/verify-self-rebuild --skip-gates --reuse-stage1 {{bin}}

check: fmt-check vet front

prepush: fmt-check vet repair-check ci

pipe target:
    test -x {{bin}} || just build
    {{bin}} pipeline --per-decl {{target}}

pipe-gen target:
    test -x {{bin}} || just build
    {{bin}} pipeline --per-decl --gen {{target}}

profile target:
    test -x {{bin}} || just build
    mkdir -p .profiles
    {{bin}} pipeline --per-decl --cpuprofile .profiles/cpu.pprof --memprofile .profiles/mem.pprof {{target}}

watch-front:
    watchexec -e go,osty,md --restart -- just front

watch-short:
    watchexec -e go,osty,md --restart -- just short

watch-pipe target:
    watchexec -e go,osty --restart -- just pipe {{target}}

sum pkg="./...":
    if command -v gotestsum >/dev/null 2>&1; then gotestsum -- {{test_flags}} {{pkg}}; else go test {{test_flags}} {{pkg}}; fi
