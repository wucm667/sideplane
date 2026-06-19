#!/bin/sh
set -eu

install_server=0
install_sidecar=0
selected=0
assume_yes=0
version=
install_dir=/usr/local/bin
download=0
download_choice=auto

usage() {
  cat <<'USAGE'
Usage: install.sh [--server] [--sidecar] [--version TAG] [--install-dir DIR] [--no-download] [--yes]

Sets up Sideplane systemd units, env files, and directories on Linux.
When neither --server nor --sidecar is provided, both are installed.

By default this script preserves the local setup flow and does not download
binaries. Pass --version vX.Y.Z to download release binaries from GitHub,
verify SHA256SUMS, and install them before setting up systemd units.
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
    --version)
      if [ "$#" -lt 2 ]; then
        echo "--version requires a tag value" >&2
        exit 2
      fi
      version=$2
      download=1
      shift
      ;;
    --install-dir)
      if [ "$#" -lt 2 ]; then
        echo "--install-dir requires a directory" >&2
        exit 2
      fi
      install_dir=$2
      shift
      ;;
    --no-download)
      download=0
      download_choice=no
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

if [ "$download_choice" = no ] && [ -n "$version" ]; then
  echo "--version and --no-download cannot be used together" >&2
  exit 2
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
repo_owner=${SIDEPLANE_RELEASE_REPOSITORY:-wucm667/sideplane}

echo "Sideplane local systemd setup"
echo "  os: $os"
echo "  arch: $arch"
echo "  server: $install_server"
echo "  sidecar: $install_sidecar"
echo "  install dir: $install_dir"
if [ "$download" -eq 1 ]; then
  echo "  release version: $version"
else
  echo "  release download: disabled"
fi
echo
echo "This script will:"
echo "  - create the sideplane user/group if missing"
echo "  - create /etc/sideplane and /var/lib/sideplane"
if [ "$download" -eq 1 ]; then
  echo "  - download selected release binaries and verify SHA256SUMS"
else
  echo "  - leave binary installation to the local operator"
fi
echo "  - copy selected systemd service files into /etc/systemd/system"
echo "  - copy env example files into /etc/sideplane without overwriting existing files"
echo
if [ "$download" -ne 1 ]; then
  echo "Local mode: build binaries manually or rerun with --version vX.Y.Z."
fi
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

fetch_url() {
  url=$1
  dest=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -q -O "$dest" "$url"
    return
  fi
  echo "curl or wget is required to download release assets" >&2
  exit 1
}

sha256_file() {
  file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return
  fi
  echo "sha256sum or shasum is required to verify release assets" >&2
  exit 1
}

expected_sha256() {
  checksum_file=$1
  asset=$2
  awk -v asset="$asset" '$2 == asset || $2 == "./" asset { print $1; found=1 } END { if (!found) exit 1 }' "$checksum_file"
}

download_release_binary() {
  asset=$1
  binary_name=$2
  checksum_file=$3
  tmp_dir=$4
  url="https://github.com/$repo_owner/releases/download/$version/$asset"
  tmp_file="$tmp_dir/$asset"

  echo "Downloading $asset"
  fetch_url "$url" "$tmp_file"

  expected=$(expected_sha256 "$checksum_file" "$asset") || {
    echo "checksum entry for $asset not found in SHA256SUMS" >&2
    exit 1
  }
  actual=$(sha256_file "$tmp_file")
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $asset" >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi

  install -m 0755 "$tmp_file" "$install_dir/$binary_name"
}

ensure_group
ensure_user

install -d -m 0755 /etc/sideplane
install -d -m 0755 /etc/systemd/system
install -d -m 0750 -o sideplane -g sideplane /var/lib/sideplane

if [ "$download" -eq 1 ]; then
  install -d -m 0755 "$install_dir"
  tmp_dir=${TMPDIR:-/tmp}/sideplane-install.$$
  cleanup() {
    rm -rf "$tmp_dir"
  }
  trap cleanup EXIT HUP INT TERM
  mkdir -p "$tmp_dir"
  checksums="$tmp_dir/SHA256SUMS"
  fetch_url "https://github.com/$repo_owner/releases/download/$version/SHA256SUMS" "$checksums"
  if [ "$install_server" -eq 1 ]; then
    download_release_binary "sideplane-server_linux_$arch" "sideplane-server" "$checksums" "$tmp_dir"
  fi
  if [ "$install_sidecar" -eq 1 ]; then
    download_release_binary "sideplane-sidecar_linux_$arch" "sideplane-sidecar" "$checksums" "$tmp_dir"
  fi
fi

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
  if [ "$download" -eq 1 ]; then
    echo "  1. sideplane-server installed to $install_dir/sideplane-server"
  else
    echo "  1. Build sideplane-server and copy it to $install_dir/sideplane-server"
  fi
  echo "  2. Edit /etc/sideplane/sideplane-server.env and set SIDEPLANE_OPERATOR_TOKEN"
  echo "  3. Run: systemctl daemon-reload && systemctl enable --now sideplane-server"
fi
if [ "$install_sidecar" -eq 1 ]; then
  if [ "$download" -eq 1 ]; then
    echo "  1. sideplane-sidecar installed to $install_dir/sideplane-sidecar"
  else
    echo "  1. Build sideplane-sidecar and copy it to $install_dir/sideplane-sidecar"
  fi
  echo "  2. Edit /etc/sideplane/sideplane-sidecar.env for this node"
  echo "  3. Enroll the sidecar before starting the service"
  echo "  4. Run: systemctl daemon-reload && systemctl enable --now sideplane-sidecar"
fi
