.PHONY: build check frontend runtime test

VERSION ?= 0.1.0-dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf development)
LDFLAGS = -X github.com/iivankin/platformd/internal/version.Version=$(VERSION) -X github.com/iivankin/platformd/internal/version.Commit=$(COMMIT)
GO_TAGS = containers_image_openpgp exclude_graphdriver_btrfs exclude_graphdriver_zfs grpcnotrace seccomp sqlite_omit_load_extension

frontend:
	bun --cwd=_frontend run build:web

runtime:
	cargo build --release --locked --manifest-path internal/objectstore/sidecar/Cargo.toml
	cargo build --release --locked --manifest-path telemetry/Cargo.toml
	mkdir -p dist/runtime
	install -m 0755 internal/objectstore/sidecar/target/release/platformd-objectstore dist/runtime/platformd-objectstore
	install -m 0755 telemetry/target/release/platformd-telemetry dist/runtime/platformd-telemetry

check: frontend
	bun --cwd=_frontend run typecheck
	bun --cwd=_frontend run check
	go vet -tags "$(GO_TAGS)" ./...

test: frontend
	bun --cwd=_frontend test
	go test -tags "$(GO_TAGS)" ./...
	go test -race -tags "$(GO_TAGS)" ./...

build: frontend runtime
	mkdir -p dist
	CGO_ENABLED=1 go build -trimpath -tags "$(GO_TAGS)" -ldflags "$(LDFLAGS)" -o dist/platformd .
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/platformd-forward ./cmd/platformd-forward
