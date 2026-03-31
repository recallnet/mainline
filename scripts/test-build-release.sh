#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
output_dir="$(mktemp -d)"
extract_dir="$(mktemp -d)"
scratch_dir="$(mktemp -d)"
trap 'rm -rf "${output_dir}" "${extract_dir}" "${scratch_dir}"' EXIT

(
  cd "${scratch_dir}"
  "${repo_root}/scripts/build-release.sh" --version v0.0.0-test --output "${output_dir}"
)

for archive in \
  "${output_dir}/mainline_v0.0.0-test_darwin_amd64.tar.gz" \
  "${output_dir}/mainline_v0.0.0-test_darwin_arm64.tar.gz" \
  "${output_dir}/mainline_v0.0.0-test_linux_amd64.tar.gz" \
  "${output_dir}/mainline_v0.0.0-test_linux_arm64.tar.gz"
do
  test -f "${archive}"
done

test -f "${output_dir}/SHA256SUMS"
tar -tzf "${output_dir}/mainline_v0.0.0-test_linux_amd64.tar.gz" | grep -q 'mainline_v0.0.0-test_linux_amd64/mainline$'

host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
host_arch="$(uname -m)"
case "${host_arch}" in
  x86_64) host_arch="amd64" ;;
  arm64|aarch64) host_arch="arm64" ;;
  *)
    echo "unsupported host arch: ${host_arch}" >&2
    exit 1
    ;;
esac

host_archive_base="mainline_v0.0.0-test_${host_os}_${host_arch}"
tar -xzf "${output_dir}/${host_archive_base}.tar.gz" -C "${extract_dir}"
"${extract_dir}/${host_archive_base}/mainline" version | grep -q '^mainline v0.0.0-test '
expected_commit="$(git -C "${repo_root}" rev-parse --short HEAD)"
"${extract_dir}/${host_archive_base}/mainline" version | grep -q "commit=${expected_commit}"
