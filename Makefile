.PHONY: help build install uninstall run run-headless test test-fast vet lint vuln tidy-check fmt fmt-check clean
.DEFAULT_GOAL := help

BINARY := bin/detent
PROMPT ?= what files are in this directory?

# Where install puts it. Go's own bin directory by default, which is
# already on PATH and needs no sudo, override for a system-wide one:
#   sudo make install PREFIX=/usr/local/bin
PREFIX ?= $(shell go env GOBIN)
ifeq ($(PREFIX),)
PREFIX := $(shell go env GOPATH)/bin
endif

# Tracked files only, so gofmt never walks docs/ or bin/.
GOFILES = $$(git ls-files '*.go')

help:
	@echo "make build         build bin/detent"
	@echo "make install       build and put it in $(PREFIX)"
	@echo "make uninstall     remove it from there"
	@echo "make run           launch the TUI (go run, no build step)"
	@echo "make run-headless  run one request headlessly, PROMPT=\"...\" sets it"
	@echo "make test          go test -race ./..., as CI runs it"
	@echo "make test-fast     go test ./..., without the race detector"
	@echo "make vet           go vet ./..."
	@echo "make lint          golangci-lint run, with .golangci.yml"
	@echo "make vuln          govulncheck ./..."
	@echo "make tidy-check    fail if go.mod or go.sum is not tidy"
	@echo "make fmt           gofmt -w every tracked .go file"
	@echo "make fmt-check     fail if any tracked file isn't gofmt'd"
	@echo "make clean         remove bin/"

build:
	go build -o $(BINARY) ./cmd/detent

install: build
	@mkdir -p $(PREFIX)
	install -m 755 $(BINARY) $(PREFIX)/detent
	@echo "installed $(PREFIX)/detent"
	@command -v detent >/dev/null || echo "note: $(PREFIX) is not on your PATH"

uninstall:
	rm -f $(PREFIX)/detent
	@echo "removed $(PREFIX)/detent"

run:
	@go run ./cmd/detent

run-headless:
	@go run ./cmd/detent -prompt "$(PROMPT)"

test:
	go test -race ./...

test-fast:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@test -z "$$(gofmt -l $(GOFILES))" || (echo "not gofmt'd:"; gofmt -l $(GOFILES); exit 1)

clean:
	rm -rf bin
