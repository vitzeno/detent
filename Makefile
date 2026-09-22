.PHONY: help build install uninstall run run-headless test vet fmt fmt-check clean

BINARY := bin/detent
GOAL   ?= what files are in this directory?

# Where install puts it. Go's own bin directory by default, which is
# already on PATH and needs no sudo; override for a system-wide one:
#   sudo make install PREFIX=/usr/local/bin
PREFIX ?= $(shell go env GOBIN)
ifeq ($(PREFIX),)
PREFIX := $(shell go env GOPATH)/bin
endif

help:
	@echo "make build       build bin/detent"
	@echo "make install     build and put it in $(PREFIX)"
	@echo "make uninstall   remove it from there"
	@echo "make run         launch the TUI (go run, no build step)"
	@echo "make run-headless run one goal headlessly — GOAL=\"...\" to set the goal"
	@echo "make test        go test ./..."
	@echo "make vet       go vet ./..."
	@echo "make fmt       gofmt -w every .go file"
	@echo "make fmt-check fail if any file isn't gofmt'd"
	@echo "make clean     remove bin/"

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
	@go run ./cmd/detent -goal "$(GOAL)"

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "not gofmt'd:"; gofmt -l .; exit 1)

clean:
	rm -rf bin
