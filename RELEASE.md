# Pre-Release Checklist

Use this checklist before pushing any `v*` tag.

- [ ] `git status --short` is clean except intentional ignored local files.
- [ ] `go test ./...` passes.
- [ ] `go test -race ./internal/server ./internal/sidecar` passes for changed concurrency paths.
- [ ] `cd web && npm run typecheck && npm run build` passes.
- [ ] `make openapi-check` passes.
- [ ] Generated Web API types are in sync with `docs/openapi.yaml`.
- [ ] `CHANGELOG.md` or release notes mention operator-visible changes.
- [ ] `docs/releasing.md`, recovery docs, and operations docs are current.
- [ ] `make release-local VERSION=vX.Y.Z` builds local binaries.
- [ ] `sideplane version --json`, `sideplane-server --version`, and `sideplane-sidecar --version` report the intended version metadata.
- [ ] A low-risk smoke run covers enrollment, heartbeat, deep probe, dry-run config apply, rollback inventory, metrics, and audit export.
- [ ] No secrets, machine-private hostnames, IPs, or real config contents are committed.
- [ ] The tag push has explicit operator approval.

Pushing a `v*` tag is a publish action because the release workflow is tag-driven.
Do not push tags while preparing or reviewing a release candidate.
