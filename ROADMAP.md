# Roadmap

Sideplane is pre-1.0 infrastructure. This roadmap describes the implemented
MVP baseline and near-term hardening direction; it is not a release commitment.

## Guiding Goal

Build a small, self-hosted control plane that can answer:

> What is each self-hosted agent running, and can Sideplane safely change it without breaking the node?

## Implemented MVP Baseline

The current repository includes these foundations:

- Go monorepo with `sideplane-server`, `sideplane-sidecar`, and `sideplane` CLI binaries.
- SQLite store with migrations, heartbeat retention, enrollment tokens, named operator tokens, node credentials, node labels, jobs, desired config history, rollout state, and audit events.
- REST API, OpenAPI contract, health/readiness endpoints, server-sent events with polling fallback, Prometheus-compatible metrics, structured request logging, rate limits, and security response headers.
- Sidecar-initiated enrollment, heartbeat, job polling, deep probe, signed config apply, dry-run/live apply gating, backup, restart, health check, and automatic rollback on failed live apply.
- Adapter interfaces with Hermes and OpenClaw read-only discovery/config snapshot support plus allowlisted restart controller support.
- Operator-managed labels and selector filtering for fleet views and staged rollouts.
- Backup inventory discovery from config-apply results with stable rollback references.
- Staged provider/model fleet rollouts with sequential batches, dry-run default, live drift health gates, pause/resume/abort controls, opt-in auto-rollback of a failed live batch's already-applied nodes, and reusable rollout templates.
- Production-ops surface: scoped operator tokens (admin/readonly), online SQLite backup with on-demand and scheduled retention, bulk deep probes and label assignment by selector, outbound alert webhooks with optional HMAC signing, audit-log export (ndjson/csv), and expected-sidecar-version drift flagging.
- Compact React/Vite Web UI for fleet overview metrics, node detail, labels, backup discovery, config diff/apply wizard, desired config history/revert, rollouts, audit history, enrollment, named tokens with scopes, alert webhooks, server settings, fleet multi-select bulk actions, a Cmd/Ctrl-K command palette, keyboard navigation, node removal, and job expansion.
- CLI coverage for fleet status, labels, bulk probe/label, rollouts and rollout templates, backups, named tokens with scopes, audit list/export, alert webhooks, server settings, desired config history/revert, config files, and shell completion.
- Docker Compose deployment, optional Prometheus/Grafana observability assets, Linux systemd units, install script, and server-embedded Web assets.

## MVP Hardening Next

Near-term work should make the existing operator path more complete and easier
to verify:

- Expand end-to-end API/CLI/Web regression coverage around rollout edge cases, token revocation, SSE reconnect, and desired-config revert.
- Improve sidecar doctor/read-only smoke workflows, store migration visibility, SQLite reliability settings, and retention observability.
- Continue hardening adapter validation, request-size limits, auth comparisons, path-safety tests, Docker/systemd packaging, release artifacts, and install checksum verification.
- Add release-oriented docs for operating rollouts, recovering from paused batches, and upgrading server/sidecar binaries.
- Keep UI density and accessibility polished as more operator workflows move from CLI to Web.

## Later

- PostgreSQL support after the SQLite MVP is boring and durable.
- macOS launchd and Windows service support.
- Multi-user/RBAC for small teams.
- Advanced rollout automation after the explicit pause/review path is proven.
- Additional runtime adapters.
- Secret backend integrations using references rather than an in-product secret manager.

## Non-Goals for the MVP

- General remote shell
- Full log aggregation platform
- Chat UI
- Prompt workspace
- Agent task board
- Multi-tenant SaaS control plane
- Marketplace/plugin ecosystem
- Kubernetes-first deployment or operator
- Sidecar binary self-update (the server flags version drift; it never downloads or runs new binaries)
