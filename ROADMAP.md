# Roadmap

Sideplane is pre-1.0 infrastructure. This roadmap describes the implemented
MVP baseline and near-term hardening direction; it is not a release commitment.

## Guiding Goal

Build a small, self-hosted control plane that can answer:

> What is each self-hosted agent running, and can Sideplane safely change it without breaking the node?

## Implemented MVP Baseline

The current repository includes these foundations:

- Go monorepo with `sideplane-server`, `sideplane-sidecar`, and `sideplane` CLI binaries.
- SQLite store with migrations, heartbeat retention, enrollment tokens, node credentials, jobs, desired config, and audit events.
- REST API, health/readiness endpoints, Prometheus-compatible metrics, structured request logging, and security response headers.
- Sidecar-initiated enrollment, heartbeat, job polling, deep probe, signed config apply, dry-run/live apply gating, backup, restart, health check, and automatic rollback on failed live apply.
- Adapter interfaces with Hermes and OpenClaw read-only discovery/config snapshot support.
- Compact React/Vite Web UI for fleet inventory, node detail, config diff/apply wizard, audit history, keyboard navigation, node removal, and job expansion.
- Docker Compose deployment, Linux systemd units, install script, and server-embedded Web assets.

## MVP Hardening Next

Near-term work should make the existing operator path more complete and easier
to verify:

- Document the current REST API with OpenAPI and generate stable TypeScript API types for the Web UI.
- Standardize JSON API error responses and secret redaction across API, audit, logs, CLI, and Web display.
- Expand CLI operator coverage for node inspection, job/audit listing, config preview/apply, restart, rollback, and help output.
- Scale Web history views with compact filters, load-more controls, copyable identifiers, and clearer token handling.
- Add standalone restart and explicit rollback workflows while preserving dry-run defaults and fake-safe tests.
- Improve sidecar doctor/read-only smoke workflows, store migration visibility, SQLite reliability settings, retention pruning, and bounded metrics.
- Harden adapter validation, request size limits, auth comparisons, path-safety tests, Docker/systemd packaging, release artifacts, and install checksum verification.
- Add focused Web smoke tests, end-to-end API/CLI regression coverage, contribution docs, templates, and changelog updates.

## Later

- PostgreSQL support after the SQLite MVP is boring and durable.
- macOS launchd and Windows service support.
- Multi-user/RBAC for small teams.
- Rollout waves and canaries after single-node safe apply is proven.
- Additional runtime adapters.
- Secret backend integrations using references rather than an in-product secret manager.

## Non-Goals for the MVP

- General remote shell
- Full log aggregation platform
- Chat UI
- Prompt workspace
- Agent task board
- Marketplace/plugin ecosystem
- Kubernetes-first deployment
