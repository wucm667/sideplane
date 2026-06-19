.PHONY: all build test test-race vet fmt web web-dev typecheck lint openapi-check clean docker install

GO ?= go
BIN_DIR ?= bin
OPENAPI_PYYAML_VERSION ?= 6.0.2
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILDINFO_PKG := github.com/wucm667/sideplane/internal/buildinfo
LDFLAGS := -X $(BUILDINFO_PKG).Version=$(VERSION) -X $(BUILDINFO_PKG).Commit=$(COMMIT) -X $(BUILDINFO_PKG).BuildDate=$(BUILD_DATE)

all: lint test build

web:
	cd web && npm ci && npm run build

build: web
	mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/sideplane-server ./cmd/sideplane-server
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/sideplane-sidecar ./cmd/sideplane-sidecar
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/sideplane ./cmd/sideplane

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

openapi-check:
	python3 -c "import yaml" 2>/dev/null || python3 -m pip install --user PyYAML==$(OPENAPI_PYYAML_VERSION)
	python3 -c "import yaml; yaml.safe_load(open('docs/openapi.yaml'))"

clean:
	rm -rf $(BIN_DIR) web/dist
	mkdir -p web/dist
	touch web/dist/.gitkeep

docker:
	docker compose -f deployments/docker-compose/docker-compose.yml build

install:
	sh install.sh
