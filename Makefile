VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/gscode1/gs-acm-utils/internal/cli.version=$(VERSION)
BIN     := bin/acm-mock-resolver

.PHONY: build test lint fmt bench tidy

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/acm-mock-resolver

test:
	go test -race ./...

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

bench:
	go test -run '^$$' -bench . -benchmem ./...

tidy:
	go mod tidy
