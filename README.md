# mainline

Git is very good at branching.

Git is not a coordinator.

That distinction does not matter when one person is working in one checkout.
It matters a lot when one machine is running five agents, ten worktrees, a
factory daemon, and a protected local `main` that still needs to stay clean and
pushable all day.

That is what `mainline` is for.

`mainline` is a repo-local control plane for parallel worktree development. It
keeps feature work in feature worktrees, serializes integrations onto protected
`main`, coalesces publishes so only the newest protected tip matters, and keeps
the whole queue durable so the machine can still explain itself after a crash.

This is not a new VCS. It is the missing piece between “Git has branches” and
“many humans and agents are all trying to land code on the same box.”

---

## The Bug Is Local Coordination

The default workflow looks reasonable:

1. branch off `main`
2. work in parallel
3. rebase occasionally
4. merge or push when done

That works for one person.

It degrades fast under local parallelism:

- one worktree rebases while another keeps building on stale `main`
- conflicts get discovered on the protected branch instead of in the source worktree
- two publish attempts race even though only one protected tip actually matters
- an agent gets interrupted and nobody knows what was queued, blocked, retried, or cancelled

The failure mode is not dramatic. It is just expensive. `main` stops being
boring. Queue state lives in scrollback. Humans and agents start doing careful
Git surgery by hand.

`mainline` makes that coordination problem explicit and mechanical.

## The Model

There is one rule that matters:

**`main` is not where feature work happens.**

Once that line is real, the rest follows:

- the repo root checkout stays as the canonical protected `main`
- feature work happens in topic worktrees
- submissions are recorded durably
- integration is serialized
- conflicts are resolved in the source worktree
- publish is a separate queue
- operators can see, retry, cancel, and stream what is happening

Git still owns refs, rebases, fast-forwards, pushes, and worktrees. SQLite owns
queue state, locks, and event history. `mainline` coordinates the two.

## The Daily Shape

The short CLI is `mq`, because that is what it is: the main queue for one
machine.

The agent path should feel like this:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
mq submit --wait --timeout 15m --json
```

Or, when the caller wants a durable machine handle instead of a one-shot wait:

```bash
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
```

The controller path should feel like this:

```bash
mq land --json --timeout 30m
```

The daemon path should feel like this:

```bash
mainlined --all --interval 2s --json
mq events --repo /path/to/protected-main --follow --json --lifecycle
```

That is the product: one machine, one protected branch, many worktrees, one
queue.

### Land a branch

![Land a branch with mq](docs/demos/gifs/land.gif)

### Inspect durable queue history

![Inspect queue history with mq logs](docs/demos/gifs/logs.gif)

### Watch protected-branch state live

![Watch mainline operator state](docs/demos/gifs/watch.gif)

The demos are generated from source-controlled VHS tapes in
[`docs/demos/tapes/`](docs/demos/tapes/) and can be re-rendered with:

```bash
./docs/demos/scripts/render.sh
```

## Why It Holds Up

Because the model is conservative in the right places.

- Git is still the source of truth
- the protected branch is treated as infrastructure, not as a workspace
- the queue survives crashes and restarts
- conflicts get pushed back to the worktree that owns them
- publish is coalesced instead of “whatever finished pushing last”

This is exactly what you want when the machine is running Codex, Claude,
factory daemons, humans, or all of them at once. `mainline` does not ask those
actors to become perfectly disciplined. It turns the safe path into the normal
path.

## What Ships

`mainline` is already a real toolchain:

- standard repo and bare-clone-plus-worktree discovery
- durable SQLite queue state in shared Git storage
- serialized integration and publish workers
- branch submission from topic worktrees and detached SHAs
- `submit --check-only`, `submit --wait`, and one-shot `land`
- opportunistic submit-side draining when no worker already holds the lock
- `wait --submission <id> --for integrated|landed`
- `status`, `watch`, `logs`, `events`, `doctor`, and `confidence`
- daemon mode through `mainlined`
- retry and cancel as real operator controls
- policy checks, hook coordination, and repo-managed hooks
- Homebrew, Nix, GitHub release archives, checksums, and release manifests
- GoReleaser-driven multi-platform binary releases for macOS, Linux, and Windows

Recent hardening coverage now explicitly exercises the adoption-critical paths:

- concurrent multi-worktree submit, integrate, and publish flows
- deleted, moved, and dirtied source worktrees after submit
- queued branch head drift after submit
- external protected-branch advancement while queued work waits
- inherited `pre-push` hook success and failure-plus-retry publish paths
- crash/restart recovery around rebase, fast-forward, and push boundaries
- JSON contract tests for `status`, raw `events`, lifecycle `events`, `watch`, and daemon logs
- bare-repo plus linked-worktree daemon runs

This repo dogfoods that workflow. The repo-local worktree instructions live in
[.agents/skills/worktree/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/worktree/SKILL.md),
and the repo-specific guardrails live in
[AGENTS.md](/Users/devrel/Projects/recallnet/mainline/AGENTS.md).
Machine-readable JSON contracts and their compatibility policy are documented in
[JSON_CONTRACTS.md](/Users/devrel/Projects/recallnet/mainline/docs/JSON_CONTRACTS.md).
That document, not the internal Go structs, is the public automation contract.

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

Homebrew and Nix details are in
[install.md](/Users/devrel/Projects/recallnet/mainline/docs/install.md).

Recommended first-time repo setup after install:

```bash
cd /path/to/protected-main
mq repo init --repo . --main-worktree .
git add mainline.toml
git commit -m "Initialize mainline repo policy"
./scripts/install-hooks.sh
./scripts/install-launch-agent.sh
```

That init commit matters. It turns the repo’s queue policy into versioned,
reviewable state instead of one more local convention that agents have to infer.
`mq repo init` also registers the repo for `mainlined --all`, so one machine
daemon can drain many repos without one idle process per repo.
For normal repos, the root checkout should be the canonical protected `main`.
Keep it clean and boring. Humans inspect that path, and the machine wrapper
builds `mq` and `mainlined` from it. If it is dirty, the wrapper should refuse
to build rather than silently drift.
Use `mq repo root --repo . --json` to verify that the root checkout is still
trustworthy. Use `mq repo root --repo . --adopt-root` only when the root
checkout is already clean and on the protected branch.

## The Core Commands

Setup:

```bash
mq repo init --repo /path/to/protected-main --main-worktree /path/to/protected-main
mq repo root --repo /path/to/protected-main --json
mq repo audit --repo /path/to/protected-main --json
mq config edit --repo /path/to/protected-main
mq doctor --repo /path/to/protected-main --fix --json
```

Submit and land:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
mq submit --allow-newer-head --wait --timeout 15m --json
mq submit --wait --timeout 15m --json
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
mq land --json --timeout 30m
```

Operate and observe:

```bash
mq status --repo /path/to/protected-main --json
mq repo audit --repo /path/to/protected-main --json
mq watch --repo /path/to/protected-main
mq events --repo /path/to/protected-main --follow --json --lifecycle
mainlined --all --json
mq retry --repo /path/to/protected-main --submission 17
mq cancel --repo /path/to/protected-main --publish 4
```

## Repository Layouts

`mainline` supports both:

- normal repos with `.git/` inside the checked-out worktree
- bare-clone storage with linked worktrees, such as
  `~/Projects/.bare/owner/repo.git` with `~/Projects/owner/repo`

For bare-clone layouts, queue state and locks live with shared Git storage so
every worktree sees the same queue truth.

## Architecture

The design is intentionally small.

- `go-git` handles ordinary inspection and config work
- native `git` is used where exact write-path behavior matters
- Git answers topology and branch semantics
- SQLite answers ordering, durability, coordination, and audit history

More detail is in
[ARCHITECTURE.md](/Users/devrel/Projects/recallnet/mainline/docs/ARCHITECTURE.md).

## When To Use It

Use `mainline` when:

- many worktrees share one machine
- `main` must stay clean and pushable
- humans and agents are landing in parallel
- you want deterministic branch landing instead of ad hoc Git rituals
- you want queue state to survive crashes and restarts

Do not use it if your workflow is one person, one branch, one push, and no
parallelism. Plain Git is already excellent there.

## Development

```bash
make fmt
make test
make build
make install-hooks
```
