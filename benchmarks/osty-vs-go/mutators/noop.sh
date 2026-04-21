#!/usr/bin/env bash
# Smoke mutator for osty-vs-go --autoresearch. Writes a timestamp to
# a scratch file so the working tree is always non-empty after the
# mutator runs, exercising the commit-on-keep / reset-on-discard path
# without actually changing anything the bench measures.
#
# Use this to verify the autoresearch loop wiring end-to-end before
# plugging in a real compiler mutator.
set -euo pipefail
scratch="benchmarks/osty-vs-go/mutators/.scratch"
mkdir -p "$(dirname "$scratch")"
date -u +"%Y-%m-%dT%H:%M:%SZ // autoresearch noop tick" >>"$scratch"
