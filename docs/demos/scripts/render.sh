#!/bin/zsh
set -euo pipefail

cd /Users/devrel/Projects/recallnet/mainline
mkdir -p docs/demos/gifs
make build >/dev/null

vhs docs/demos/tapes/land.tape
vhs docs/demos/tapes/logs.tape
vhs docs/demos/tapes/watch.tape
