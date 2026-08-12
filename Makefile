.PHONY: test race vet build ui ui-check docs spike perf

GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

test:
	$(GO) test ./...

# The four documentation gates on their own. `make test` runs them too.
docs:
	$(GO) test ./internal/docset/...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p bin
	$(GO) build -o bin/gs ./cmd/gs
	$(GO) build -o bin/gitseq-mcp ./cmd/gitseq-mcp

ui:
	cd ui && npm run build

# The resident serves the committed embed, not ui/src directly. Rebuild before
# comparing so modified, deleted, and newly hashed assets all fail the gate.
ui-check: ui
	@if test -n "$$(git status --porcelain --untracked-files=normal -- internal/service/uidist)"; then \
		git status --short --untracked-files=normal -- internal/service/uidist; \
		echo "committed UI embed differs; run 'make ui' and commit the regenerated files" >&2; \
		exit 1; \
	fi

# Regenerate the stable six-case evidence without mixing it into shipping code.
spike:
	$(GO) run ./spike/cmd/gitseq-report

# Performance evidence is deliberately outside the ordinary correctness gate.
# PERF_ARGS selects the tier, output directory, or exact refs to compare.
perf:
	$(GO) run ./cmd/gitseq-perf $(PERF_ARGS)
