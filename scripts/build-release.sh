#!/usr/bin/env bash
set -euo pipefail

version="dev"
output_dir="dist"
commit="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:?missing value for --version}"
      shift 2
      ;;
    --output)
      output_dir="${2:?missing value for --output}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

platforms=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
)

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
if [[ "${output_dir}" = /* ]]; then
  release_root="${output_dir}"
else
  release_root="${repo_root}/${output_dir}"
fi
rm -rf "${release_root}"
mkdir -p "${release_root}"

ldflags="-X github.com/recallnet/mainline/internal/app.Version=${version} -X github.com/recallnet/mainline/internal/app.Commit=${commit} -X github.com/recallnet/mainline/internal/app.Date=${date}"

build_archive() {
  local goos="$1"
  local goarch="$2"
  local stage_dir archive_base archive_path

  archive_base="mainline_${version}_${goos}_${goarch}"
  stage_dir="${release_root}/${archive_base}"
  archive_path="${release_root}/${archive_base}.tar.gz"

  mkdir -p "${stage_dir}"

  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build -trimpath -ldflags "${ldflags}" -o "${stage_dir}/mainline" ./cmd/mainline
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build -trimpath -ldflags "${ldflags}" -o "${stage_dir}/mq" ./cmd/mq
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build -trimpath -ldflags "${ldflags}" -o "${stage_dir}/mainlined" ./cmd/mainlined

  cp "${repo_root}/README.md" "${stage_dir}/README.md"
  if [[ -f "${repo_root}/LICENSE" ]]; then
    cp "${repo_root}/LICENSE" "${stage_dir}/LICENSE"
  fi

  tar -C "${release_root}" -czf "${archive_path}" "${archive_base}"
  rm -rf "${stage_dir}"
}

for platform in "${platforms[@]}"; do
  # shellcheck disable=SC2086
  build_archive ${platform}
done

(cd "${release_root}" && shasum -a 256 ./*.tar.gz > SHA256SUMS)
