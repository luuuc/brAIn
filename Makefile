.PHONY: build test clean install lint ci run

VERSION ?= dev
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -ldflags="-s -w -X 'github.com/luuuc/brain/internal/version.Version=$(VERSION)'"

build:
	go build $(LDFLAGS) -trimpath -o bin/brain ./cmd/brain

test:
	go test -v ./...

clean:
	rm -rf bin/ dist/

install: build
	cp bin/brain /usr/local/bin/brain

lint:
	@command -v golangci-lint >/dev/null 2>&1 || \
		(echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	@PATH="$$PATH:$$(go env GOPATH)/bin" golangci-lint run

ci: build test lint
	@echo "All CI checks passed!"

run:
	go run ./cmd/brain $(ARGS)
