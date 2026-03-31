#!/bin/zsh
set -euo pipefail

ROOT="/tmp/mainline-readme-demo"
REPO="$ROOT/demo"
FEATURE="$ROOT/feature-login"
ORIGIN="$ROOT/demo-origin.git"
BIN_DIR="/Users/devrel/Projects/recallnet/mainline/bin"

rm -rf "$ROOT"
mkdir -p "$ROOT"
cd "$ROOT"

git init --initial-branch=main demo >/dev/null
cd "$REPO"
git config user.name Hatch
git config user.email intents@textile.io

echo hello > README.md
git add README.md
git commit -m "initial commit" >/dev/null

"$BIN_DIR/mq" repo init --repo . >/dev/null
git add mainline.toml
git commit -m "configure mainline" >/dev/null

git init --bare "$ORIGIN" >/dev/null
git remote add origin "$ORIGIN"
git push -u origin main >/dev/null

git worktree add "$FEATURE" -b feature/login main >/dev/null
cd "$FEATURE"
echo fix > login.txt
git add login.txt
git commit -m "add login fix" >/dev/null

echo "$REPO"
