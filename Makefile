.PHONY: build test fmt web

GO ?= go
BIN_DIR ?= bin

web:
	cd web && npm ci && npm run build

build: web
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/sideplane-server ./cmd/sideplane-server
	$(GO) build -o $(BIN_DIR)/sideplane-sidecar ./cmd/sideplane-sidecar
	$(GO) build -o $(BIN_DIR)/sideplane ./cmd/sideplane

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...
