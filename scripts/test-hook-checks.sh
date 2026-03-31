#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
temp_root="$(mktemp -d)"
trap 'rm -rf "${temp_root}"' EXIT

remote="${temp_root}/remote.git"
clone_a="${temp_root}/clone-a"
clone_b="${temp_root}/clone-b"
clone_c="${temp_root}/clone-c"

git init --bare "${remote}" >/dev/null
git clone --quiet "${repo_root}" "${clone_a}"
rsync -a --delete \
  --exclude '.git' \
  --exclude '.git/' \
  --exclude '.agents/skills/goal-planner/' \
  "${repo_root}/" "${clone_a}/"
(cd "${clone_a}" && git remote set-url origin "${remote}")
(cd "${clone_a}" && git config user.name hook-test)
(cd "${clone_a}" && git config user.email hook-test@example.com)
(cd "${clone_a}" && git push --quiet origin HEAD:main)
git clone --quiet "${remote}" "${clone_b}"
git clone --quiet "${remote}" "${clone_c}"

(cd "${clone_a}" && ./scripts/install-hooks.sh >/dev/null)
test "$(git -C "${clone_a}" config --get core.hooksPath)" = ".githooks"
test -x "${clone_a}/.githooks/pre-push"

(
  cd "${clone_a}"
  git add -A
  ./.githooks/pre-commit
  git commit --quiet --no-verify -m "sync hook test worktree"
)

(
  cd "${clone_a}"
  git checkout --quiet -b feature/hook-check
  if ./scripts/run-hook-checks.sh pre-push <<'EOF'
refs/heads/feature/hook-check HEAD refs/heads/main 0000000000000000000000000000000000000000
EOF
  then
    echo "expected pre-push to block non-main push-to-main" >&2
    exit 1
  fi
)

(
  cd "${clone_a}"
  :
)

rsync -a --delete \
  --exclude '.git' \
  --exclude '.git/' \
  --exclude '.agents/skills/goal-planner/' \
  "${repo_root}/" "${clone_b}/"
(cd "${clone_b}" && git config user.name hook-test)
(cd "${clone_b}" && git config user.email hook-test@example.com)
(cd "${clone_b}" && ./scripts/install-hooks.sh >/dev/null)
(
  cd "${clone_b}"
  git add -A
  ./.githooks/pre-commit
  git commit --quiet --no-verify -m "sync hook test worktree"
)

(
  cd "${clone_b}"
  if ./scripts/run-hook-checks.sh pre-push <<'EOF'
refs/heads/main HEAD refs/heads/main 0000000000000000000000000000000000000000
EOF
  then
    :
  else
    echo "expected pre-push to pass from local main" >&2
    exit 1
  fi
)

(
  cd "${clone_c}"
  git config user.name hook-test
  git config user.email hook-test@example.com
  echo "hook advance" >> README.md
  git commit --quiet -am "advance main"
  git push --quiet origin HEAD:main
)

(
  cd "${clone_b}"
  if ./scripts/run-hook-checks.sh pre-push <<'EOF'
refs/heads/main HEAD refs/heads/main 0000000000000000000000000000000000000000
EOF
  then
    echo "expected pre-push to block when origin/main is ahead" >&2
    exit 1
  fi
)
