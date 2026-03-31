#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
output_dir="$(mktemp -d)"
trap 'rm -rf "${output_dir}"' EXIT

"${repo_root}/scripts/build-release.sh" --version v0.0.0-test --output "${output_dir}"

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
