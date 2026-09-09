#!/bin/bash
set -o pipefail
cd /Users/hughpyle/play/gitseq-worktrees/causal-trailer-equality || exit 1
echo "== head $(git rev-parse HEAD)"
echo "== gofmt"; gofmt -l ./internal ./cmd; echo "rc=$?"
echo "== vet"; go vet ./... 2>&1 | tail -3; echo "rc=${PIPESTATUS[0]}"
echo "== build"; go build ./... ; echo "rc=$?"
echo "== test all"; go test -count=1 ./... 2>&1 | grep -v '^ok\|no test files' ; echo "rc=${PIPESTATUS[0]}"
echo "== race intent+kernel"; go test -race -count=1 ./internal/intent/... ./internal/kernel/... 2>&1 | tail -3; echo "rc=${PIPESTATUS[0]}"
echo "== docset"; go test -count=1 ./internal/docset/... 2>&1 | tail -2; echo "rc=${PIPESTATUS[0]}"
echo "== diff-check"; git diff --check; echo "rc=$?"
echo "== DONE"
