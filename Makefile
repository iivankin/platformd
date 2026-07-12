.PHONY: build check frontend test

VERSION ?= 0.1.0-dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf development)
LDFLAGS = -X github.com/iivankin/platformd/internal/version.Version=$(VERSION) -X github.com/iivankin/platformd/internal/version.Commit=$(COMMIT)

frontend:
	bun --cwd=_frontend run build:web

check: frontend
	bun --cwd=_frontend run typecheck
	bun --cwd=_frontend run check
	go vet ./...

test: frontend
	bun --cwd=_frontend test
	go test ./...
	go test -race ./...

build: frontend
	mkdir -p dist
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/platformd .
