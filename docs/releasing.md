# Releasing Sideplane

Sideplane releases are operator-controlled. Normal CI and merge activity should
not publish artifacts, push tags, or create GitHub releases.

## Version Metadata

All three binaries read build metadata from
`github.com/wucm667/sideplane/internal/buildinfo`:

```bash
BUILDINFO_PKG=github.com/wucm667/sideplane/internal/buildinfo
VERSION=v0.1.0
COMMIT=$(git rev-parse --short HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

go build -trimpath -ldflags "\
  -X ${BUILDINFO_PKG}.Version=${VERSION} \
  -X ${BUILDINFO_PKG}.Commit=${COMMIT} \
  -X ${BUILDINFO_PKG}.BuildDate=${BUILD_DATE}" \
  ./cmd/sideplane
```

`sideplane version`, `sideplane-server --version`, and
`sideplane-sidecar --version` should report the same version, commit, and build
date when built from the same release inputs. The CLI also supports
`sideplane version --json` for scripts.

## Local Release Candidate

Before tagging, build a local candidate without publishing anything:

```bash
make clean
make test
make typecheck
make openapi-check
make release-local VERSION=v0.1.0
```

`make release-local` writes local binaries to `dist/` using the same ldflags
path as release CI. It does not upload artifacts.

To reproduce the full release matrix locally, use `make release-dist`. It mirrors
the `release.yml` build (Linux `amd64`/`arm64` for `sideplane-server`,
`sideplane-sidecar`, and `sideplane`), rebuilds the Web UI for embedding,
cross-compiles with `-trimpath` and the buildinfo ldflags, and writes a
`SHA256SUMS` file into `dist/`:

```bash
make release-dist VERSION=v0.1.0
```

`dist/` is gitignored. `release-dist` never pushes or publishes anything; the tag
and GitHub release remain a manual maintainer step.

## Verify Release Artifacts

After `make release-dist`, run the verification smoke to confirm the artifacts
are intact and the server boots:

```bash
sh scripts/verify-release.sh
```

The script verifies every binary against `dist/SHA256SUMS`, prints each binary's
`--version`, then starts the server on a temporary port, confirms `/healthz`,
and shuts it down. It cleans up its temp state and mutates no real machine. The
release matrix is cross-compiled for Linux; when no dist binary can run on the
current host (for example on macOS), the script builds host-native equivalents
from source for the functional checks while still verifying the dist checksums.

## Release Workflow

The GitHub release workflow is tag-driven. When an operator intentionally pushes
a `v*` tag, the workflow:

1. Checks out the tagged source.
2. Installs Go and Node.
3. Builds the Web UI for embedding.
4. Builds Linux `amd64` and `arm64` binaries for `sideplane-server`,
   `sideplane-sidecar`, and `sideplane`.
5. Writes `SHA256SUMS` for the release assets.
6. Creates the GitHub release for that verified tag.

Do not push tags from automation in normal development. A release only begins
after a human has completed the checklist and intentionally pushed the tag.

## Cut A Tag Locally

Use an annotated or signed tag from a clean worktree:

```bash
git status --short
git log --oneline -5
git tag -a v0.1.0 -m "v0.1.0"
```

Pushing the tag is the publish action:

```bash
git push origin v0.1.0
```

Do not run that push until the pre-release checklist has passed.

## Verify Checksums

After the workflow creates a draft or release artifact set, download the assets
and verify checksums before installing on a node:

```bash
sha256sum -c SHA256SUMS
./sideplane --version
./sideplane-server --version
./sideplane-sidecar --version
```

The install script also verifies `SHA256SUMS` when run with `--version vX.Y.Z`.

## Smoke Checks

For a low-risk host or disposable VM:

1. Start `sideplane-server` with SQLite and an operator token.
2. Create an enrollment token.
3. Enroll a sidecar.
4. Confirm heartbeat freshness and runtime status in the Web UI.
5. Run a deep probe.
6. Create a dry-run config apply.
7. Export audit events.
8. Stop the sidecar and confirm stale/offline transitions.

Live config apply remains gated by the live-write preflight checklist.
