#!/usr/bin/env bash
set -euo pipefail

version=""
output=""
repo="recallnet/mainline"
checksums=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:?missing value for --version}"
      shift 2
      ;;
    --output)
      output="${2:?missing value for --output}"
      shift 2
      ;;
    --repo)
      repo="${2:?missing value for --repo}"
      shift 2
      ;;
    --checksums)
      checksums="${2:?missing value for --checksums}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "${version}" || -z "${output}" || -z "${checksums}" ]]; then
  echo "usage: $0 --version <tag> --checksums <SHA256SUMS> --output <path> [--repo owner/name]" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
commit="$(git -C "${repo_root}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
release_url="https://github.com/${repo}/releases/download/${version}"
checksums_asset="SHA256SUMS"
formula_asset="mainline.rb"

archives=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
)

json_escape() {
  python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"
}

{
  printf '{\n'
  printf '  "version": %s,\n' "$(json_escape "${version}")"
  printf '  "commit": %s,\n' "$(json_escape "${commit}")"
  printf '  "repository": %s,\n' "$(json_escape "${repo}")"
  printf '  "checksums_url": %s,\n' "$(json_escape "${release_url}/${checksums_asset}")"
  printf '  "homebrew_formula_url": %s,\n' "$(json_escape "${release_url}/${formula_asset}")"
  printf '  "assets": [\n'

  first=1
  for target in "${archives[@]}"; do
    # shellcheck disable=SC2086
    set -- ${target}
    goos="$1"
    goarch="$2"
    name="mainline_${version}_${goos}_${goarch}.tar.gz"
    sha="$(awk -v target="./${name}" '$2 == target { print $1 }' "${checksums}")"
    if [[ -z "${sha}" ]]; then
      echo "missing checksum for ${name}" >&2
      exit 1
    fi

    if [[ ${first} -eq 0 ]]; then
      printf ',\n'
    fi
    first=0

    printf '    {\n'
    printf '      "name": %s,\n' "$(json_escape "${name}")"
    printf '      "os": %s,\n' "$(json_escape "${goos}")"
    printf '      "arch": %s,\n' "$(json_escape "${goarch}")"
    printf '      "url": %s,\n' "$(json_escape "${release_url}/${name}")"
    printf '      "sha256": %s\n' "$(json_escape "${sha}")"
    printf '    }'
  done

  printf '\n  ]\n'
  printf '}\n'
} > "${output}"
