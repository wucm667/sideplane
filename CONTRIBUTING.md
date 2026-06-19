# Contributing To Sideplane

Thanks for helping harden Sideplane. This project is security-sensitive control-plane infrastructure for self-hosted AI agent fleets, so contributions should keep operator safety, auditability, and rollback behavior boring and explicit.

## Local Setup

Required tools:

- Go 1.24 or newer.
- Node.js and npm for the React/Vite web UI.
- Docker Compose if you want to validate the packaged compose deployment.
- Python 3 with PyYAML for OpenAPI checks; `make openapi-check` installs the pinned PyYAML version when needed.

Install web dependencies:

```bash
cd web
npm ci
```

Run the server locally:

```bash
export SIDEPLANE_OPERATOR_TOKEN='replace-with-a-long-random-token'
go run ./cmd/sideplane-server --db ./sideplane.db
```

Run the Vite development UI:

```bash
cd web
npm run dev
```

## Common Workflow

Use the Makefile where possible:

```bash
make lint
make test
make build
```

Useful focused commands:

```bash
go test ./...
go test -race ./internal/store ./internal/server ./internal/sidecar
cd web && npm run test
cd web && npm run typecheck
cd web && npm run build
make openapi-check
scripts/smoke-readonly.sh
```

Before sending a PR, run the smallest focused test for the code you changed and at least one broader gate, usually `go test ./...` plus the relevant web command.

## No Live Writes In Tests

Tests must not:

- Write real Hermes or OpenClaw configuration.
- Restart real services, systemd units, Docker containers, local agents, VPS processes, or LAN machines.
- SSH anywhere.
- Touch real operator machines.
- Read, copy, or commit private hostnames, private IPs, real paths, tokens, credentials, or real config contents.

Use temp directories, fake adapters/controllers, httptest servers, or mock command runners for restart, rollback, install, and sidecar apply behavior.

## Product Boundaries

Sideplane is a control plane for self-hosted AI agent fleets. It should help operators know what each node is running, safely change configuration, and recover when something goes wrong.

Keep contributions focused on:

- Fleet inventory and runtime status.
- Heartbeats, probes, drift, config diffs, signed config apply, restart, rollback, audit, metrics, packaging, and operator workflows.

Avoid turning Sideplane into:

- A chat UI.
- A prompt workspace.
- An autonomous task board.
- A marketplace.
- A generic multi-agent collaboration product.
- A Kubernetes-first platform.

## Code Style

- Format Go with `gofmt`.
- Keep shell scripts POSIX-compatible unless the file already declares another shell.
- Keep the web UI compact and operator-focused.
- Do not edit generated files by hand. For OpenAPI TypeScript types, update `docs/openapi.yaml`, run `cd web && npm run generate:api`, and commit both source and generated changes.
- Prefer small, explicit interfaces and deterministic behavior over clever automation.
- Add tests around config merge/diff/validation, signing, job state transitions, rollback, path safety, request limits, and API/CLI contracts when those areas change.

## Local AI Guidance Files

`AGENTS.md`, `CLAUDE.md`, and `todo.md` are intentionally gitignored local guidance files. Do not add or commit them.

Also do not commit `.sideplane/`, `.claude/`, `bin/`, `web/dist/`, `web/node_modules/`, `web/prototype/`, `web/tsconfig.tsbuildinfo`, or local prompt/input scratch files.

## Security Reports

Do not report vulnerabilities in public issues or PR comments. Follow [SECURITY.md](SECURITY.md) for private reporting guidance.
