# Changelog

All notable changes to Sideplane will be documented in this file.

This project follows the spirit of Keep a Changelog and will use semantic versioning once releases begin.

## [Unreleased]

### Server

- Added structured JSON API errors, security response headers, and structured request logging.
- Added one-time enrollment tokens, node credential verification, heartbeat freshness states, node deletion, and audit events.
- Added desired config storage, effective config preview, config diff support, signed config-apply jobs, restart jobs, and rollback jobs.
- Added bounded request body reads, paginated node jobs, paginated fleet node listing, audit filtering, heartbeat/job/audit retention pruning, and `/readyz` store connectivity checks.
- Added Prometheus-compatible metrics for build info, heartbeats, job lifecycle counters, late job results, rollback counters, fleet freshness, and drift.

### Sidecar

- Added node enrollment, heartbeat reporting, job polling, deep-probe execution, runtime config snapshots, and discovery warnings.
- Added signed config plan verification, dry-run/live config apply gates, backup metadata, rollback execution, restart execution, and health-check/rollback reporting.
- Added sidecar `doctor` diagnostics and read-only local smoke coverage.

### CLI

- Added `fleet status`, `node inspect`, `node remove`, `probe`, `jobs list`, `audit list`, `config get`, `config set`, `config preview`, `config apply`, `restart`, `rollback`, enrollment token creation, and version output.
- Added table and JSON output paths, wait/poll flows for operator jobs, bounded list flags, and compatibility with legacy and paginated node-list responses.

### Web UI

- Added compact fleet overview with sorting, filtering, search, status badges, drift badges, runtime warning display, copyable identifiers, and keyboard shortcuts.
- Added node detail workflows for job history expansion, config previews, config apply, restart, rollback, audit pagination, enrollment token creation, and operator token controls.
- Added OpenAPI-generated TypeScript API types.

### Infrastructure

- Added embedded web assets served by `sideplane-server`, Docker Compose deployment, hardened systemd units, verified release download support in `install.sh`, release artifact workflow, CI timeouts, race/smoke checks, OpenAPI contract checks, and community issue/PR templates.
- Added README, security policy, roadmap, contributing guide, changelog, and operator docs for live-write preflight, read-only sidecar deployment, config apply, and smoke testing.

### Adapters

- Added read-only Hermes and OpenClaw adapters with runtime discovery, config hash/provider/model snapshots, provider/model validation, JSON syntax validation, and warning reporting.
