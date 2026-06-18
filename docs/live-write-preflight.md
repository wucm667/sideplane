# Live Write Preflight

Sideplane live config apply is intentionally operator-gated. Use this checklist
before any first content-change write against a real managed runtime.

This document is generic. Keep machine hostnames, IP addresses, private paths,
tokens, and real config contents in local ignored notes only.

## Already Passed

For this preflight milestone, the sandbox happy path, rollback regressions, and
real-machine zero-change live apply have already passed. Details belong in local
ignored notes such as `.sideplane/real-machine-notes.md`. Do not rerun those as
a new gate for the first content-change write; this runbook describes the next
operator-approved step only.

## Scope

The first content-change live write should change exactly one low-risk model
string and then immediately change it back. Do not combine it with provider
changes, secret changes, service upgrades, rollout waves, or unrelated config
cleanup.

## Restart Permission Model

For the first content-change live write, Sideplane uses **Path A:
root/systemd controller**:

- Start the sidecar with `--allow-live-apply`.
- Configure the managed Hermes systemd unit with `--hermes-service-unit <unit>`.
- Run the sidecar with permission to execute `systemctl restart <unit>` and
  `systemctl is-active <unit>` noninteractively.

The validated default is a root-managed sidecar. Non-root sudo restart support
is deferred; do not pass or document a `--hermes-restart-sudo` flag unless that
code path has been implemented and tested in a later change.

## Required Safety Controls

- The sidecar initiates the outbound connection to the server; no inbound
  sidecar management port is required.
- Mutating operator APIs require an operator token.
- The server must use a persistent signing key so signed plans remain verifiable
  across process restarts.
- Live apply requires `--allow-live-apply`; it is dangerous and must be enabled
  only for controlled operator-approved runs.
- The restart target must be explicit and allowlisted; do not introduce generic
  shell execution.
- The target config path must be known from a recent deep probe.
- The live config path must not be a symlink unless a reviewed symlink strategy
  is documented before the run.
- The rollback expectation must be explicit: backup first, replace atomically,
  restart, health check, and restore the backup on failure.

## First Content-Change Runbook

1. Record the current runtime snapshot from a fresh deep probe: runtime type,
   provider, model, config hash, and config path metadata. Store any
   machine-specific observations only in local ignored notes.
2. Confirm the target config path is the expected regular file, not a symlink,
   unless a reviewed symlink handling strategy has been accepted before the
   run.
3. Pick one low-risk model string change. Do not change provider, endpoint,
   secrets, tool settings, or unrelated runtime fields.
4. Prepare the revert value before applying the change. The revert is the same
   single model field changed back to the original value.
5. Create a dry-run config apply for the exact model change and inspect the
   result. Required completed steps: `plan_received`, `signature_verified`,
   `backup_created`, `temp_written`, and `validated`. Required skipped steps:
   `replaced`, `restarted`, and `health_checked`.
6. Inspect the dry-run diff or rendered temp result without exposing secrets.
   Confirm only the intended model string changes and no secret-like values are
   present in plan, result, logs, audit events, or UI output.
7. Capture the live config hash immediately before the live apply.
8. Create the live config apply with `dryRun=false` only after explicit
   operator approval for this run.
9. Confirm the live apply reports `replaced`, `restarted`, and `health_checked`
   as completed. Confirm a backup path is recorded for the run.
10. Capture the live config hash immediately after the change. It should differ
    from the pre-change hash unless the selected value was accidentally the
    same as the original.
11. Confirm the managed runtime is healthy after restart and that the audit log
    records the operator action without secrets.
12. Apply the prepared revert plan immediately, again changing only the model
    string.
13. Capture the final config hash. It should match the original pre-change hash
    if the revert restored the exact original bytes.
14. Confirm no unrelated config bytes changed. Do not copy real config contents
    into tracked files; record only hashes and generic observations in committed
    docs.

## Go/No-Go Criteria

Go only when all of these are true:

- Operator token is configured for mutating APIs.
- Persistent signing key is configured and stable across server restarts.
- Server and sidecar are on a trusted network or protected by the intended TLS
  boundary.
- Sidecar restart permission model is decided; for this preflight it is
  Path A, root/systemd with `--hermes-service-unit <unit>`.
- Config path is confirmed as a regular file, or an accepted symlink strategy is
  documented before the run.
- Dry-run validation succeeds for the exact proposed model string change.
- Backup and rollback expectations are explicit before live apply.
- Revert plan is ready before the live apply starts.

Stop and do not run the live content-change write if any of these are true:

- Desired change touches more than one model string.
- Provider, endpoint, secrets, auth files, tool settings, or unrelated fields
  would change.
- Restart or health check target is missing or not allowlisted.
- Dry-run output contains secret-like values in plan, logs, audit, or UI output.
- The pre-change hash cannot be captured.
- The operator has not explicitly approved the live content-change write.
