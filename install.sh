#!/bin/sh
# kbridge CLI installer. Usage:
#   curl -fsSL https://raw.githubusercontent.com/why-xn/kbridge/main/install.sh | sh
#   KBRIDGE_VERSION=v1.0.0 KBRIDGE_INSTALL_DIR=~/.local/bin sh install.sh
set -eu

REPO="why-xn/kbridge"
INSTALL_DIR="${KBRIDGE_INSTALL_DIR:-/usr/local/bin}"

fail() { echo "install: $1" >&2; exit 1; }

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in linux|darwin) ;; *) fail "unsupported OS: $os" ;; esac
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "unsupported arch: $arch" ;;
esac

version="${KBRIDGE_VERSION:-}"
if [ -z "$version" ]; then
  version="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | cut -d'"' -f4)"
  [ -n "$version" ] || fail "could not resolve latest version"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
archive="kb_${version}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${version}"

echo "Downloading ${archive} ..."
curl -fsSL "${base}/${archive}" -o "${tmp}/${archive}" || fail "download failed"
curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt" || fail "checksums download failed"

echo "Verifying checksum ..."
( cd "$tmp" && grep " ${archive}\$" checksums.txt | sha256sum -c - ) || fail "checksum verification failed"

tar -xzf "${tmp}/${archive}" -C "$tmp" kb || fail "extract failed"

if [ -w "$INSTALL_DIR" ]; then
  install -m 0755 "${tmp}/kb" "${INSTALL_DIR}/kb"
else
  echo "Elevating to install into ${INSTALL_DIR} (set KBRIDGE_INSTALL_DIR to avoid sudo)"
  sudo install -m 0755 "${tmp}/kb" "${INSTALL_DIR}/kb"
fi

echo "Installed kb to ${INSTALL_DIR}/kb"
"${INSTALL_DIR}/kb" --version || true
