#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
shift || true

if [[ -z "${mode}" ]]; then
  echo "usage: $0 <pre-commit|pre-push>" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "${repo_root}"

# Git hooks export repo-local env like GIT_DIR and GIT_WORK_TREE. Clear them so
# nested test repos and helper git commands resolve against their own cwd.
while read -r git_env_var; do
  unset "${git_env_var}"
done < <(git rev-parse --local-env-vars)

if ! command -v go >/dev/null 2>&1; then
  echo "❌ Go is required for hook checks" >&2
  exit 1
fi

die() {
  echo "❌ $*" >&2
  exit 1
}

changed_cached() {
  git diff --cached --name-only -- "$@" 2>/dev/null || true
}

current_branch() {
  git symbolic-ref --quiet --short HEAD 2>/dev/null || echo detached
}

check_staged_go_format() {
  local staged_go unformatted
  staged_go="$(changed_cached '*.go')"
  if [[ -z "${staged_go}" ]]; then
    return 0
  fi

  unformatted="$(printf '%s\n' "${staged_go}" | xargs gofmt -l 2>/dev/null || true)"
  if [[ -n "${unformatted}" ]]; then
    echo "❌ gofmt required for staged Go files:" >&2
    printf '%s\n' "${unformatted}" >&2
    echo "   Run: gofmt -w <files> && git add <files>" >&2
    exit 1
  fi
}

check_repo_format() {
  local unformatted
  unformatted="$(gofmt -l .)"
  if [[ -n "${unformatted}" ]]; then
    echo "❌ repository has unformatted Go files:" >&2
    printf '%s\n' "${unformatted}" >&2
    echo "   Run: make fmt" >&2
    exit 1
  fi
}

check_secrets_in_staged_files() {
  local staged
  staged="$(git diff --cached --name-only --diff-filter=ACMR 2>/dev/null || true)"
  if [[ -z "${staged}" ]]; then
    return 0
  fi

  python3 - <<'PY'
import re
import subprocess
import sys

files = subprocess.check_output(
    ["git", "diff", "--cached", "--name-only", "--diff-filter=ACMR"],
    text=True,
).splitlines()
pattern = re.compile(r"(password|secret|api[_-]?key|token|credential)\s*[:=]\s*['\"][^'\"]+['\"]", re.I)
bad = []
for rel in files:
    try:
        text = subprocess.check_output(["git", "show", f":{rel}"], text=True, stderr=subprocess.DEVNULL)
    except subprocess.CalledProcessError:
        continue
    except UnicodeDecodeError:
        continue
    if pattern.search(text):
        bad.append(rel)
if bad:
    print("❌ hardcoded secrets detected in staged files:", file=sys.stderr)
    for rel in bad:
        print(rel, file=sys.stderr)
    sys.exit(1)
PY
}

check_workflows_if_staged() {
  local workflows
  workflows="$(changed_cached '.github/workflows/*.yml' '.github/workflows/*.yaml')"
  if [[ -z "${workflows}" ]]; then
    return 0
  fi
  if ! command -v actionlint >/dev/null 2>&1; then
    die "actionlint is required when workflow files are staged"
  fi
  printf '%s\n' "${workflows}" | xargs actionlint
}

run_release_regressions_if_staged() {
  local release_inputs
  release_inputs="$(changed_cached \
    'scripts/build-release.sh' \
    'scripts/test-build-release.sh' \
    'scripts/generate-homebrew-formula.sh' \
    'scripts/generate-release-manifest.sh' \
    '.github/workflows/release.yml' \
    '.github/workflows/ci.yml' \
    'Formula/*.rb' \
    'docs/install.md' \
    'Makefile')"
  if [[ -z "${release_inputs}" ]]; then
    return 0
  fi

  ./scripts/test-build-release.sh
  local out
  out="$(mktemp -d)"
  ./scripts/build-release.sh --version v0.0.0-hook --output "${out}"
  ./scripts/generate-homebrew-formula.sh --version v0.0.0-hook --checksums "${out}/SHA256SUMS" --output "${out}/mainline.rb"
  ruby -c "${out}/mainline.rb" >/dev/null
  ./scripts/generate-release-manifest.sh --version v0.0.0-hook --checksums "${out}/SHA256SUMS" --output "${out}/release-manifest.json"
  rm -rf "${out}"
}

run_full_repo_suite() {
  check_repo_format
  go vet ./...
  go test ./...
  make test-invariants
  ./scripts/test-build-release.sh
  make build
  local out
  out="$(mktemp -d)"
  ./scripts/build-release.sh --version v0.0.0-hook --output "${out}"
  ./scripts/generate-homebrew-formula.sh --version v0.0.0-hook --checksums "${out}/SHA256SUMS" --output "${out}/mainline.rb"
  ruby -c "${out}/mainline.rb" >/dev/null
  ./scripts/generate-release-manifest.sh --version v0.0.0-hook --checksums "${out}/SHA256SUMS" --output "${out}/release-manifest.json"
  rm -rf "${out}"
}

check_remote_main_not_ahead() {
  if ! git remote get-url origin >/dev/null 2>&1; then
    return 0
  fi

  git fetch origin main --quiet 2>/dev/null || true
  if ! git show-ref --verify --quiet refs/remotes/origin/main; then
    return 0
  fi

  local behind
  behind="$(git rev-list --count HEAD..origin/main 2>/dev/null || echo 0)"
  if [[ "${behind}" != "0" ]]; then
    echo "❌ pre-push blocked: origin/main is ${behind} commit(s) ahead" >&2
    echo "   Fix: git fetch origin main && git rebase origin/main" >&2
    exit 1
  fi
}

check_no_uncommitted_changes() {
  local dirty
  dirty="$(git status --short 2>/dev/null || true)"
  if [[ -n "${dirty}" ]]; then
    echo "❌ pre-push blocked: uncommitted changes detected" >&2
    printf '%s\n' "${dirty}" >&2
    exit 1
  fi
}

check_push_target_policy() {
  local pushes_main=0 _local_ref _local_sha remote_ref _remote_sha
  while read -r _local_ref _local_sha remote_ref _remote_sha; do
    if [[ "${remote_ref}" == "refs/heads/main" ]]; then
      pushes_main=1
    fi
  done

  if [[ "${pushes_main}" == "1" ]] && [[ "$(current_branch)" != "main" ]]; then
    echo "❌ pre-push blocked: pushes to origin/main must come from local branch main" >&2
    echo "   Use mq to land work onto protected main before pushing." >&2
    exit 1
  fi
}

case "${mode}" in
  pre-commit)
    check_staged_go_format
    check_workflows_if_staged
    check_secrets_in_staged_files
    go vet ./...
    go test ./...
    make test-invariants
    run_release_regressions_if_staged
    ;;
  pre-push)
    export CI=true
    export TERM=dumb
    check_remote_main_not_ahead
    check_no_uncommitted_changes
    check_push_target_policy
    run_full_repo_suite
    ;;
  *)
    die "unknown hook mode: ${mode}"
    ;;
esac
