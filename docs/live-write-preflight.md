# Live Write Preflight

Sideplane live config apply is intentionally operator-gated. Use this checklist
before any first content-change write against a real managed runtime.

This document is generic. Keep machine hostnames, IP addresses, private paths,
tokens, and real config contents in local ignored notes only.

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
