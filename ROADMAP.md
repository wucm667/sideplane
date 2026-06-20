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
- Rollout safety hardening: creation-time overlap guard for non-terminal rollout targets, `allowOverlap` override for intentional concurrency, scheduled rollout state in the API contract, and operator-visible conflict errors.
- Sidecar delivery resilience: latest-wins heartbeat retry and a bounded in-memory job-result retry buffer that drops the oldest queued result on overflow.
- Production-ops surface: scoped operator tokens (admin/readonly), online SQLite backup with on-demand and scheduled retention, server config-file loading with flag/env/file/default precedence, bulk deep probes and label assignment by selector, generic and Slack-compatible outbound alert webhooks, optional HMAC signing for generic webhooks, audit-log export (ndjson/csv), and expected-sidecar-version drift flagging.
- Compact React/Vite Web UI for fleet overview metrics, node detail, labels, backup discovery, config diff/apply wizard, desired config history/revert, rollouts, audit history, enrollment, named tokens with scopes, alert webhooks, server settings, fleet multi-select bulk actions, a Cmd/Ctrl-K command palette, keyboard navigation, node removal, and job expansion.
- CLI coverage for fleet status, labels, bulk probe/label, rollouts and rollout templates, rollout overlap override, backups, named tokens with scopes, audit list/export, alert webhooks with channel kind selection, server settings, desired config history/revert, config files, JSON version output, and shell completion.
- Edge-deployment surface: in-process TLS with an optional HTTP→HTTPS redirector, reverse-proxy/base-path serving, node maintenance mode (excluded from rollouts/bulk ops with suppressed alerts), scheduled rollouts via `startAt`, read-only runtime health checks (healthy/degraded/unknown), rollout and webhook Prometheus metrics, `whoami`/`status` endpoints, and acting-token-name attribution in audit and alerts.
- Docker Compose deployment, optional Prometheus/Grafana observability assets with example alert rules, Linux systemd units, install script, release/recovery runbooks, and server-embedded Web assets.
- Regression coverage around the operator lifecycle, token revocation and scopes, desired-config revert edges, Web SSE reconnect, and hardening UI surfaces.

## MVP Hardening Next

Near-term work should continue turning the implemented operator path into
boring day-2 infrastructure:

- Improve store migration visibility, SQLite reliability settings, and retention observability.
- Broaden adapter validation and path-safety tests as Hermes/OpenClaw configuration shapes stabilize.
- Keep Docker/systemd packaging, release artifact generation, and install checksum verification exercised in CI before every tag.
- Expand upgrade guidance after the first real pre-1.0 release candidate is cut and tested by operators.
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
- Built-in ACME/automatic certificate issuance (bring your own cert/key or terminate TLS at a reverse proxy)
- SMTP/email alerting in the hardening wave (alert channels are generic JSON and Slack-compatible webhooks)
