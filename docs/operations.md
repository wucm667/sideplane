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
