#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SQLC_VERSION="${SQLC_VERSION:-1.27.0}"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

BIN_DIR="${ROOT_DIR}/.tmp/tools/sqlc/${SQLC_VERSION}/${OS}-${ARCH}"
BIN_PATH="${BIN_DIR}/sqlc"

ensure_sqlc() {
  if [[ -x "${BIN_PATH}" ]]; then
    return
  fi

  mkdir -p "${BIN_DIR}"
  archive="${BIN_DIR}/sqlc.tar.gz"
  url="https://github.com/sqlc-dev/sqlc/releases/download/v${SQLC_VERSION}/sqlc_${SQLC_VERSION}_${OS}_${ARCH}.tar.gz"
  curl -fsSL "${url}" -o "${archive}"
  tar -xzf "${archive}" -C "${BIN_DIR}"
}

ensure_sqlc
exec "${BIN_PATH}" "$@"
