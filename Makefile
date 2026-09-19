.PHONY: help build run run-cli test vet fmt fmt-check clean

BINARY := bin/detent
GOAL   ?= what files are in this directory?

help:
	@echo "make build     build bin/detent"
	@echo "make run       launch the TUI (go run, no build step)"
	@echo "make run-cli   run the plain stdin/print flow — GOAL=\"...\" to set the goal"
	@echo "make test      go test ./..."
	@echo "make vet       go vet ./..."
	@echo "make fmt       gofmt -w every .go file"
	@echo "make fmt-check fail if any file isn't gofmt'd"
	@echo "make clean     remove bin/"

build:
	go build -o $(BINARY) ./cmd/detent

run:
	@go run ./cmd/detent

run-cli:
	@go run ./cmd/detent -cli -goal "$(GOAL)"

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
