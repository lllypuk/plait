BINARY := plait
BUILD_DIR := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null || echo "$$HOME/go/bin/golangci-lint")
GOVULNCHECK := $(shell command -v govulncheck 2>/dev/null || echo "$$HOME/go/bin/govulncheck")

.PHONY: build test fmt fmt-check lint vuln check clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/plait

test:
	go test -race ./...

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint:
	$(GOLANGCI_LINT) run

vuln:
	$(GOVULNCHECK) ./...

check: fmt-check test lint vuln

clean:
	rm -rf $(BUILD_DIR)
