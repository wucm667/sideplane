# Operator Recovery Runbook

This runbook is for self-hosted Sideplane operators recovering from paused
rollouts, bad configuration changes, database issues, or token exposure.

Do recovery from a trusted shell with an admin operator token. Keep machine
private details, real hostnames, IPs, secrets, and raw runtime config contents
out of tickets and committed notes.

## First Checks

1. Confirm the server is reachable:

   ```bash
   sideplane status --server http://localhost:8080 --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
   ```

2. Check recent audit activity:

   ```bash
   sideplane audit list --server http://localhost:8080 --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
   ```

3. Export a copy before changing state:

   ```bash
   sideplane audit export --format ndjson --out sideplane-audit.ndjson \
     --server http://localhost:8080 --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
   ```

## Paused Rollout

Start by inspecting the rollout and affected node jobs:

```bash
sideplane rollout status <rolloutId> --server http://localhost:8080 \
  --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
sideplane jobs list <nodeId> --server http://localhost:8080 \
  --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
```

Resume when the pause cause was transient and the rollout target is still
correct: a node came back online, a dry-run job was retried manually, or an
external receiver recovered.

```bash
sideplane rollout resume <rolloutId> --server http://localhost:8080 \
  --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
```

Abort when the target provider/model is wrong, the node set is wrong, health
checks show an unsafe runtime state, or the rollout has already been replaced by
a safer plan.

```bash
sideplane rollout abort <rolloutId> --server http://localhost:8080 \
  --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
```

Do not create an overlapping rollout for the same nodes unless you have reviewed
the conflict and intentionally use `allowOverlap`.

## Per-Node Rollback

Sideplane rollback uses backup references reported by prior config-apply jobs.
List the server-known references first:

```bash
sideplane backups list <nodeId> --server http://localhost:8080 \
  --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
```

Create a dry-run rollback before any live mutation:

```bash
sideplane rollback <nodeId> --backup-ref <backupRef> --wait \
  --server http://localhost:8080 --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
```

Only after reviewing the dry-run result and confirming the live-write preflight,
run the live rollback:

```bash
sideplane rollback <nodeId> --backup-ref <backupRef> --live --yes --wait \
  --server http://localhost:8080 --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
```

Afterward, run a deep probe and confirm the config hash/provider/model returned
to the expected state.

## Restore From A Database Backup

Database restore is offline. Do not replace the SQLite file while the server is
running.

Systemd example:

```bash
sudo systemctl stop sideplane-server
sudo cp /var/lib/sideplane/backups/sideplane-backup-YYYYMMDDTHHMMSS.db \
  /var/lib/sideplane/sideplane.db
sudo chown sideplane:sideplane /var/lib/sideplane/sideplane.db
sudo systemctl start sideplane-server
```

Docker Compose example:

```bash
docker compose -f deployments/docker-compose/docker-compose.yml stop server
cp ./sideplane-backup.db ./data/sideplane.db
docker compose -f deployments/docker-compose/docker-compose.yml start server
```

After restore, check `/readyz`, `sideplane status`, fleet node count, recent
audit events, and enrollment/operator-token state. If the backup predates a token
rotation, rotate again.

## Rotate Operator Tokens

Named tokens can be rotated without restarting the server:

```bash
sideplane token create --name ops-new --scope admin \
  --server http://localhost:8080 --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
sideplane whoami --server http://localhost:8080 --operator-token "<new-token>"
sideplane token revoke <oldTokenId> --server http://localhost:8080 \
  --operator-token "<new-token>"
```

For the bootstrap token in `SIDEPLANE_OPERATOR_TOKEN`, update the environment
file or secret store, restart `sideplane-server`, verify the new token with
`sideplane whoami`, then remove the old value from operator machines.

## Revoke A Leaked Token

1. If the leaked token is a named token, list metadata and revoke it:

   ```bash
   sideplane token list --server http://localhost:8080 \
     --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
   sideplane token revoke <tokenId> --server http://localhost:8080 \
     --operator-token "$SIDEPLANE_OPERATOR_TOKEN"
   ```

2. If the leaked token is the bootstrap token, replace `SIDEPLANE_OPERATOR_TOKEN`
   and restart the server.
3. Export audit events and review actions since the suspected exposure time.
4. Rotate any downstream credentials that may have been changed through exposed
   operator access.
5. Keep read-only tokens for dashboards and automation where mutation is not
   needed.

## Recovery Exit Criteria

- The affected rollout is completed, aborted, or intentionally left paused with
  an operator note.
- Any live rollback has a completed job result and a fresh deep-probe result.
- The fleet view shows expected freshness, drift, and runtime health.
- Audit export has been captured for the incident window.
- Leaked or obsolete tokens are revoked, and new tokens have been verified.
