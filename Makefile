BINARY  := wisanggeni
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# golangci-lint is not vendored; prefer whichever copy is on PATH and fall back
# to the one installed by `go install` in this toolchain's GOPATH.
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo $(shell go env GOPATH)/bin/golangci-lint)

.PHONY: all build test lint fmt vet clean

all: lint test build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

test:
	go test -race ./...

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

clean:
	rm -rf bin
