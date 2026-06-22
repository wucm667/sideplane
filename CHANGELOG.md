# Changelog

All notable changes to Sideplane will be documented in this file.

This project follows the spirit of Keep a Changelog and will use semantic versioning once releases begin.

## [Unreleased]

### Server

- Require operator authentication for fleet, audit, and configuration read endpoints (previously readable without a token).
- Added expected Hermes/OpenClaw runtime version settings, per-runtime outdated flags, and the `sideplane_fleet_runtime_outdated` metric.

### Sidecar

- Added runtime version capture from Docker image tags or opt-in single allowlisted read-only version commands.

## [0.1.0] - 2026-06-21

First tagged release. The sections below capture the capabilities shipped in the
0.1.0 baseline.

### Server

- Added structured JSON API errors, security response headers, and structured request logging.
- Added one-time enrollment tokens, node credential verification, heartbeat freshness states, node deletion, and audit events.
- Added operator-managed node labels, label selector filtering, named revocable operator tokens, server-sent events, and auth/enrollment rate limits.
- Added desired config storage, desired config history/revert, effective config preview, config diff support, signed config-apply jobs, restart jobs, and rollback jobs.
- Added per-node rollback backup inventory and staged fleet rollouts with create/list/get/action APIs plus background reconciliation.
- Added bounded request body reads, paginated node jobs, paginated fleet node listing, audit filtering, heartbeat/job/audit retention pruning, and `/readyz` store connectivity checks.
- Added Prometheus-compatible metrics for build info, heartbeats, job lifecycle counters, late job results, rollback counters, fleet freshness, and drift.
- Added opt-in rollout auto-rollback that rolls back a failed live batch's already-applied nodes before pausing, without recursion or touching real machines.
- Added admin/readonly operator token scopes with method-based enforcement and acting-token attribution in audit events.
- Added online SQLite backup with an on-demand `sideplane-server backup` subcommand and a scheduled, retention-pruned backup goroutine.
- Added bulk job creation (`/api/jobs/bulk`) and bulk label assignment (`/api/nodes/labels`) by selector or node set.
- Added outbound alert webhooks with a non-blocking, retry/timeout dispatcher and optional HMAC-SHA256 signing, plus webhook CRUD endpoints.
- Added audit-log export (`/api/audit/export`) streaming ndjson or csv with redaction, expected-sidecar-version settings with an outdated gauge, and reusable rollout templates with `templateId` prefill.
- Added in-process TLS serving with cert/key validation and an optional HTTP→HTTPS redirector, plus reverse-proxy/base-path serving with probes kept at the root.
- Added node maintenance mode (excluded from rollout/bulk targeting with suppressed offline/drift alerts) and scheduled rollouts via an optional `startAt` time.
- Added read-only runtime health checks, rollout and webhook Prometheus metrics, `whoami`/`status` endpoints, and acting-token-name attribution in audit events and rollout alerts.
- Added rollout overlap protection: new rollouts return `409` when resolved nodes are already in non-terminal rollouts unless `allowOverlap` is set.
- Added server config-file loading with flag/env/file/default precedence and a `sideplane-server config-file path` helper.
- Added Slack-compatible alert webhook channels while keeping HMAC signing scoped to generic JSON webhooks.

### Sidecar

- Added node enrollment, heartbeat reporting, job polling, deep-probe execution, runtime config snapshots, and discovery warnings.
- Added signed config plan verification, dry-run/live config apply gates, backup metadata, rollback execution, restart execution, and health-check/rollback reporting.
- Added allowlisted OpenClaw service restart controller parity with Hermes.
- Added sidecar `doctor` diagnostics and read-only local smoke coverage.
- Added read-only runtime health checks (healthy/degraded/unknown) reported in heartbeat snapshots without contacting external networks or restarting anything.
- Added latest-wins heartbeat retry and a bounded in-memory job-result retry buffer with oldest-result drop-on-overflow behavior.

### CLI

- Added `fleet status`, `node inspect`, `node remove`, `probe`, `jobs list`, `audit list`, `config get`, `config set`, `config preview`, `config apply`, `restart`, `rollback`, enrollment token creation, and version output.
- Added node label management, backup listing, rollout create/list/status/pause/resume/abort, named operator token create/list/revoke, desired config history/revert, config file loading, and shell completion generation.
- Added table and JSON output paths, wait/poll flows for operator jobs, bounded list flags, and compatibility with legacy and paginated node-list responses.
- Added bulk `probe --selector` and `node label --selector`, operator token `--scope`, rollout `--auto-rollback` and `--template`, `rollout template` create/list/delete, `audit export`, `webhook` create/list/delete, and `settings` get/set.
- Added `node maintenance --on|--off`, rollout `--start-at`, `whoami`, `status`, runtime health in `node inspect`, and acting-token-name display in audit output.
- Added rollout `--allow-overlap`, webhook `--kind generic|slack`, and JSON version output.

### Web UI

- Added compact fleet overview with sorting, filtering, search, status badges, drift badges, runtime warning display, copyable identifiers, and keyboard shortcuts.
- Added node detail workflows for labels, job history expansion, backup discovery, config previews, config apply, desired config history/revert, restart, rollback, audit pagination, enrollment token creation, and operator token controls.
- Added staged rollout creation/progress/actions, SSE live refresh with polling fallback, and fleet overview metrics.
- Added OpenAPI-generated TypeScript API types.
- Added Playwright visual smoke coverage for Fleet, Node detail, Activity, Enrollment, Config wizard, and Rollouts at desktop and mobile widths.
- Added operator token scope controls, alert webhook management, server settings, fleet multi-select bulk probe/label actions, sidecar-outdated badges, rollout template save/picker, and a Cmd/Ctrl-K command palette.
- Added node maintenance toggles and badges, scheduled-rollout start time input, runtime health badges, acting-token-name in the audit view, and server base-path awareness for API/SSE/asset URLs.
- Added rollout overlap controls and alert webhook kind selection, plus Playwright smoke coverage for SSE reconnect and hardening surfaces.

### Infrastructure

- Added embedded web assets served by `sideplane-server`, Docker Compose deployment, hardened systemd units, verified release download support in `install.sh`, release artifact workflow, CI timeouts, race/smoke checks, OpenAPI contract checks, and community issue/PR templates.
- Added optional Prometheus/Grafana compose assets and a pre-provisioned Sideplane dashboard.
- Added README, security policy, roadmap, contributing guide, changelog, and operator docs for live-write preflight, read-only sidecar deployment, config apply, fleet rollouts, and smoke testing.
- Added runtime-health Grafana panels, example Prometheus alert rules, OpenAPI refresh for hardening fields, release checklist/runbook docs, and operator recovery runbooks.

### Adapters

- Added read-only Hermes and OpenClaw adapters with runtime discovery, config hash/provider/model snapshots, provider/model validation, JSON syntax validation, and warning reporting.
- Added OpenClaw allowlisted service controller support for dry-run/live restart jobs.
- Added a read-only runtime health-check contract with local, allowlisted Hermes and OpenClaw implementations.
