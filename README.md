# mainline

Five agents share one machine.

They all have worktrees. They all fork from the same local `main`. They all run tests, rebase, fix conflicts, and eventually want to land. And because this is real life, `main` is also supposed to stay clean enough to push upstream all day.

Git gives you the primitives for that world. It does not give you a coordinator.

That coordinator is `mainline`.

`mainline` is a local control plane for parallel worktree development. It keeps topic work in topic worktrees, serializes integrations onto protected `main`, coalesces publishes so only the newest protected tip matters, and stores queue state durably so the machine can still explain itself after a crash.

This is not a new VCS. It is the missing piece between "Git has branches" and "ten humans and agents are all landing work on the same box."

---

## The Bug Is Not In Git

The failure mode here is subtle.

Nothing is obviously broken. Everyone is doing normal Git things:

- branch off `main`
- work in parallel
- rebase occasionally
- merge or push when done

That workflow is fine for one person.

It gets sloppy fast under local parallelism:

- one worktree rebases while another is still building on stale `main`
- conflicts are discovered late, on the protected branch, in the wrong checkout
- two publish attempts race even though only the newest protected tip matters
- an agent gets interrupted and nobody can tell what was queued, what was blocked, or what is safe to retry

The result is not some dramatic distributed systems incident. It is worse: low-grade local chaos. `main` stops being boring. People start treating the protected branch as a scratchpad. Queue state lives in scrollback and memory.

`mainline` takes that coordination problem and makes it explicit.

## The Core Idea

There is one rule that matters:

**`main` is not where feature work happens.**

Once you enforce that, the rest becomes mechanical:

- feature work happens in feature worktrees
- submissions are recorded durably
- integrations happen one at a time
- conflicts are resolved back in the source worktree
- publish is a separate queue
- operators can see the queue, retry items, cancel items, and watch the system progress

That is the whole shape of the tool.

Git stays in charge of refs, rebases, fast-forwards, pushes, and worktrees. SQLite stores the queue, locks, and event history. `mainline` coordinates the two.

## What Using It Feels Like

You do your normal work in a feature worktree. You commit there. Then you hand that branch to the local queue.

```bash
mq repo init --repo .
cd /path/to/topic-worktree
mq land
mq watch --repo /path/to/main
```

The short name is `mq` because that is what it is: the main queue for one machine.

### Land a branch

![Land a branch with mq](docs/demos/gifs/land.gif)

### Inspect durable queue history

![Inspect queue history with mq logs](docs/demos/gifs/logs.gif)

### Watch protected-branch state live

![Watch mainline operator state](docs/demos/gifs/watch.gif)

The demos are generated from source-controlled VHS tapes in [`docs/demos/tapes/`](docs/demos/tapes/) and can be re-rendered with:

```bash
./docs/demos/scripts/render.sh
```

## Why The Model Holds Up

The model is intentionally conservative.

- Git is still the source of truth
- `go-git` handles ordinary repository inspection and config work
- native `git` is used where exact write-path behavior matters
- SQLite gives durable local state instead of shell folklore
- the protected branch stays clean because it is treated as infrastructure, not a workspace

That matters if your machine is running Codex, Claude, factory daemons, humans, or all of them at once. `mainline` does not ask those actors to become perfectly disciplined. It gives them a coordinator that turns the safe path into the normal path.

## What Ships

`mainline` is already a real toolchain:

- repo discovery for standard repos and bare-clone-plus-worktree layouts
- durable SQLite queue state in shared Git storage
- serialized integration and publish workers
- `land` for one-command submit-plus-integrate-plus-publish
- `submit`, `run-once`, `publish`, `retry`, and `cancel`
- `status`, `watch`, `logs`, `events`, and `confidence`
- daemon mode through `mainlined`
- policy checks, hook coordination, and repo-managed hooks
- release archives, Homebrew assets, Nix packaging, and machine-readable release manifests
- versioned GitHub release package assets for package-manager automation

This repo also dogfoods the workflow. The committed worktree instructions live in [SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/worktree/SKILL.md), and the repo-specific guardrails live in [AGENTS.md](/Users/devrel/Projects/recallnet/mainline/AGENTS.md).

It also ships evidence, not just claims: stress runs, soak runs, certification runs against real repo layouts, and a `mq confidence` gate that tells you whether the current build has enough proof behind it.

## Install

Requires Go 1.25 or newer.

From source:

```bash
git clone git@github.com:recallnet/mainline.git
cd mainline
make build
make install-hooks
```

With `go install`:

```bash
go install github.com/recallnet/mainline/cmd/mainline@latest
go install github.com/recallnet/mainline/cmd/mq@latest
go install github.com/recallnet/mainline/cmd/mainlined@latest
```

Homebrew and Nix details are in [install.md](/Users/devrel/Projects/recallnet/mainline/docs/install.md).

## The Operator Surface

Setup and health:

```bash
mq repo init --repo /path/to/main --main-worktree /path/to/main
mq config edit --repo /path/to/main
mq doctor --repo /path/to/main
mq repo show --repo /path/to/main --json
```

Queue work:

```bash
cd /path/to/topic-worktree
mq submit --check --json
mq submit --check-only --json
mq submit --json
mq submit --wait --timeout 10m
mq land --json --timeout 30m
mq submit
mq run-once --repo /path/to/main
mq publish --repo /path/to/main
```

For factory or daemon callers, the intended handoff is:

- `mq submit --check --json` to fast-fail deterministic problems before queue mutation
- `mq submit --check-only --json` for the stricter dry-run path: clean worktree, current protected tip included, and no duplicate active submission for the same branch SHA
- `[integration].MaxQueueDepth` to stop dead queues from silently accumulating unbounded queued branches
- `mq submit --json` to record the branch and get a stable `submission_id`
- `mq submit --wait --timeout 10m` when an agent needs a blocking landed-or-blocked answer without implementing its own poll loop
- `mainlined` or `mq land` to carry the branch the rest of the way to integrated and published state

If `origin/main` advances before your branch reaches the front of the queue, that is normal queue work, not a manual repair job. `mainline` syncs protected `main` from upstream before integration when policy allows it, records that as a durable event, and only blocks if the branch now has a real rebase conflict.

`mq submit --wait` is integration-scoped, not publish-scoped. It exits `0` when the branch is integrated, `1` for blocked/failed/cancelled outcomes, and `2` on timeout.

If a repo sets `[integration].MaxQueueDepth`, `mq submit` will reject new queued work once that many submissions are already waiting. Use `mq submit --check-only --json` when an agent wants a cheap preflight without consuming queue capacity.

`[checks].CommandTimeout` is per-repo and now defaults to `5m`, which is a better fit for real pre-integrate gates. `mainline` still enforces a hard ceiling of `15m`, and a hung pre-integrate check blocks the submission with `blocked_reason = "check_timeout"` instead of letting one branch freeze the queue forever.

Observe and control:

```bash
mq status --repo /path/to/main --json
mq watch --repo /path/to/main
mq logs --repo /path/to/main --follow
mq retry --repo /path/to/main --submission 17
mq cancel --repo /path/to/main --publish 4
mq confidence --repo /path/to/main --json
mq version
```

Daemon mode:

```bash
mainlined --repo /path/to/main --interval 2s --json
```

## Where It Fits

Use `mainline` when:

- many worktrees share one machine
- `main` needs to stay pushable all day
- humans and coding agents are landing in parallel
- you want queue state to survive crashes and restarts
- you want a local operator view instead of reading raw `.git` state

Do not use it if your workflow is one person, one branch, one push, no parallelism. Plain Git is already excellent there.

## Repository Layouts

`mainline` supports both:

- normal repos with `.git/` inside the checked-out worktree
- bare-clone storage with linked worktrees, such as `~/Projects/.bare/owner/repo.git` with `~/Projects/owner/repo`

For bare-clone layouts, state and locks live with shared Git storage so every worktree sees the same queue truth.

## Architecture

The system is intentionally small.

- Git answers branch and worktree semantics
- SQLite answers ordering, durability, recovery, and audit history
- `mainline` coordinates the landing path between them

More detail is in [ARCHITECTURE.md](/Users/devrel/Projects/recallnet/mainline/docs/ARCHITECTURE.md).

## Development

```bash
make fmt
make test
make build
make install-hooks
```
