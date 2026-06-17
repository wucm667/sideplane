# Read-Only Sidecar Deployment Guide

This guide is for the first real-machine read-only test stage.

Sideplane at this stage can enroll a sidecar, receive heartbeats, run deep probes, and report redacted config snapshot placeholders. It must not write runtime config files, restart services, upgrade runtimes, or roll back anything yet.

Use a disposable or low-risk node first. Do not start with a production machine that runs critical agent workloads.

After deployment, run the [real-machine read-only smoke test](real-machine-readonly-smoke-test.md).

## Server

Build the server, sidecar, CLI, and web assets:

```bash
go build -o bin/sideplane-server ./cmd/sideplane-server
go build -o bin/sideplane-sidecar ./cmd/sideplane-sidecar
go build -o bin/sideplane ./cmd/sideplane
npm --prefix web ci
npm --prefix web run build
```

Start the server on a protected LAN, VPN, or firewalled VPS:

```bash
export SIDEPLANE_OPERATOR_TOKEN='replace-with-a-long-random-token'
bin/sideplane-server \
  --addr :8080 \
  --db ./sideplane-readonly.db \
  --web-dir ./web/dist
```

Do not expose an unauthenticated Sideplane server on the public internet. If the server is on a VPS, put it behind TLS and restrict ingress to trusted IPs or a VPN. The operator token protects job creation, but this early deployment slice is still intended for controlled testing.

## Enrollment

Create a one-time enrollment token from the server host or a trusted operator machine:

```bash
bin/sideplane enrollment create --server http://SERVER_HOST:8080
```

On the test node, enroll the sidecar:

```bash
sudo install -m 0755 bin/sideplane-sidecar /usr/local/bin/sideplane-sidecar
sudo install -d -m 0750 -o root -g root /etc/sideplane
sudo install -d -m 0750 /var/lib/sideplane

sudo /usr/local/bin/sideplane-sidecar enroll \
  --server http://SERVER_HOST:8080 \
  --token ENROLLMENT_TOKEN \
  --node-id test-node-1 \
  --state /var/lib/sideplane/sidecar.json
```

The sidecar initiates outbound connections to the server. No inbound sidecar port is required.

## Systemd Sidecar

Create a dedicated user if needed:

```bash
sudo useradd --system --home /var/lib/sideplane --shell /usr/sbin/nologin sideplane
sudo chown -R sideplane:sideplane /var/lib/sideplane
```

Install the example service and environment file:

```bash
sudo install -m 0644 deployments/systemd/sideplane-sidecar.service /etc/systemd/system/sideplane-sidecar.service
sudo install -m 0640 deployments/systemd/sideplane-sidecar.env.example /etc/sideplane/sideplane-sidecar.env
sudoedit /etc/sideplane/sideplane-sidecar.env
```

Set:

```text
SIDEPLANE_SERVER_URL=http://SERVER_HOST:8080
SIDEPLANE_NODE_ID=test-node-1
SIDEPLANE_STATE_PATH=/var/lib/sideplane/sidecar.json
SIDEPLANE_HEARTBEAT_INTERVAL=30s
SIDEPLANE_JOB_POLL_INTERVAL=30s
```

Then start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now sideplane-sidecar
sudo systemctl status sideplane-sidecar
```

If the service user cannot see expected runtime commands or future read-only config paths, stop and record the permission/path issue. Do not loosen permissions broadly on a production node during this stage.

## Read-Only Checks

In the web UI:

- Enter the operator token.
- Confirm the node appears and heartbeats become fresh.
- Run a `Deep Probe`.
- Confirm the job completes.
- Confirm runtime status and config snapshot warnings/details are visible.

This stage is intentionally read-only. Do not implement or test config writes, runtime restarts, upgrades, rollback, or destructive cleanup until the real-machine read-only checklist has passed.
