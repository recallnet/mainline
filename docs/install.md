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
mq repo init --repo .
git add mainline.toml
git commit -m "Initialize mainline repo policy"
./scripts/install-hooks.sh
mq repo root --repo . --json
```

`mq repo init` expects the protected worktree to be on a branch, not detached
HEAD. For ordinary repos, run it from local branch `main`. If you really intend
to protect a different branch, pass `--protected-branch` explicitly.
For ordinary repos, treat that root checkout as the canonical protected `main`.
Keep it clean and on `main`, because humans inspect it and the machine wrappers
build from it.
Do not run package-manager helpers like `npm skills` from that protected root
checkout; run them in the topic worktree you are changing so lockfile drift
does not block publish.
The machine-level `mq` and `mainlined` wrappers refuse to build from a dirty
root checkout so local binaries cannot silently drift away from the code humans
are reading.
Use `mq repo root --repo . --json` to confirm that the canonical root checkout
is trustworthy. If the root checkout is already clean and on the protected
branch but config drift points elsewhere, repair that with
`mq repo root --repo . --adopt-root`.

For bare-repository-plus-worktree layouts, there is no human-facing root
checkout at the bare repo path. In that topology, trust the configured
canonical protected worktree instead of expecting `root_checkout.exists = true`.

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

## Manual Helper Mode

`mainlined` still exists, but the default product model does not require a
standing daemon. Mutating `mq` commands queue work and then opportunistically
become the per-repo drainer themselves, staying alive until the repo is
quiescent.

For agent-heavy repos, strongly consider:

```toml
[publish]
Mode = 'auto'
```

With `Mode = 'manual'`, `mq submit --wait` only proves `integrated`. Remote
publish still requires `mq publish`, `mq land`, or `mq wait --for landed`.

With `Mode = 'auto'`, you can also wait through publish directly from submit:

```bash
mq submit --wait --for landed --timeout 30m --json
```

If you want to experiment manually with a long-lived helper process anyway, run
it directly:

```bash
mainlined --all --json --interval 2s
```

That path is optional and not part of the default machine setup.

If old deleted repos are still present in the optional global registry:

```bash
mq registry prune --json
```

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
