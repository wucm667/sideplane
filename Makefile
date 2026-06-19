.PHONY: all build test test-race vet fmt web web-dev typecheck lint openapi-check clean docker install

GO ?= go
BIN_DIR ?= bin
OPENAPI_PYYAML_VERSION ?= 6.0.2

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
