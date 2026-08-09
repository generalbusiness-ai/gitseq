.PHONY: test race vet build ui ui-check

GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

test:
	cd spike && $(GO) test ./...

race:
	cd spike && $(GO) test -race ./...

vet:
	cd spike && $(GO) vet ./...

build:
	mkdir -p bin
	cd spike && $(GO) build -o ../bin/gs ./cmd/gs
	cd spike && $(GO) build -o ../bin/gitseq-mcp ./cmd/gitseq-mcp

ui:
	cd ui && npm run build

# The resident serves the committed embed, not ui/src directly. Rebuild before
# comparing so modified, deleted, and newly hashed assets all fail the gate.
ui-check: ui
	@if test -n "$$(git status --porcelain --untracked-files=normal -- spike/internal/service/uidist)"; then \
		git status --short --untracked-files=normal -- spike/internal/service/uidist; \
		echo "committed UI embed differs; run 'make ui' and commit the regenerated files" >&2; \
		exit 1; \
	fi
