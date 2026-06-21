# Pre-Release Checklist

Use this checklist before pushing any `v*` tag.

- [ ] `git status --short` is clean except intentional ignored local files.
- [ ] `go test ./...` passes.
- [ ] `go test -race ./internal/store ./internal/server ./internal/sidecar ./internal/rollout` passes.
- [ ] `cd web && npm run typecheck && npm run test && npm run build` passes.
- [ ] `make openapi-check` passes.
- [ ] Generated Web API types are in sync with `docs/openapi.yaml` (`npm run generate:api` leaves no diff).
- [ ] The dated `CHANGELOG.md` section matches the tag and `[Unreleased]` is reset.
- [ ] `docs/releasing.md`, recovery docs, and operations docs are current.
- [ ] `make release-dist VERSION=vX.Y.Z` builds the Linux amd64/arm64 matrix with a `SHA256SUMS` file in `dist/`.
- [ ] `sh scripts/verify-release.sh` passes (checksums, `--version`, and a `/healthz` boot check).
- [ ] `sideplane version --json`, `sideplane-server --version`, and `sideplane-sidecar --version` report the intended version metadata.
- [ ] A low-risk smoke run covers enrollment, heartbeat, deep probe, dry-run config apply, rollback inventory, metrics, and audit export.
- [ ] No secrets, machine-private hostnames, IPs, or real config contents are committed.
- [ ] The tag push has explicit operator approval.

Pushing a `v*` tag is a publish action because the release workflow is tag-driven.
Do not push tags while preparing or reviewing a release candidate.
