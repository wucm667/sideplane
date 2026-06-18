.PHONY: all build test test-race vet fmt web web-dev typecheck lint clean docker install

GO ?= go
BIN_DIR ?= bin

all: lint test build

web:
	cd web && npm ci && npm run build

build: web
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/sideplane-server ./cmd/sideplane-server
	$(GO) build -o $(BIN_DIR)/sideplane-sidecar ./cmd/sideplane-sidecar
	$(GO) build -o $(BIN_DIR)/sideplane ./cmd/sideplane

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

web-dev:
	cd web && npm install && npm run dev

typecheck:
	cd web && npm run typecheck

lint:
	$(GO) vet ./...
	cd web && npm run typecheck

clean:
	rm -rf $(BIN_DIR) web/dist
	mkdir -p web/dist
	touch web/dist/.gitkeep

docker:
	docker compose -f deployments/docker-compose/docker-compose.yml build

install:
	sh install.sh
