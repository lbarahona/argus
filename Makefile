BINARY := argus
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build install clean test lint

build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/argus/

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/argus/

clean:
	rm -rf bin/

test:
	go test ./... -v

lint:
	golangci-lint run ./...
