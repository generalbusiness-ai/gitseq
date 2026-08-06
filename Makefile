.PHONY: test race vet build

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
