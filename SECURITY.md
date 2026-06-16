# Security Policy

Sideplane is security-sensitive infrastructure. It manages remote agent configuration, sidecar enrollment, lifecycle actions, restarts, and rollback.

## Supported Versions

Sideplane is not yet released. Security support will be defined once the first version is published.

## Reporting a Vulnerability

Please do not report security vulnerabilities in public issues.

Use GitHub's private vulnerability reporting feature if it is enabled for this repository. If it is not enabled, contact the repository owner through GitHub and request a private reporting channel.

Useful reports include:

- Affected component
- Reproduction steps
- Expected impact
- Logs or screenshots with secrets removed
- Suggested fix, if available

## Security-Sensitive Areas

Please be especially careful around:

- Enrollment token generation and exchange
- Long-lived node credentials
- Signed configuration plans
- Sidecar job execution
- Secret references
- Adapter command allowlists
- Config backup and rollback
- WebSocket and polling job channels
- Audit logs

## Non-Goals

Sideplane should not expose arbitrary remote shell execution. The sidecar should execute a narrow allowlist of lifecycle and adapter operations.

Secrets should not be stored inline in ordinary configuration or written to logs.

