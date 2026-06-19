#!/bin/sh
set -eu

install_server=0
install_sidecar=0
selected=0
assume_yes=0

usage() {
  cat <<'USAGE'
Usage: install.sh [--server] [--sidecar] [--yes]

Sets up Sideplane systemd units, env files, and directories on Linux.
When neither --server nor --sidecar is provided, both are installed.

Important: this script does not download binaries yet. Build and copy
sideplane-server and/or sideplane-sidecar to /usr/local/bin manually.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --server)
      install_server=1
      selected=1
      ;;
    --sidecar)
      install_sidecar=1
      selected=1
      ;;
    --yes|-y)
      assume_yes=1
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

if [ "$selected" -eq 0 ]; then
  install_server=1
  install_sidecar=1
fi

os=$(uname -s)
if [ "$os" != "Linux" ]; then
  echo "Sideplane install.sh supports Linux only; detected $os" >&2
  exit 1
fi

machine=$(uname -m)
case "$machine" in
  x86_64|amd64)
    arch=amd64
    ;;
  aarch64|arm64)
    arch=arm64
    ;;
  *)
    echo "unsupported architecture: $machine" >&2
    exit 1
    ;;
esac

if [ "$(id -u)" -ne 0 ]; then
  echo "install.sh must be run as root" >&2
  exit 1
fi

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
systemd_dir="$script_dir/deployments/systemd"

echo "Sideplane local systemd setup"
echo "  os: $os"
echo "  arch: $arch"
echo "  server: $install_server"
echo "  sidecar: $install_sidecar"
echo
echo "This script will:"
echo "  - create the sideplane user/group if missing"
echo "  - create /etc/sideplane and /var/lib/sideplane"
echo "  - copy selected systemd service files into /etc/systemd/system"
echo "  - copy env example files into /etc/sideplane without overwriting existing files"
echo
echo "Limitation: this script does NOT download binaries yet."
echo "Build binaries manually and copy them to /usr/local/bin for now."
echo

if [ "$assume_yes" -ne 1 ]; then
  printf "Continue? [y/N] "
  read answer
  case "$answer" in
    y|Y|yes|YES)
      ;;
    *)
      echo "aborted"
      exit 0
      ;;
  esac
fi

ensure_group() {
  if getent group sideplane >/dev/null 2>&1; then
    return
  fi
  if command -v groupadd >/dev/null 2>&1; then
    groupadd --system sideplane
    return
  fi
  if command -v addgroup >/dev/null 2>&1; then
    addgroup -S sideplane 2>/dev/null || addgroup --system sideplane
    return
  fi
  echo "cannot create sideplane group: groupadd/addgroup not found" >&2
  exit 1
}

ensure_user() {
  if id sideplane >/dev/null 2>&1; then
    return
  fi
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --gid sideplane --home-dir /var/lib/sideplane --shell /usr/sbin/nologin sideplane
    return
  fi
  if command -v adduser >/dev/null 2>&1; then
    adduser -S -G sideplane -h /var/lib/sideplane -s /sbin/nologin sideplane 2>/dev/null || \
      adduser --system --ingroup sideplane --home /var/lib/sideplane --shell /usr/sbin/nologin sideplane
    return
  fi
  echo "cannot create sideplane user: useradd/adduser not found" >&2
  exit 1
}

copy_env_example() {
  src=$1
  dest=$2
  if [ ! -e "$dest" ]; then
    cp "$src" "$dest"
  else
    echo "keeping existing $dest"
  fi
}

ensure_group
ensure_user

install -d -m 0755 /etc/sideplane
install -d -m 0755 /etc/systemd/system
install -d -m 0750 -o sideplane -g sideplane /var/lib/sideplane

if [ "$install_server" -eq 1 ]; then
  install -m 0644 "$systemd_dir/sideplane-server.service" /etc/systemd/system/sideplane-server.service
  copy_env_example "$systemd_dir/sideplane-server.env.example" /etc/sideplane/sideplane-server.env
fi

if [ "$install_sidecar" -eq 1 ]; then
  install -m 0644 "$systemd_dir/sideplane-sidecar.service" /etc/systemd/system/sideplane-sidecar.service
  copy_env_example "$systemd_dir/sideplane-sidecar.env.example" /etc/sideplane/sideplane-sidecar.env
fi

# TODO: add binary download once release CI is set up.

echo
echo "Next steps:"
if [ "$install_server" -eq 1 ]; then
  echo "  1. Build sideplane-server and copy it to /usr/local/bin/sideplane-server"
  echo "  2. Edit /etc/sideplane/sideplane-server.env and set SIDEPLANE_OPERATOR_TOKEN"
  echo "  3. Run: systemctl daemon-reload && systemctl enable --now sideplane-server"
fi
if [ "$install_sidecar" -eq 1 ]; then
  echo "  1. Build sideplane-sidecar and copy it to /usr/local/bin/sideplane-sidecar"
  echo "  2. Edit /etc/sideplane/sideplane-sidecar.env for this node"
  echo "  3. Enroll the sidecar before starting the service"
  echo "  4. Run: systemctl daemon-reload && systemctl enable --now sideplane-sidecar"
fi
