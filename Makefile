.PHONY: help build install uninstall run run-headless test test-fast vet lint vuln tidy-check fmt fmt-check clean
.DEFAULT_GOAL := help

# .exe on Windows, nothing elsewhere.
EXE := $(shell go env GOEXE)
BINARY := bin/detent$(EXE)
PROMPT ?= what files are in this directory?

# Where install puts it: Go's own bin directory by default, already on PATH
# with no sudo. Override for a system-wide one: sudo make install PREFIX=/usr/local/bin
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
	@echo ""
	@echo "On Windows, build, install, run and test work from any shell; the rest need Git Bash."

build:
	go build -o $(BINARY) ./cmd/detent

# go install, not cp: it adds .exe, makes the directory and needs no shell, so it
# works from cmd and PowerShell too. Exported per target, since VAR=x cmd is sh only.
# Your own config, under HOME or Windows' USERPROFILE. make's wildcard rather
# than a shell test, so the hint works from cmd too.
USERCONFIG := $(wildcard $(HOME)/.config/detent/config.y*ml $(USERPROFILE)/.config/detent/config.y*ml)

install: export GOBIN := $(PREFIX)
install:
	go install ./cmd/detent
	@echo "installed detent$(EXE) in $(PREFIX)"
	$(if $(USERCONFIG),,@echo "no config yet: detent -init writes one to fill in")

uninstall: export GOBIN := $(PREFIX)
uninstall:
	go clean -i ./cmd/detent
	@echo "removed detent$(EXE) from $(PREFIX)"

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
