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

sha_for() {
  local name="$1"
  awk -v bare="${name}" -v dotted="./${name}" '$2 == bare || $2 == dotted { print $1; exit }' "${checksums}"
}

resolve_archive_name() {
  local goos="$1"
  local goarch="$2"
  local ext="$3"
  local with_tag="mainline_${version}_${goos}_${goarch}.${ext}"
  local stripped="mainline_${version#v}_${goos}_${goarch}.${ext}"
  if [[ -n "$(sha_for "${with_tag}")" ]]; then
    printf '%s\n' "${with_tag}"
    return
  fi
  if [[ -n "$(sha_for "${stripped}")" ]]; then
    printf '%s\n' "${stripped}"
    return
  fi
  printf '%s\n' "${with_tag}"
}

darwin_amd64_archive="$(resolve_archive_name darwin amd64 tar.gz)"
darwin_arm64_archive="$(resolve_archive_name darwin arm64 tar.gz)"
darwin_amd64_sha="$(sha_for "${darwin_amd64_archive}")"
darwin_arm64_sha="$(sha_for "${darwin_arm64_archive}")"

if [[ -z "${darwin_amd64_sha}" || -z "${darwin_arm64_sha}" ]]; then
  echo "missing darwin archive checksums in ${checksums}" >&2
  exit 1
fi

cat > "${output}" <<EOF
class Mainline < Formula
  desc "Local-first protected-branch coordinator for Git worktrees"
  homepage "https://github.com/${repo}"
  version "${version#v}"

  on_linux do
    odie "mainline Homebrew releases are macOS-only; use the GitHub release archives or Nix on Linux"
  end

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/${repo}/releases/download/${version}/${darwin_arm64_archive}"
      sha256 "${darwin_arm64_sha}"
    else
      url "https://github.com/${repo}/releases/download/${version}/${darwin_amd64_archive}"
      sha256 "${darwin_amd64_sha}"
    end
  end

  def install
    bin.install "mainline"
    bin.install "mq"
    bin.install "mainlined"
    prefix.install "README.md"
  end

  test do
    assert_match "mainline ${version}", shell_output("#{bin}/mainline version")
    assert_match "mq ${version}", shell_output("#{bin}/mq version")
    assert_match "mainlined ${version}", shell_output("#{bin}/mainlined --version")
  end
end
EOF
