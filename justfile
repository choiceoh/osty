set shell := ["bash", "-cu"]

bin := ".bin/osty"
front_packages := "./internal/lexer ./internal/parser ./internal/resolve ./internal/check ./internal/diag ./internal/format ./internal/lint ./internal/pipeline ./internal/speccorpus"

default:
    just front

build:
    mkdir -p .bin
    go build -o {{bin}} ./cmd/osty

front:
    go test -count=1 {{front_packages}}

spec:
    go test -count=1 ./internal/speccorpus -v

short:
    go test -short ./...

full:
    go test ./...

lsp test=".":
    go test -count=1 ./internal/lsp -run '{{test}}' -v

cmd test=".":
    go test -count=1 ./cmd/osty -run '{{test}}' -v

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
    watchexec -e go,osty,md -- just front

watch-pipe target:
    watchexec -e go,osty -- just pipe {{target}}

sum:
    gotestsum -- ./...
