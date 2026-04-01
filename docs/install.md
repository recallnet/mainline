# Install

`mainline` now ships source-controlled packaging outputs for Homebrew and Nix.
GitHub binary releases are built by GoReleaser.

For direct downloads, GitHub releases publish versioned archives for macOS,
Linux, and Windows plus a `SHA256SUMS` file and a machine-readable
`release-manifest.json`.

## Direct Download

Download the archive that matches your platform from the GitHub releases page:

```bash
https://github.com/recallnet/mainline/releases
```

Automation can consume the release manifest directly:

```bash
curl -LO https://github.com/recallnet/mainline/releases/download/v0.1.0/release-manifest.json
curl -LO https://github.com/recallnet/mainline/releases/download/v0.1.0/release-manifest_v0.1.0.json
```

Tagged releases also publish a versioned package metadata bundle:

```bash
curl -LO https://github.com/recallnet/mainline/releases/download/v0.1.0/mainline_packages_v0.1.0.tar.gz
```

Archive naming:

- `mainline_<version>_darwin_amd64.tar.gz`
- `mainline_<version>_darwin_arm64.tar.gz`
- `mainline_<version>_linux_amd64.tar.gz`
- `mainline_<version>_linux_arm64.tar.gz`
- `mainline_<version>_windows_amd64.zip`
- `mainline_<version>_windows_arm64.zip`

Each archive contains:

- `mainline`
- `mq`
- `mainlined`
- `README.md`

Confirm the installed binary version:

```bash
mq version
mainline version
mainlined --version
```

Recommended first-time repo setup after install:

```bash
cd /path/to/repo-root
mq repo init --repo . --main-worktree .
git add mainline.toml
git commit -m "Initialize mainline repo policy"
./scripts/install-hooks.sh
./scripts/install-launch-agent.sh
mq repo root --repo . --json
```

`mq repo init` automatically registers the repo for the machine-global daemon
registry used by `mainlined --all`.
For ordinary repos, treat that root checkout as the canonical protected `main`.
Keep it clean and on `main`, because humans inspect it and the machine wrappers
build from it.
The machine-level `mq` and `mainlined` wrappers refuse to build from a dirty
root checkout so local binaries cannot silently drift away from the code humans
are reading.
Use `mq repo root --repo . --json` to confirm that the canonical root checkout
is trustworthy. If the root checkout is already clean and on the protected
branch but config drift points elsewhere, repair that with
`mq repo root --repo . --adopt-root`.

State compatibility:

- the SQLite queue store is schema-versioned
- legacy unversioned stores upgrade in place when opened by a newer binary
- if a store was created by a newer unsupported binary, `mainline` fails clearly instead of mutating it

## Go Install

```bash
go install github.com/recallnet/mainline/cmd/mainline@latest
go install github.com/recallnet/mainline/cmd/mq@latest
go install github.com/recallnet/mainline/cmd/mainlined@latest
```

## Homebrew

Stable tagged releases publish a versioned `mainline.rb` formula asset alongside
the release archives. GitHub releases also attach explicit versioned package
metadata assets so automation can pin exact filenames. Download the versioned
formula from the release page and install it:

```bash
curl -LO https://github.com/recallnet/mainline/releases/download/v0.1.0/mainline_v0.1.0.rb
brew install ./mainline_v0.1.0.rb
```

This installs:

- `mainline`
- `mq`
- `mainlined`

The repo also keeps a `--HEAD` development formula in [mainline.rb](/Users/devrel/Projects/recallnet/mainline/Formula/mainline.rb) for source-first installs.

## Nix

Install directly from the flake:

```bash
nix profile install github:recallnet/mainline#mainline
```

Run without installing:

```bash
nix run github:recallnet/mainline#mainline -- --help
nix run github:recallnet/mainline#mq -- --help
nix run github:recallnet/mainline#mainlined -- --help
```

The flake source is in [flake.nix](/Users/devrel/Projects/recallnet/mainline/flake.nix) and [package.nix](/Users/devrel/Projects/recallnet/mainline/nix/package.nix).

## Maintainer Release Flow

Build the full release artifact set locally:

```bash
make release-snapshot VERSION=v0.1.0
make package-release VERSION=v0.1.0
make goreleaser-check
```

This writes archives, `SHA256SUMS`, versioned package metadata assets, and a
versioned package bundle under `dist/`.

If you want to dry-run the GitHub archive path locally through the same release
engine used in CI:

```bash
make goreleaser-snapshot VERSION=v0.1.0
```

Push a version tag to publish a GitHub release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub will not show any Releases until the first `v*` tag has been pushed.

## Machine-Global Daemon

For a machine running many repos, prefer one `mainlined --all` process instead
of one idle daemon per repo.

Register each repo once:

```bash
cd /path/to/repo-root
mq repo init --repo . --main-worktree .
```

Run it directly:

```bash
mainlined --all --json --interval 2s
```

Or install the macOS launch agent:

```bash
./scripts/install-launch-agent.sh
```

The launch agent is opt-in. `mq repo init` registers repos for `--all` mode,
but it does not install the service by itself.

Verify the exact label:

```bash
launchctl print gui/$(id -u)/com.recallnet.mainline.global
```

If macOS reports:

```text
Could not find service "com.recallnet.mainline.global"
```

the service is simply not installed yet. Run
`./scripts/install-launch-agent.sh` and verify again.

## Completion Install

Bash:

```bash
mainline completion bash > ~/.local/share/bash-completion/completions/mainline
mq completion bash > ~/.local/share/bash-completion/completions/mq
```

Zsh:

```bash
mkdir -p ~/.zsh/completions
mainline completion zsh > ~/.zsh/completions/_mainline
mq completion zsh > ~/.zsh/completions/_mq
```

Fish:

```bash
mkdir -p ~/.config/fish/completions
mainline completion fish > ~/.config/fish/completions/mainline.fish
mq completion fish > ~/.config/fish/completions/mq.fish
```
