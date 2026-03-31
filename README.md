# mainline

Git already knows how to branch, rebase, fast-forward, and push.

What it does not know is how to behave when five agents on one machine are all
working in parallel, all using worktrees, and `main` is expected to stay
pushable all day.

That is the problem `mainline` solves.

`mainline` turns "please do not stomp on `main`" from a social rule into a
local coordination system:

- topic work happens in worktrees
- integrations onto `main` are serialized
- publishes are coalesced so the newest protected tip wins
- queue state survives crashes and restarts
- operators can see what happened, what is blocked, and what is next

If your machine is running humans, Codex, Claude, factory daemons, or all of
them at once, `mainline` keeps `main` boring without inventing a new VCS.

---

## The Failure Mode

The naive workflow looks harmless:

1. everyone branches off the same local `main`
2. everyone rebases whenever they remember
3. everyone eventually merges or pushes

It works for one person.

It degrades badly under parallelism:

- one worktree rebases, another pushes, a third keeps building on stale state
- `main` becomes the place conflicts get discovered and half-resolved
- publish jobs race even though only the latest protected tip matters
- nobody has a durable local record of what was queued, blocked, retried, or cancelled

The result is not a dramatic distributed-systems failure. It is worse: a slow
drip of local confusion, wasted rebases, dirty protected worktrees, and pushes
that happen in the wrong order.

`mainline` is the missing local coordinator.

## The Model

`mainline` keeps the model intentionally small:

- Git remains the source of truth for refs, worktrees, rebase, merge, and push semantics
- SQLite stores durable queue state, locks, and operator-visible history
- one canonical protected-branch worktree exists
- topic branches are submitted from their own worktrees
- integration happens by rebase-then-fast-forward
- publish requests are queued, coalesced, and drained separately

This is not a hosted service. It is a repo-local control plane for the Git
workflow you already have.

## What It Feels Like

You make changes in a feature worktree. You commit there. You submit that
branch to the queue. `mainline` lands it onto protected `main` in order. Then
it publishes the protected tip when you ask, or automatically if policy says so.

The short CLI is `mq`, because that is how it should feel in daily use.

```bash
mq repo init --repo .
mq submit --repo /path/to/topic-worktree
mq run-once --repo /path/to/main
mq publish --repo /path/to/main
mq watch --repo /path/to/main
```

### Land a branch

![Land a branch with mq](docs/demos/gifs/land.gif)

### Inspect durable queue history

![Inspect queue history with mq logs](docs/demos/gifs/logs.gif)

### Watch protected-branch state live

![Watch mainline operator state](docs/demos/gifs/watch.gif)

These demos are generated from source-controlled VHS tapes in
[`docs/demos/tapes/`](docs/demos/tapes/) and can be re-rendered with:

```bash
./docs/demos/scripts/render.sh
```

## Why This Works

Because it draws one hard line:

`main` is not where feature work happens.

That one rule unlocks the rest:

- all commits happen in topic worktrees
- conflicts are resolved in the source worktree, not on protected `main`
- integrations are serialized instead of "whoever pushes last"
- publish is treated as its own queue with coalescing semantics
- operator actions like retry and cancel become explicit state transitions instead of shell folklore

`mainline` does not replace Git discipline. It enforces the parts that matter
when many actors share the same machine.

## For Teams Running Agents

This project is built for the exact setup that breaks most local Git habits:

- multiple coding agents on one machine
- many linked worktrees
- a protected local `main`
- regular pushes to remote
- a need to know what is happening without reading raw `.git` state

The repo itself dogfoods this workflow. The committed worktree skill lives at
[.agents/skills/worktree/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/worktree/SKILL.md),
and the repo-specific guardrails live at
[AGENTS.md](/Users/devrel/Projects/recallnet/mainline/AGENTS.md).

The rule is simple:

- do all work in a feature worktree
- never mutate protected `main` with native `git`
- land through `mq`

## What Ships Today

`mainline` is already a real tool, not a sketch:

- repository discovery for standard repos and bare-clone-plus-worktree layouts
- durable SQLite state stored with shared Git storage
- per-repo integration and publish locking
- branch submission from feature worktrees
- serialized `run-once` integration onto protected `main`
- publish queue with newest-tip coalescing
- polling daemon mode through `mainlined`
- retry and cancel as explicit operator controls
- live operator surfaces through `status`, `watch`, `logs`, and `events`
- policy checks, hook coordination, and worktree layout warnings
- shell completions for `bash`, `zsh`, and `fish`
- Homebrew and Nix packaging
- tag-built GitHub release archives with checksums
- version-reporting binaries for support and release verification
- versioned Homebrew formula assets generated in GitHub releases

## Install

Requires Go 1.25 or newer.

From source:

```bash
git clone git@github.com:recallnet/mainline.git
cd mainline
make build
```

With `go install`:

```bash
go install github.com/recallnet/mainline/cmd/mainline@latest
go install github.com/recallnet/mainline/cmd/mq@latest
go install github.com/recallnet/mainline/cmd/mainlined@latest
```

Homebrew and Nix install details are in
[install.md](/Users/devrel/Projects/recallnet/mainline/docs/install.md).

## The Core Commands

Repo setup:

```bash
mq repo init --repo /path/to/main --main-worktree /path/to/main
mq config edit --repo /path/to/main
mq doctor --repo /path/to/main
mq repo show --repo /path/to/main --json
```

Queue work:

```bash
mq submit --repo /path/to/topic-worktree
mq run-once --repo /path/to/main
mq publish --repo /path/to/main
```

Operate the queue:

```bash
mq status --repo /path/to/main --json
mq watch --repo /path/to/main
mq logs --repo /path/to/main --follow
mq retry --repo /path/to/main --submission 17
mq cancel --repo /path/to/main --publish 4
mq version
```

Daemon mode:

```bash
mainlined --repo /path/to/main --interval 2s --json
```

## Repository Layouts

`mainline` supports both:

- normal repos with `.git/` in the checked-out worktree
- bare-clone storage with linked worktrees, such as
  `~/Projects/.bare/owner/repo.git` plus `~/Projects/owner/repo`

For bare-clone layouts, queue state and locks live with shared Git storage so
every worktree sees the same truth.

## Architecture

The design is intentionally conservative.

- `go-git` handles repository inspection, config, and ordinary ref/worktree operations
- native `git` is used where exact Git behavior matters, especially on the write path
- Git answers topology and branch semantics
- SQLite answers ordering, durability, coordination, and audit history

More detail is in [ARCHITECTURE.md](/Users/devrel/Projects/recallnet/mainline/docs/ARCHITECTURE.md).

## When To Use It

Use `mainline` when:

- your repo already uses worktrees
- `main` must stay clean and pushable
- multiple engineers or agents share one machine
- you want branch landing and publish to be deterministic
- you want an operator-visible record of queue state

Do not use it if your workflow is one person, one branch, one push, and no
parallelism. Git alone is already good at that.

## Development

```bash
make fmt
make test
make build
```

The deeper workflow examples live in
[FLOWS.md](/Users/devrel/Projects/recallnet/mainline/docs/FLOWS.md).
