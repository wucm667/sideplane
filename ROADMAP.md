# Roadmap

Sideplane is in early project initialization. This roadmap describes the intended direction, not a release commitment.

## Guiding Goal

Build a small, self-hosted control plane that can answer:

> What is each self-hosted agent running, and can Sideplane safely change it without breaking the node?

## Phase 0: Project Foundation

- Public README
- License
- Contribution guide
- Security policy
- Issue and pull request templates
- Initial architecture documentation

## Phase 1: Core Skeleton

- Go module
- `sideplane-server` command
- `sideplane-sidecar` command
- `sideplane` CLI command
- SQLite store
- Basic migrations
- Structured logging
- Health endpoint
- Prometheus-compatible metrics endpoint

## Phase 2: Enrollment and Heartbeat

- One-time enrollment tokens
- Node credential exchange
- Node registry
- Sidecar heartbeat
- Fresh/stale/offline status
- Basic fleet table in the web UI

## Phase 3: Runtime Adapters

- Adapter interface
- Hermes status adapter
- OpenClaw status adapter
- Runtime discovery
- Current provider/model reporting
- Config hash reporting
- Deep probe jobs

## Phase 4: Safe Configuration

- Layered desired configuration
- Effective config preview
- Current versus desired diff
- Signed config plan
- Sidecar backup
- Validation before apply
- Atomic config replace
- Restart when required
- Health check
- Automatic rollback on failure
- Audit log

## Later

- PostgreSQL support
- Better install script
- Docker Compose deployment
- systemd units
- macOS launchd support
- Multi-user/RBAC
- Rollout waves and canaries
- Additional runtime adapters
- Secret backend integrations

## Non-Goals for the MVP

- General remote shell
- Full log aggregation platform
- Chat UI
- Prompt workspace
- Agent task board
- Marketplace/plugin ecosystem
- Kubernetes-first deployment

