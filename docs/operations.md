# Operations

This guide covers day-2 operations for a self-hosted Sideplane server: database
backup/restore, operator token scopes, bulk fleet operations, alert webhooks,
expected sidecar version, and rollout templates.

## Database Backup And Restore

The SQLite store can be snapshotted online while the server runs.

- On demand:

  ```bash
  sideplane-server backup --db ./sideplane.db --out ./sideplane-backup.db
  ```

  This writes a transactionally consistent copy with SQLite `VACUUM INTO`
  without serving HTTP. The destination must not already exist.

- Scheduled: start the server with `--backup-dir <dir>` and a positive
  `--backup-interval` (for example `1h`). Each tick writes a timestamped copy and
  prunes all but the most recent `--backup-retention` backups (default `7`).
  Periodic backups are off unless both flags are set.

Restore is offline:

```bash
sudo systemctl stop sideplane-server   # or stop the container
cp ./sideplane-backup.db ./sideplane.db
sudo systemctl start sideplane-server
```

Do not swap the database file while the server is running.

## Operator Token Scopes

Named operator tokens carry a scope:

- `admin` — full read and mutating access (default for backward compatibility).
- `readonly` — GET/list endpoints only; mutating endpoints return `403`.

The env/flag bootstrap token is always `admin`. Create scoped tokens with the
CLI or Web:

```bash
sideplane token create --name viewer --scope readonly
```

Audit events for operator actions record the acting named token id (`actor_id`)
when available.

## Bulk Operations

Bulk operations target a label selector or an explicit node set (mutually
exclusive). Selector semantics are exact `key=value` AND matching.

- Bulk deep probe:

  ```bash
  sideplane probe --selector role=canary
  ```

- Bulk label assignment (merges the given labels, preserving other keys):

  ```bash
  sideplane node label --selector role=canary tier=gold
  ```

Both are also available from the fleet table multi-select in the Web UI. Bulk
job creation skips nodes that already have an active job and reports per-node
results. Both actions are audited (`job.bulk.create`, `node.labels.bulk.update`).

## Alert Webhooks

Operator-configured webhooks receive a small JSON payload on these events:
`node.offline`, `node.drift`, `rollout.paused`, `rollout.failed`. Delivery is
best-effort with bounded retries and a per-attempt timeout; it never blocks core
request paths, and payloads contain no secrets.

```bash
sideplane webhook create --url https://hooks.example.com/sp \
  --event rollout.paused --event rollout.failed --sign
```

When `--sign` is set (or a secret is provided), the server returns a signing
secret once. Deliveries then carry an `X-Sideplane-Signature: sha256=<hex>` HMAC
of the request body. Verify it on the receiver with the shared secret.

Retry policy: network errors, `5xx`, and `429` are retried with backoff up to a
bounded number of attempts; other `4xx` responses are treated as permanent and
dropped. A persistently failing or slow receiver is dropped without stalling
producers.

## Expected Sidecar Version

Set an expected sidecar version to flag nodes running a different version:

```bash
sideplane settings set --expected-sidecar-version v1.2.0
```

Nodes whose reported `sidecarVersion` differs are marked `sidecarOutdated` in the
fleet view and counted by the `sideplane_fleet_sidecar_outdated` metric gauge.
Leave the value empty to disable the check. Sideplane never downloads or executes
sidecar binaries; this is visibility only.

## Rollout Auto-Rollback

A rollout can opt into automatic rollback with `autoRollbackOnFailure` (CLI
`--auto-rollback`, requires `--live`). On a failed live batch, the orchestrator
dispatches a per-node rollback to each already-applied node's most recent
pre-rollout backup via the existing rollback pipeline, then pauses with a reason
noting the attempt. Rollback dispatch is never retried and never triggers another
rollback. Dry-run rollouts are never rolled back.

## Rollout Templates

A rollout template is a saved, reusable rollout spec that is never executed on
its own. Create one and reference it when creating a rollout:

```bash
sideplane rollout template create --name canary --selector role=canary \
  --provider openai --model gpt-4o --batch-size 1
sideplane rollout create --template <templateId>
```

The template prefills the spec; the spec is still resolved and validated at
rollout creation time.

## Scheduled Rollouts

A rollout can carry an optional `startAt` time (RFC3339). With a future
`startAt` the rollout stays scheduled and the orchestrator does not dispatch any
batch until that time is reached; an empty or past value runs immediately. Abort
works while a rollout is still scheduled.

```bash
sideplane rollout create --selector role=canary --provider openai --model gpt-4o \
  --start-at 2026-07-01T09:00:00Z
```

## Node Maintenance Mode

Put a node into maintenance to take it out of automated change flows without
removing it from the fleet:

```bash
sideplane node maintenance <nodeId> --on
sideplane node maintenance <nodeId> --off
```

Maintenance nodes are excluded from rollout target resolution and bulk operations
by default (pass an explicit include flag to override), and their node-offline and
drift alert webhooks are suppressed. Heartbeats are still recorded and the node
shows a maintenance badge. Already-running per-node jobs are not interrupted.

## Runtime Health Checks

The sidecar performs read-only, local liveness checks for each runtime and
reports a health state of `healthy`, `degraded`, or `unknown` with a short
reason. These checks only inspect local, allowlisted signals (config readability,
declared service/container presence) — they never contact provider APIs, reach
external networks, or restart anything. Health is shown on the node runtime cards
and in `sideplane node inspect`, and degraded runtimes are counted by a metric
gauge.

## TLS And Reverse Proxy

The server speaks plain HTTP by default. To terminate TLS in-process, set both
`--tls-cert` and `--tls-key` (or `SIDEPLANE_TLS_CERT` / `SIDEPLANE_TLS_KEY`);
setting only one fails fast. Optionally run an HTTP→HTTPS redirector with
`--tls-redirect-addr`. There is no built-in ACME/auto-certificate issuance.

To serve Sideplane under a subpath behind a reverse proxy, set `--base-path`
(e.g. `--base-path /sideplane`, or `SIDEPLANE_BASE_PATH`). The API, SSE stream,
and embedded web app are served under that prefix; the server injects the base
into the served `index.html` so the web client builds all request and asset URLs
relative to it. The `/healthz`, `/readyz`, and `/metrics` probe endpoints remain
available at the root for liveness/readiness checks.
