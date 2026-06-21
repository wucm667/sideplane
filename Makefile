.PHONY: all build generate test test-race vet fmt web web-dev typecheck lint openapi-check smoke release-local release-dist clean docker install

# RELEASE_PLATFORMS mirrors the .github/workflows/release.yml build matrix.
RELEASE_PLATFORMS ?= linux/amd64 linux/arm64
RELEASE_CMDS ?= sideplane-server sideplane-sidecar sideplane

GO ?= go
BIN_DIR ?= bin
DIST_DIR ?= dist
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

generate:
	cd web && npm run generate:api

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

smoke:
	scripts/smoke-readonly.sh

release-local: web
	mkdir -p $(DIST_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/sideplane-server ./cmd/sideplane-server
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/sideplane-sidecar ./cmd/sideplane-sidecar
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/sideplane ./cmd/sideplane

# release-dist cross-compiles the release matrix locally and writes checksummed
# binaries plus a SHA256SUMS file into dist/. It never pushes or publishes; the
# git tag and GitHub release remain a manual maintainer step. The Web UI is
# rebuilt first so each server binary embeds current assets.
release-dist:
	cd web && npm run build
	mkdir -p $(DIST_DIR)
	@for platform in $(RELEASE_PLATFORMS); do \
		goos="$${platform%/*}"; goarch="$${platform#*/}"; \
		for cmd in $(RELEASE_CMDS); do \
			out="$(DIST_DIR)/$${cmd}_$${goos}_$${goarch}"; \
			echo "building $$out"; \
			GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$$out" ./cmd/$$cmd || exit 1; \
		done; \
	done
	cd $(DIST_DIR) && rm -f SHA256SUMS && \
		if command -v sha256sum >/dev/null 2>&1; then sha256sum sideplane* > SHA256SUMS; \
		else shasum -a 256 sideplane* > SHA256SUMS; fi
	@echo "wrote $(DIST_DIR)/SHA256SUMS"

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) web/dist
	mkdir -p web/dist
	touch web/dist/.gitkeep

docker:
	docker compose -f deployments/docker-compose/docker-compose.yml build

install:
	sh install.sh
