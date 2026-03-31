# Install

`mainline` now ships source-controlled packaging outputs for Homebrew and Nix.

For direct downloads, GitHub releases also publish versioned `.tar.gz` archives
for macOS and Linux plus a `SHA256SUMS` file and a machine-readable
`release-manifest.json`.

## Direct Download

Download the archive that matches your platform from the GitHub releases page:

```bash
https://github.com/recallnet/mainline/releases
```

Automation can consume the release manifest directly:

```bash
curl -LO https://github.com/recallnet/mainline/releases/download/v0.1.0/release-manifest.json
```

Archive naming:

- `mainline_<version>_darwin_amd64.tar.gz`
- `mainline_<version>_darwin_arm64.tar.gz`
- `mainline_<version>_linux_amd64.tar.gz`
- `mainline_<version>_linux_arm64.tar.gz`

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
the release archives. Download that formula from the release page and install it:

```bash
curl -LO https://github.com/recallnet/mainline/releases/download/v0.1.0/mainline.rb
brew install ./mainline.rb
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
```

This writes archives and `SHA256SUMS` under `dist/`.

Push a version tag to publish a GitHub release:

```bash
git tag v0.1.0
git push origin v0.1.0
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
