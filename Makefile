.PHONY: test race vet build ui spike

GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

test:
	$(GO) test ./...

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

# Regenerate the stable six-case evidence without mixing it into shipping code.
spike:
	$(GO) run ./spike/cmd/gitseq-report
