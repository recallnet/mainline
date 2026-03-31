# Install

`mainline` is source-first today.

## Go Install

```bash
go install github.com/recallnet/mainline/cmd/mainline@latest
go install github.com/recallnet/mainline/cmd/mq@latest
go install github.com/recallnet/mainline/cmd/mainlined@latest
```

## Homebrew

No tap is published yet. A local formula-style install can still build from source:

```bash
brew install go
git clone git@github.com:recallnet/mainline.git
cd mainline
make build
sudo install -m 0755 ./bin/mainline /usr/local/bin/mainline
sudo install -m 0755 ./bin/mq /usr/local/bin/mq
sudo install -m 0755 ./bin/mainlined /usr/local/bin/mainlined
```

## Nix

No packaged flake output is published yet. For now, use a dev shell or build from source inside your existing Nix environment.

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
