#!/usr/bin/env bash
set -euo pipefail

version=""
dist_dir="dist"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:?missing value for --version}"
      shift 2
      ;;
    --dist)
      dist_dir="${2:?missing value for --dist}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "${version}" ]]; then
  echo "usage: $0 --version <tag> [--dist <dir>]" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
if [[ "${dist_dir}" = /* ]]; then
  dist_root="${dist_dir}"
else
  dist_root="${repo_root}/${dist_dir}"
fi

formula_source="${dist_root}/mainline.rb"
manifest_source="${dist_root}/release-manifest.json"
checksums_source="${dist_root}/SHA256SUMS"
formula_target="${dist_root}/mainline_${version}.rb"
manifest_target="${dist_root}/release-manifest_${version}.json"
package_stage="${dist_root}/mainline_packages_${version}"
package_archive="${dist_root}/mainline_packages_${version}.tar.gz"

for path in "${formula_source}" "${manifest_source}" "${checksums_source}"; do
  if [[ ! -f "${path}" ]]; then
    echo "missing required release asset: ${path}" >&2
    exit 1
  fi
done

cp "${formula_source}" "${formula_target}"
cp "${manifest_source}" "${manifest_target}"

rm -rf "${package_stage}" "${package_archive}"
mkdir -p "${package_stage}"
cp "${formula_target}" "${package_stage}/"
cp "${manifest_target}" "${package_stage}/"
cp "${checksums_source}" "${package_stage}/"

if [[ -f "${repo_root}/flake.nix" ]]; then
  cp "${repo_root}/flake.nix" "${package_stage}/"
fi
if [[ -f "${repo_root}/flake.lock" ]]; then
  cp "${repo_root}/flake.lock" "${package_stage}/"
fi
if [[ -f "${repo_root}/nix/package.nix" ]]; then
  mkdir -p "${package_stage}/nix"
  cp "${repo_root}/nix/package.nix" "${package_stage}/nix/package.nix"
fi

tar -C "${dist_root}" -czf "${package_archive}" "$(basename "${package_stage}")"
rm -rf "${package_stage}"
