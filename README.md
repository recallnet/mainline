# mainline

`mainline` is a local-first branch coordinator for Git repositories with a protected local branch, usually `main`.

It is designed to keep the protected branch clean while topic branches from many worktrees are integrated and published through a safe queue.

## Status

This repository currently implements Milestone 0 from the project plan:

- Go module and package skeleton
- `mainline` CLI entrypoint
- `mainlined` daemon entrypoint
- package boundaries for git, queue, state, policy, and worker logic
- build/test helpers
- CI for format, lint, test, and release build

The queue, repository inspection, and durable state features are planned next.

## Install

Build from source with Go 1.25 or newer:

```bash
git clone git@github.com:recallnet/mainline.git
cd mainline
make build
```

The binaries are written to `./bin`.

## Usage

```bash
mainline --help
mainlined --help
```

Current commands are intentionally minimal and document the upcoming command surface.

## Development

Common tasks:

```bash
make fmt
make lint
make test
make build
```

## Project Layout

- `cmd/mainline`: user-facing CLI
- `cmd/mainlined`: optional daemon entrypoint
- `internal/app`: shared CLI and daemon wiring
- `internal/git`: Git repository abstraction boundary
- `internal/policy`: repository policy types
- `internal/queue`: integration and publish queue types
- `internal/state`: durable state abstraction boundary
- `internal/worker`: worker coordination types

## License

TBD
