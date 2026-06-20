# Sideplane

Open-source control plane for self-hosted AI agent fleets.

Sideplane is built for solo operators, one-person companies, and small teams running agents across local PCs, LAN servers, and VPS nodes. It starts with Hermes Agent and OpenClaw and focuses on a narrow operational promise:

> Know what every self-hosted agent is running, safely change its configuration, and recover when a change goes wrong.

## Status

Sideplane is early, but the first control-plane path is implemented: a Go server, Go sidecar, SQLite store, compact React UI, Docker packaging, systemd units, enrollment flow, heartbeat/status reporting, audit history, metrics, and signed config-apply jobs.

Production operators should still treat it as pre-1.0 infrastructure. Run it on low-risk nodes first, keep backups, and review the dry-run/live apply docs before enabling live writes.

## Features

- Fleet inventory with node heartbeat, freshness state, runtime summary, and drift badges.
- Sidecar enrollment token flow with one-time tokens exchanged for long-lived node credentials.
- Hermes and OpenClaw adapters for read-only runtime discovery, config hash reporting, and provider/model snapshots.
- Desired configuration layering with effective config preview and read-only actual-vs-desired diffs.
- Signed config apply plans, dry-run by default, with live apply gated behind explicit sidecar opt-in and rollback handling.
- Staged fleet rollouts for provider/model changes across node labels or explicit node lists, canary-first by default.
- Deep-probe, config-apply, restart/rollback-aware job lifecycle with paginated recent job status in the UI.
- Operator audit log with node/action filtering and deletion audit events.
- Node removal API and UI flow for decommissioned fleet entries.
- Conservative retention pruning for old completed/failed jobs and audit events.
- Prometheus-compatible `/metrics`, including job counters and fleet freshness/drift gauges.
- Compact infrastructure-console Web UI served directly by `sideplane-server`.
- Docker Compose, server and sidecar systemd units, and a Linux install script for local systemd setup.
- Server hardening with security response headers and structured request logging.

Sideplane is not a chat UI, prompt workspace, autonomous task board, marketplace, or generic multi-agent collaboration product.

## Quick Start

### Docker Compose

From the repository root:

```bash
export SIDEPLANE_OPERATOR_TOKEN='replace-with-a-long-random-token'
docker compose -f deployments/docker-compose/docker-compose.yml up -d --build
```

Open `http://localhost:8080`, enter the operator token in the sidebar, and create an enrollment token in the Enrollment view. Then enroll the compose sidecar:

```bash
docker compose -f deployments/docker-compose/docker-compose.yml exec sidecar \
  sideplane-sidecar enroll --server http://server:8080 --token TOKEN --state /data/sidecar.json
```

The server container serves the embedded Web UI and exposes a `/healthz`
healthcheck. The sidecar starts after the server is healthy, waits quietly for
`/data/sidecar.json`, then begins outbound heartbeat and job polling with
env-var configuration.

### Local Development

Run the server with SQLite. By default the Go binary serves the embedded Web
assets compiled into `sideplane-server`:

```bash
export SIDEPLANE_OPERATOR_TOKEN='replace-with-a-long-random-token'
go run ./cmd/sideplane-server --db ./sideplane.db
```

Run the Web UI dev server in another terminal:

```bash
cd web
npm install
npm run dev
```

The Vite dev server listens on `http://localhost:3000` and proxies API requests to `http://localhost:8080`.

To override the embedded assets during local UI development, serve a built UI
directory from the Go server:

```bash
npm --prefix web run build
export SIDEPLANE_OPERATOR_TOKEN='replace-with-a-long-random-token'
go run ./cmd/sideplane-server --db ./sideplane.db --web-dir ./web/dist
```

### Manual Sidecar

Create an enrollment token from the UI or CLI:

```bash
go run ./cmd/sideplane enrollment create \
  --server http://localhost:8080 \
  --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
```

Enroll the sidecar:

```bash
go run ./cmd/sideplane-sidecar enroll \
  --server http://localhost:8080 \
  --token TOKEN \
  --node-id worker-a \
  --state ./sidecar-state.json
```

Start heartbeat and job polling:

```bash
go run ./cmd/sideplane-sidecar --state ./sidecar-state.json
```

## Configuration

Server flags can be configured with environment variables. Explicit CLI flags take precedence over env vars.

| Env var | Default | Purpose |
| --- | --- | --- |
| `SIDEPLANE_ADDR` | `:8080` | HTTP listen address. |
| `SIDEPLANE_DB_PATH` | `sideplane.db` | SQLite database path. |
| `SIDEPLANE_WEB_DIR` | empty | Built Web UI directory to serve instead of embedded assets. Empty uses embedded assets. |
| `SIDEPLANE_OPERATOR_TOKEN` | empty | Bearer token required for mutating operator APIs. |
| `SIDEPLANE_SIGNING_KEY` | empty | Ed25519 config-plan signing key path. Empty uses ephemeral in-memory key. |
| `SIDEPLANE_STALE_AFTER` | `2m` | Heartbeat age before a node is stale. |
| `SIDEPLANE_OFFLINE_AFTER` | `10m` | Heartbeat age before a node is offline. Must exceed stale duration. |
| `SIDEPLANE_HEARTBEAT_RETENTION` | `100` | Number of recent heartbeat records retained per node. |
| `SIDEPLANE_JOB_RETENTION` | `720h` | Age to retain completed and failed jobs. Pending and claimed jobs are never pruned. Set `0` to disable. |
| `SIDEPLANE_AUDIT_RETENTION` | `4320h` | Age to retain audit events. Set `0` to disable. |
| `SIDEPLANE_ROLLOUT_INTERVAL` | `5s` | Background rollout reconciliation interval. Set `0` to disable. |
| `SIDEPLANE_ALLOW_UNAUTHENTICATED_OPERATOR_API` | false | Development-only escape hatch for mutating operator APIs. |

Matching flags are available on `sideplane-server`: `--addr`, `--db`,
`--web-dir`, `--operator-token`, `--signing-key`, `--stale-after`,
`--offline-after`, `--heartbeat-retention`, `--job-retention`,
`--audit-retention`, `--rollout-interval`, and
`--allow-unauthenticated-operator-api`.

Sidecar runtime flags also support env vars. Explicit CLI flags take precedence over env vars, then values loaded from the sidecar state file.

| Env var | Purpose |
| --- | --- |
| `SIDEPLANE_SERVER_URL` | Server URL used for heartbeat, job polling, and enrollment commands. |
| `SIDEPLANE_NODE_ID` | Node ID override after enrollment. |
| `SIDEPLANE_SIDECAR_STATE` | Sidecar state file path. |
| `SIDEPLANE_HEARTBEAT_INTERVAL` | Heartbeat loop interval. |
| `SIDEPLANE_JOB_POLL_INTERVAL` | Job polling interval. |
| `SIDEPLANE_HERMES_CONFIG_PATHS` | Read-only Hermes config search paths. |
| `SIDEPLANE_OPENCLAW_CONFIG_PATHS` | Read-only OpenClaw config search paths. |
| `SIDEPLANE_HERMES_DOCKER_CONTAINER` | Optional Hermes Docker container for read-only status/log inspection and allowlisted restart target. |
| `SIDEPLANE_HERMES_SERVICE_UNIT` | Optional Hermes systemd service unit restart target. |
| `SIDEPLANE_SERVER_PUBLIC_KEY` | Server public key used to verify signed config plans. |
| `SIDEPLANE_APPLY_WORK_DIR` | Sidecar-controlled work directory for config apply dry runs and backups. |

There is intentionally no env var for `--allow-live-apply` or `--node-credential`. Live writes require an explicit flag, and node credentials are read from the state file.

## Architecture

Sideplane uses an external sidecar model:

```text
Sideplane Server
  - Web UI, REST API, metrics
  - SQLite store
  - desired configuration state
  - job planner and audit log

Sideplane Sidecar
  - node enrollment and heartbeat
  - runtime discovery
  - Hermes/OpenClaw adapters
  - signed config plan verification
  - backup/apply/restart/health-check/rollback path

Managed Runtimes
  - Hermes Agent
  - OpenClaw
```

Sidecars connect outbound to the server, which works for LAN, public, and private/NATed nodes without requiring inbound management ports. The sidecar is a controlled executor, not a general remote shell.

## API And Operations

Core endpoints:

- `GET /healthz`, `GET /readyz`, and `GET /metrics`.
- `POST /api/enrollment-tokens` creates one-time enrollment tokens with operator auth.
- `POST /api/enroll` exchanges an enrollment token for a node credential.
- `POST /api/heartbeat` records node status with node credential auth.
- `GET /api/nodes` lists freshness-adjusted nodes with drift state.
- `DELETE /api/nodes/{nodeId}` removes a node with operator auth and records `node.delete`.
- `GET /api/nodes/{nodeId}/jobs?limit=50&status=completed` lists node jobs with optional bounded `limit` and `status` filters.
- `POST /api/nodes/{nodeId}/jobs` creates a `deep_probe` job with operator auth.
- `POST /api/nodes/{nodeId}/config-apply` creates a signed config apply job with operator auth.
- `GET /api/audit?nodeId=...&action=...&limit=...` lists audit events with additive filters.
- `GET /api/sidecar/jobs/next?nodeId=...` and `POST /api/sidecar/jobs/{jobId}/result` power sidecar polling.

## CLI Reference

The `sideplane` CLI is a compact operator client for the REST API. Defaults
resolve in this order: explicit flag, environment variable, CLI config file,
then built-in default. The default config file is
`~/.config/sideplane/config.yaml`; set `SIDEPLANE_CONFIG` to use another path.

```yaml
server: http://localhost:8080
operatorToken: replace-with-a-long-random-token
runtimeType: hermes
profile: default
```

The CLI also reads `SIDEPLANE_SERVER_URL`, `SIDEPLANE_OPERATOR_TOKEN`,
`SIDEPLANE_RUNTIME_TYPE`, and `SIDEPLANE_PROFILE`.

Generate shell completion with `sideplane completion bash` or
`sideplane completion zsh`.

| Command | Purpose | Key flags |
| --- | --- | --- |
| `sideplane fleet status` | Show fleet node status. | `--server`, `--selector`, `--json` |
| `sideplane probe <nodeId>` | Create a deep-probe job. | `--server`, `--operator-token`, `--wait`, `--json` |
| `sideplane jobs list <nodeId>` | List node jobs with optional filters. | `--server`, `--operator-token`, `--limit`, `--status`, `--json` |
| `sideplane audit list` | List audit events newest first. | `--server`, `--node-id`, `--action`, `--limit`, `--json` |
| `sideplane rollout create` | Create a staged provider/model rollout. | `--server`, `--operator-token`, `--selector`, `--node`, `--provider`, `--model`, `--runtime-type`, `--profile`, `--batch-size`, `--live`, `--yes`, `--health-timeout`, `--json` |
| `sideplane rollout list` | List staged rollouts. | `--server`, `--operator-token`, `--json` |
| `sideplane rollout status <id>` | Show rollout batches and per-node progress. | `--server`, `--operator-token`, `--watch`, `--json` |
| `sideplane rollout pause/resume/abort <id>` | Control a rollout. | `--server`, `--operator-token`, `--json` |
| `sideplane config preview <nodeId>` | Show effective desired config and diff. | `--server`, `--runtime-type`, `--profile`, `--actual-hash`, `--json` |
| `sideplane config apply <nodeId>` | Create a dry-run or live config apply job. | `--server`, `--operator-token`, `--runtime-type`, `--profile`, `--config-path`, `--live`, `--yes`, `--wait`, `--json` |
| `sideplane config get` | Show desired configuration. | `--server`, `--json` |
| `sideplane config set` | Update global desired provider/model. | `--server`, `--operator-token`, `--provider`, `--model` |
| `sideplane config-file path` | Print the resolved CLI config path. | none |
| `sideplane completion bash/zsh` | Print a shell completion script. | none |
| `sideplane node inspect <nodeId>` | Show detailed node state and runtime status. | `--server`, `--json` |
| `sideplane node label <nodeId>` | Set or remove operator-managed labels. | `--server`, `--operator-token`, `--remove`, `--json` |
| `sideplane node remove <nodeId>` | Remove a decommissioned node record. | `--server`, `--operator-token`, `--yes` |
| `sideplane backups list <nodeId>` | List rollback backups for a node. | `--server`, `--operator-token`, `--limit`, `--json` |
| `sideplane enrollment create` | Create a one-time sidecar enrollment token. | `--server`, `--operator-token`, `--expires-in` |
| `sideplane version` | Print CLI version. | none |

## Web Operator Notes

The Web UI is intentionally a compact infrastructure console. It includes:

- Fleet search and sortable node columns.
- Node job history with server-side `limit` and `status` query support.
- Expandable job result rows for deep-probe and config-apply details.
- Activity history filters by node ID and action.
- Keyboard shortcuts: `f`/`1` opens Fleet, `a`/`2` opens Activity, `e`/`3`
  opens Enrollment, `r` refreshes the current view, and `Esc` returns from a
  node detail view to Fleet.

For systemd deployment files, see `deployments/systemd/`. The root
`install.sh` creates the `sideplane` user/group, `/etc/sideplane`,
`/var/lib/sideplane`, and copies systemd units/env examples. Pass
`--version vX.Y.Z` to download release binaries from GitHub and verify them
against `SHA256SUMS`; pass `--no-download` to keep the local build-and-copy
workflow.

## Security

- Operator-token auth protects mutating operator endpoints.
- One-time enrollment tokens are stored hashed and exchanged for long-lived node credentials.
- Configuration plans are signed by the server and verified by the sidecar.
- The sidecar defaults to dry-run config apply; live apply requires explicit `--allow-live-apply`.
- The sidecar uses adapter-specific allowlisted operations and does not expose arbitrary shell execution.
- Provider secrets should be referenced through local env/files/SOPS/1Password/Vault-style backends, not stored inline in ordinary config.
- Server responses include `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, and a restrictive Content Security Policy. CORS is not enabled by default because the server serves its own UI.
- Server request logs are structured with method, path, status, duration, and remote address.

## Docs

- [Contributor guide](CONTRIBUTING.md)
- [Signed config apply pipeline](docs/config-apply-pipeline.md)
- [Live-write preflight](docs/live-write-preflight.md)
- [Read-only sidecar deployment](docs/read-only-sidecar-deployment.md)
- [Real-machine read-only smoke test](docs/real-machine-readonly-smoke-test.md)

## License

Sideplane is licensed under the [Apache License 2.0](./LICENSE).
