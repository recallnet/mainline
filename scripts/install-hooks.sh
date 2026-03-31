#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

git -C "${repo_root}" config core.hooksPath .githooks
echo "configured core.hooksPath=$(git -C "${repo_root}" config --get core.hooksPath)"
