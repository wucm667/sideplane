# Fleet Rollouts

Sideplane rollouts stage one provider/model target across a set of existing
nodes. They reuse the signed config-apply job path; the rollout system does not
write config files directly and does not restart services from the server.

## Target Selection

A rollout selects nodes in exactly one of two ways:

- `selector`: exact label matches such as `role=canary,zone=lab`.
- `nodeIds`: explicit node IDs.

Selectors and explicit node IDs are mutually exclusive. Labels are
operator-managed metadata on node records, not sidecar-reported facts. They use
the same validation as node labels, and selector matching is exact key/value
matching across all supplied labels.

## Batch Model

Rollouts create sequential batches from the resolved node list. `batchSize`
defaults to `1`, which makes the first node a canary. The rollout engine is a
pure state stepper: it advances one snapshot at a time using an injected clock,
dispatcher, and health reader. The server orchestrator persists the updated
state and dispatches jobs; it does not run live writes itself.

Each node in the active batch receives a normal signed config-apply job. The
job payload carries the rollout target provider/model, runtime type, profile,
and dry-run/live mode. `live` defaults to `false`. Live rollouts still require
the usual signing key setup and sidecar `--allow-live-apply` gate.

## Health Gates

After dispatching a batch, Sideplane waits up to `healthTimeout` for every
node in that batch. The default timeout is five minutes.

- Dry-run rollout health means the config-apply job succeeded.
- Live rollout health means the config-apply job succeeded and the latest
  heartbeat shows no provider/model drift for that node.

If every node is healthy, the next batch starts. If any node fails, times out,
or is offline, the rollout pauses with a reason and the failing node IDs. No
later batch is dispatched while paused.

## Operator Actions

Rollout states are:

```text
pending -> running -> paused -> completed
                       |       -> aborted
                       |       -> failed
```

Operators can pause, resume, or abort a rollout. Resume re-dispatches
unfinished nodes as needed and then continues batch progression. Abort is
terminal.

## Non-Goal

Automatic rollback of a failed rollout batch is intentionally out of scope.
Rollouts pause on failure so the operator can inspect job results, review
backups with `sideplane backups list`, and create an explicit rollback job when
that is the safest recovery path.

## API And CLI

Primary endpoints:

- `POST /api/rollouts`
- `GET /api/rollouts`
- `GET /api/rollouts/{rolloutId}`
- `POST /api/rollouts/{rolloutId}/actions`

CLI commands:

- `sideplane rollout create`
- `sideplane rollout list`
- `sideplane rollout status`
- `sideplane rollout pause`
- `sideplane rollout resume`
- `sideplane rollout abort`
