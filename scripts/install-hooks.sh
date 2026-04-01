#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

chmod +x "${repo_root}"/.githooks/*
git -C "${repo_root}" config core.hooksPath .githooks
echo "configured core.hooksPath=$(git -C "${repo_root}" config --get core.hooksPath)"
echo "installed repo hooks:"
printf '  %s\n' ".githooks/pre-commit" ".githooks/pre-push" ".githooks/prepare-commit-msg"
echo "next:"
echo "  1. commit mainline.toml with: git commit -m 'Initialize mainline repo policy'"
echo "  2. agents finish from topic worktrees with: mq submit --check-only --json && mq submit --wait --timeout 15m --json"
