set shell := ["bash", "-cu"]

bin := ".bin/osty"
checker_bin := ".osty/bin/osty-native-checker"
front_packages := "./internal/lexer ./internal/parser ./internal/resolve ./internal/check ./internal/diag ./internal/format ./internal/lint ./internal/pipeline"
test_flags := "-count=1 -vet=off"

default: front

help:
    just --list

build:
    mkdir -p .bin
    go build -o {{bin}} ./cmd/osty

build-checker:
    mkdir -p .osty/bin
    go build -o {{checker_bin}} ./cmd/osty-native-checker

build-all: build build-checker

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
    go test {{test_flags}} {{front_packages}}

spec:
    go test {{test_flags}} ./internal/speccorpus -v

short:
    go test {{test_flags}} -short ./...

full:
    go test {{test_flags}} ./...

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

ci: build
    {{bin}} ci .

verify-selfhost:
    go test {{test_flags}} -run 'SnapshotParity|CoreSnapshotParity' ./internal/ci ./internal/runner

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
    if command -v gotestsum >/dev/null 2>&1; then gotestsum -- {{pkg}}; else go test {{test_flags}} {{pkg}}; fi
