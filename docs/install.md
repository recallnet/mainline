# Install

`mainline` now ships source-controlled packaging outputs for Homebrew and Nix.

For direct downloads, GitHub releases also publish versioned `.tar.gz` archives
for macOS and Linux plus a `SHA256SUMS` file.

## Direct Download

Download the archive that matches your platform from the GitHub releases page:

```bash
https://github.com/recallnet/mainline/releases
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

## Go Install

```bash
go install github.com/recallnet/mainline/cmd/mainline@latest
go install github.com/recallnet/mainline/cmd/mq@latest
go install github.com/recallnet/mainline/cmd/mainlined@latest
```

## Homebrew

Install from the committed formula without cloning the repo manually:

```bash
brew install --HEAD https://raw.githubusercontent.com/recallnet/mainline/main/Formula/mainline.rb
```

This installs:

- `mainline`
- `mq`
- `mainlined`

The formula source is in [Formula/mainline.rb](/Users/devrel/Projects/recallnet/mainline/Formula/mainline.rb).

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
