# Configuration Apply Pipeline

Sideplane changes runtime configuration through a signed, transactional pipeline.
The default mode is a **dry run** that validates a change without touching the
live config or restarting anything. A live apply must be requested explicitly
and is gated by a sidecar flag.

This document describes the pipeline and the operator flag. It contains no
machine-specific values; substitute your own server URL, node IDs, and paths.

## Pipeline

```text
plan -> diff -> sign -> sidecar -> backup -> validate -> replace -> restart -> health check
                                                              \-> rollback on failure
```

1. **Plan**: the server builds a config plan from the persisted desired config
   (global defaults -> node override -> runtime/profile override) for the target
   node and runtime.
2. **Diff**: the effective desired config is compared with the latest read-only
   actual snapshot from a deep probe.
3. **Sign**: the server signs the plan with its ed25519 key. The sidecar verifies
   the signature before doing anything else and rejects tampered plans.
4. **Backup**: the sidecar copies the current config into a sidecar-controlled
   work directory before any change.
5. **Validate**: the rendered config is written to a temp file and validated.
6. **Replace** (live only): the validated config is moved into place atomically.
7. **Restart + health check** (live only): the runtime is restarted with an
   allowlisted operation and checked for health.
8. **Rollback**: any failure after the replace restores the backup byte-for-byte.

In dry-run mode the pipeline stops after validation; the replace, restart, and
health-check steps are reported as `skipped` and nothing on the node changes.

## Operator flag: live apply is off by default

The sidecar performs live config replacement and restart only when started with:

```bash
sideplane-sidecar --allow-live-apply
```

Without this flag the sidecar runs dry-run only. A live plan received while the
flag is off fails safely at the validation step (`live config apply is disabled
by sidecar policy`) before any backup, write, or restart occurs.

For a containerized or systemd-managed runtime, also point the sidecar at its
restart target so health checks and restart can find the service:

```bash
sideplane-sidecar --hermes-docker-container <container>   # docker-managed runtime
sideplane-sidecar --hermes-service-unit <unit>            # systemd-managed runtime
```

## Creating an apply from the API

A config apply is created per node and defaults to dry run:

```bash
# Dry run (safe default)
curl -fsS -X POST -H 'Authorization: Bearer <operator-token>' \
  -H 'Content-Type: application/json' \
  -d '{"runtimeType":"hermes"}' \
  https://<server>/api/nodes/<nodeId>/config-apply

# Live apply (requires the sidecar --allow-live-apply flag)
curl -fsS -X POST -H 'Authorization: Bearer <operator-token>' \
  -H 'Content-Type: application/json' \
  -d '{"runtimeType":"hermes","dryRun":false}' \
  https://<server>/api/nodes/<nodeId>/config-apply
```

The server requires that desired provider/model are set and that a config path
is known from a prior deep probe; otherwise it returns `400`.

In the web UI, the node detail **Edit config** button opens the Change
Configuration wizard (Edit -> Review -> Apply -> Done), which runs a dry run by
default and renders the per-step pipeline checklist.

## Metrics

The Prometheus endpoint at `/metrics` exposes apply outcomes:

```text
sideplane_jobs_created_total{type="config_apply"}
sideplane_jobs_completed_total{type="config_apply"}
sideplane_jobs_failed_total{type="config_apply"}
sideplane_config_apply_rolled_back_total
```

## Safety notes

- No secret values are stored in plans, logs, audit events, diffs, or metrics.
- Plans are signed; the sidecar rejects unsigned or tampered plans.
- Backups are retained per apply run so a change can be reverted.
- Live apply is never reachable with `--allow-live-apply` off.
