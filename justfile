set shell := ["bash", "-cu"]

bin := ".bin/osty"
checker_bin := ".osty/bin/osty-native-checker"
front_packages := "./internal/lexer ./internal/parser ./internal/resolve ./internal/check ./internal/diag ./internal/format ./internal/lint ./internal/pipeline"
backend_packages := "./internal/ir ./internal/mir ./internal/backend ./internal/llvmgen"
osty_test_dirs := "examples/int_control_e2e examples/int_methods_e2e examples/int_struct_e2e"
test_flags := "-count=1 -vet=off"
stdlib_matrix_fast_tests := "TestStdlibSupportMatrix"
stdlib_matrix_backend_tests := "Test(Stdlib(CheckResult|Symbol|Method)|InjectReachableStdlib|ReachableStdlib|Phase2|LLVMBackendBinaryRunsStd(Zip|Xlsx|Image|Smtp|Crypto|Random|Term|Os)|PrepareEntryRewritesStdEncoding)"
stdlib_matrix_llvmgen_tests := "Test(Std(Env|Io|Term|Strings|Bytes|Crypto)|UnsupportedDiagnostic)"
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
    go test {{test_flags}} ./internal/llvmgen -run '{{stdlib_matrix_llvmgen_tests}}' -v

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
    set -euo pipefail
    test -x {{bin}} || just build
    for dir in {{osty_test_dirs}}; do {{bin}} test --seed 0x1 --serial "$dir"; done

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
    while IFS= read -r file; do case "$file" in testdata/spec/negative/*|internal/airepair/testdata/corpus/*.input.osty) continue ;; esac; {{bin}} repair --check "$file"; done < <(git ls-files '*.osty')

# Capture residual airepair cases into tmp/airepair-cases/ and rewrite
# todo-airepair.md with the ranked learning backlog. Non-blocking.
airepair-capture: build
    bash scripts/airepair-update-backlog.sh {{bin}}

ci: build
    {{bin}} ci .

verify-selfhost:
    go test {{test_flags}} -run 'SnapshotParity|CoreSnapshotParity' ./internal/ci ./internal/runner

verify-self-rebuild: build-all
    bash scripts/verify-self-rebuild {{bin}}

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

prepush: fmt-check vet repair-check airepair-capture ci

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
