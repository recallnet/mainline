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
  "${output_dir}/mainline_v0.0.0-test_linux_arm64.tar.gz" \
  "${output_dir}/mainline_v0.0.0-test_windows_amd64.zip" \
  "${output_dir}/mainline_v0.0.0-test_windows_arm64.zip"
do
  test -f "${archive}"
done

test -f "${output_dir}/SHA256SUMS"
linux_archive_listing="${output_dir}/mainline_v0.0.0-test_linux_amd64.list"
windows_archive_listing="${output_dir}/mainline_v0.0.0-test_windows_amd64.list"
tar -tzf "${output_dir}/mainline_v0.0.0-test_linux_amd64.tar.gz" > "${linux_archive_listing}"
unzip -Z1 "${output_dir}/mainline_v0.0.0-test_windows_amd64.zip" > "${windows_archive_listing}"
grep -q 'mainline_v0.0.0-test_linux_amd64/mainline$' "${linux_archive_listing}"
grep -q '^mainline_v0.0.0-test_windows_amd64/mainline.exe$' "${windows_archive_listing}"

bare_checksums="${output_dir}/SHA256SUMS.bare"
sed 's# \./# #g' "${output_dir}/SHA256SUMS" > "${bare_checksums}"
"${repo_root}/scripts/generate-homebrew-formula.sh" --version v0.0.0-test --checksums "${bare_checksums}" --output "${output_dir}/mainline.rb"
"${repo_root}/scripts/generate-release-manifest.sh" --version v0.0.0-test --checksums "${bare_checksums}" --output "${output_dir}/release-manifest.json"
ruby -c "${output_dir}/mainline.rb" >/dev/null
grep -q '"name": "mainline_v0.0.0-test_darwin_amd64.tar.gz"' "${output_dir}/release-manifest.json"

goreleaser_checksums="${output_dir}/SHA256SUMS.goreleaser"
cp "${output_dir}/mainline_v0.0.0-test_darwin_amd64.tar.gz" "${output_dir}/mainline_0.0.0-test_darwin_amd64.tar.gz"
cp "${output_dir}/mainline_v0.0.0-test_darwin_arm64.tar.gz" "${output_dir}/mainline_0.0.0-test_darwin_arm64.tar.gz"
cp "${output_dir}/mainline_v0.0.0-test_linux_amd64.tar.gz" "${output_dir}/mainline_0.0.0-test_linux_amd64.tar.gz"
cp "${output_dir}/mainline_v0.0.0-test_linux_arm64.tar.gz" "${output_dir}/mainline_0.0.0-test_linux_arm64.tar.gz"
cp "${output_dir}/mainline_v0.0.0-test_windows_amd64.zip" "${output_dir}/mainline_0.0.0-test_windows_amd64.zip"
cp "${output_dir}/mainline_v0.0.0-test_windows_arm64.zip" "${output_dir}/mainline_0.0.0-test_windows_arm64.zip"
cat > "${goreleaser_checksums}" <<EOF
$(shasum -a 256 "${output_dir}/mainline_0.0.0-test_darwin_amd64.tar.gz" | sed "s#${output_dir}/##")
$(shasum -a 256 "${output_dir}/mainline_0.0.0-test_darwin_arm64.tar.gz" | sed "s#${output_dir}/##")
$(shasum -a 256 "${output_dir}/mainline_0.0.0-test_linux_amd64.tar.gz" | sed "s#${output_dir}/##")
$(shasum -a 256 "${output_dir}/mainline_0.0.0-test_linux_arm64.tar.gz" | sed "s#${output_dir}/##")
$(shasum -a 256 "${output_dir}/mainline_0.0.0-test_windows_amd64.zip" | sed "s#${output_dir}/##")
$(shasum -a 256 "${output_dir}/mainline_0.0.0-test_windows_arm64.zip" | sed "s#${output_dir}/##")
EOF
"${repo_root}/scripts/generate-homebrew-formula.sh" --version v0.0.0-test --checksums "${goreleaser_checksums}" --output "${output_dir}/mainline-goreleaser.rb"
"${repo_root}/scripts/generate-release-manifest.sh" --version v0.0.0-test --checksums "${goreleaser_checksums}" --output "${output_dir}/release-manifest-goreleaser.json"
ruby -c "${output_dir}/mainline-goreleaser.rb" >/dev/null
grep -q 'mainline_0.0.0-test_darwin_amd64.tar.gz' "${output_dir}/mainline-goreleaser.rb"
grep -q '"name": "mainline_0.0.0-test_darwin_amd64.tar.gz"' "${output_dir}/release-manifest-goreleaser.json"

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
