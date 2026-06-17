# Real-Machine Read-Only Smoke Test Checklist

Run this checklist on one disposable or low-risk real machine before any config apply work begins.

Do not test config writes, runtime restarts, upgrades, rollback, or destructive cleanup in this phase.

## Setup

- [ ] Choose a server host on a trusted LAN, VPN, or firewalled VPS.
- [ ] Set `SIDEPLANE_OPERATOR_TOKEN` before starting `sideplane-server`.
- [ ] Serve the built web UI with `--web-dir ./web/dist`.
- [ ] Keep the server behind TLS or a trusted network boundary if it is not purely local.
- [ ] Choose one sidecar node: a local PC, LAN server, or low-risk VPS.
- [ ] Confirm no inbound sidecar port is opened or required.
- [ ] Confirm the sidecar node can make outbound HTTP/HTTPS connections to the server.

## Enrollment

- [ ] Create an enrollment token from a trusted operator shell.
- [ ] Enroll the sidecar with `sideplane-sidecar enroll`.
- [ ] Confirm the state file exists at the expected path.
- [ ] Confirm the state file is readable by the sidecar service user and not world-readable.
- [ ] Start the sidecar manually or through systemd.

## Heartbeat And Freshness

- [ ] Confirm the node appears in `GET /api/nodes`.
- [ ] Confirm the node appears in the Fleet UI.
- [ ] Confirm the node becomes `fresh` while the sidecar is running.
- [ ] Stop the sidecar and confirm the node becomes `stale` after the configured stale interval.
- [ ] Keep it stopped and confirm the node becomes `offline` after the configured offline interval.
- [ ] Restart the sidecar and confirm it returns to `fresh`.

## Deep Probe

- [ ] Enter the operator token in the Fleet UI.
- [ ] Create one `Deep Probe` from the UI.
- [ ] Confirm a duplicate `Deep Probe` cannot be queued while one is pending or claimed.
- [ ] Confirm the job reaches `completed`.
- [ ] Confirm the completed result contains a `runtimes` array.
- [ ] Confirm the completed result contains a `configSnapshots` array.
- [ ] Confirm missing Hermes/OpenClaw installations are not fatal.

## Config Snapshot Discovery

- [ ] Confirm detected runtime names and types look correct.
- [ ] Confirm provider/model fields are empty, redacted, or accurate; never raw API keys.
- [ ] Confirm config source/path is empty or accurate.
- [ ] Confirm warnings are understandable.
- [ ] If expected config details are missing, record install path, config path, service user, and file permissions.

## Operator Token Behavior

- [ ] With `SIDEPLANE_OPERATOR_TOKEN` set, create a `Deep Probe` without a UI token and confirm `401`.
- [ ] Enter the correct token and confirm `Deep Probe` succeeds.
- [ ] Try a wrong token and confirm mutation requests fail.
- [ ] Confirm read-only fleet refresh still works without sending an operator token.

## NAT And Outbound-Only Behavior

- [ ] Put the sidecar on a private or NATed network if available.
- [ ] Confirm the sidecar can enroll and heartbeat using outbound connectivity only.
- [ ] Confirm no inbound firewall rule is needed for the sidecar.
- [ ] If the server is public, confirm only the intended server ports are reachable.

## Logs To Collect On Failure

- [ ] `sideplane-server` startup logs.
- [ ] `sideplane-server` logs around enrollment, heartbeat, and job creation.
- [ ] `journalctl -u sideplane-sidecar -n 200 --no-pager`.
- [ ] The sidecar state file path and ownership.
- [ ] `which hermes` and `which openclaw` from the same user that runs the sidecar.
- [ ] Any expected runtime config file paths and permissions.
- [ ] Browser console errors from the Fleet UI.
- [ ] Server URL, network layout, TLS/proxy/firewall notes, and NAT behavior.

## Observed Bugs

Record every mismatch for the next bug-fix phase, especially:

- Hermes/OpenClaw actual install paths.
- Config file formats and locations.
- Service manager differences.
- User permissions.
- Path and home-directory differences under systemd.
- VPS firewall or TLS issues.
- NAT/outbound connectivity behavior.
- API auth behavior over a public endpoint.

## Stop Condition

Stop after this read-only checklist has been run on at least one real machine. Do not implement config apply, config file writes, runtime restart, upgrade, rollback, destructive cleanup, or service-manager integration that changes live services until the checklist results have been reviewed.
