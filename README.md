# Sideplane

Open-source control plane for self-hosted AI agent fleets.

Sideplane is for solo operators, one-person companies, and small teams running AI agents across local PCs, LAN servers, and VPS nodes. It starts with Hermes Agent and OpenClaw, with a focus on safe configuration, lifecycle management, visibility, and rollback.

> Know what every self-hosted agent is running, safely change its configuration, and recover when a change goes wrong.

## Status

Sideplane is in early project initialization.

The repository is currently defining product scope, architecture, and the first implementation path. Production use is not ready yet.

## Why

Running multiple self-hosted agents quickly creates an operations problem:

- Each node may run different agent runtimes.
- Model and provider settings drift across machines.
- Updating configuration often means SSHing into every server.
- Letting an agent modify its own critical configuration can be risky.
- Upgrades and restarts need backup, validation, health checks, and rollback.

Sideplane aims to provide a small, self-hosted control plane for that problem.

## What Sideplane Will Manage

Initial runtime targets:

- Hermes Agent
- OpenClaw

Initial management capabilities:

- Fleet inventory
- Node and agent heartbeat
- Runtime status
- Current provider/model visibility
- Configuration diff
- Safe configuration apply
- Restart
- Rollback
- Audit trail

Sideplane is not intended to be a chat UI, prompt workspace, autonomous task board, or generic multi-agent collaboration platform.

## Architecture Direction

Sideplane uses an external sidecar model.

```text
Sideplane Server
  - Web UI
  - REST API
  - WebSocket endpoint
  - SQLite/PostgreSQL store
  - Desired configuration state
  - Job planner
  - Audit log

Sideplane Sidecar
  - Node enrollment
  - Heartbeat
  - Runtime discovery
  - Hermes adapter
  - OpenClaw adapter
  - Config backup/apply/rollback
  - Service restart
  - Health check

Managed Runtimes
  - Hermes Agent
  - OpenClaw
```

The sidecar is an independent process. Hermes and OpenClaw are managed runtimes, not the components responsible for changing their own critical configuration.

## Network Model

Sidecars connect outbound to the server by default.

This supports:

- LAN deployments where server and sidecars can reach each other
- Public server with public sidecars
- Public server with private or NATed sidecars

The server dispatches work through an existing sidecar connection or polling fallback, rather than requiring inbound access to every node.

## Planned Technology Stack

- Server: Go
- Sidecar: Go
- CLI: Go
- Web UI: React, TypeScript, Vite
- UI primitives: Radix UI or shadcn/ui with Tailwind CSS
- Default database: SQLite
- Production/team database option: PostgreSQL
- API style: REST plus WebSocket
- API contract: OpenAPI with generated TypeScript client
- Metrics: Prometheus-compatible `/metrics`
- Logging: structured Go `slog`
- Packaging: single binaries, Docker images, systemd units, and install script

The first version should remain easy to self-host and should not require Redis, NATS, Kubernetes, or a separate frontend server at runtime.

## Configuration Model

Sideplane will use layered desired configuration:

```text
global defaults
  -> group overrides
    -> node overrides
      -> agent/profile overrides
```

The MVP should support changing model/provider configuration globally and overriding it for an individual agent or profile.

Configuration changes should follow a safe flow:

```text
plan
  -> diff
  -> sign
  -> sidecar receives
  -> local backup
  -> write temp config
  -> validate
  -> atomic replace
  -> restart if needed
  -> health check
  -> report success or rollback
```

Secrets should be referenced rather than stored inline in ordinary configuration.

## MVP Scope

The first useful version should include:

- Server setup with SQLite
- Sidecar enrollment token flow
- Node registry
- Heartbeat and fresh/stale/offline state
- Hermes status adapter
- OpenClaw status adapter
- Current model/provider display
- Config hash and drift display
- Config diff preview
- Signed config plan
- Safe config apply
- Restart operation
- Rollback operation
- Basic audit log
- Prometheus-compatible metrics endpoint

## Repository Layout

Planned monorepo layout:

```text
sideplane/
  cmd/
    sideplane-server/
    sideplane-sidecar/
    sideplane/
  internal/
    server/
    sidecar/
    api/
    store/
    auth/
    jobs/
    rollout/
    audit/
  pkg/
    protocol/
    config/
    adapters/
      hermes/
      openclaw/
    crypto/
  web/
  deployments/
    docker-compose/
    systemd/
  docs/
  examples/
```

## Development

Run the development server:

```bash
go run ./cmd/sideplane-server
```

The server listens on `:8080` by default, opens `sideplane.db` in the current
directory, and applies SQLite migrations on startup. Use `--addr` to choose
another address and `--db` to choose a different SQLite database path:

```bash
go run ./cmd/sideplane-server --addr :9090 --db ./dev-sideplane.db
```

Node freshness is computed by the server when `GET /api/nodes` is listed. The
store keeps the latest heartbeat-derived status, while the API response applies
the current freshness policy. By default, nodes become `stale` after `2m` and
`offline` after `10m` without a heartbeat:

```bash
go run ./cmd/sideplane-server --stale-after 2m --offline-after 10m
```

`--offline-after` must be greater than `--stale-after`; otherwise the server
exits during startup.

Available endpoints:

- `GET /healthz` returns `{"status":"ok"}`
- `GET /readyz` returns `{"status":"ready"}`
- `GET /metrics` returns a placeholder Prometheus-compatible endpoint
- `POST /api/enrollment-tokens` creates a one-time sidecar enrollment token
- `POST /api/enroll` exchanges an enrollment token for a node credential
- `POST /api/heartbeat` records the latest heartbeat-derived node status and
  requires `Authorization: Bearer <nodeCredential>`
- `GET /api/nodes` lists nodes with freshness-adjusted `fresh`, `stale`, or
  `offline` state

Create a sidecar enrollment token with the CLI:

```bash
go run ./cmd/sideplane enrollment create --server http://localhost:8080
```

The response prints the plaintext token once and its expiry. The server stores
only a hash of the token. The endpoint is intentionally unauthenticated for the
first development slice; authentication/RBAC still needs to be added before
production use.

Enroll a sidecar with the one-time token:

```bash
go run ./cmd/sideplane-sidecar enroll --server http://localhost:8080 --token TOKEN
```

Enrollment writes `nodeId` and `nodeCredential` to
`~/.sideplane/sidecar.json` by default. Use `--state` to choose another path,
and `--node-id` to request a specific node ID:

```bash
go run ./cmd/sideplane-sidecar enroll \
  --server http://localhost:8080 \
  --token TOKEN \
  --node-id worker-a \
  --state ./sidecar-state.json
```

After enrollment, start the sidecar heartbeat loop from the saved state:

```bash
go run ./cmd/sideplane-sidecar
```

For a custom state path, pass the same `--state` value used during enrollment:

```bash
go run ./cmd/sideplane-sidecar --state ./sidecar-state.json
```

The runtime `--server` and `--node-id` flags override values loaded from state.
The node credential is read from state first; `--node-credential` is available
for tests and temporary runs when no state credential exists.

Expected next steps:

1. Extend sidecar heartbeat status with Hermes and OpenClaw discovery.
2. Add Hermes and OpenClaw adapter interfaces.
3. Implement config diff and safe apply planning.

## License

Sideplane is licensed under the [Apache License 2.0](./LICENSE).
