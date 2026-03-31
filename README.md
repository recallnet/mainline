# mainline

Git is excellent at describing history.

It is not excellent at coordinating ten active worktrees, several coding
agents, a protected local `main`, and a machine where `origin/main` is expected
to stay pushable all day.

That gap is where teams start inventing local folklore:

- "just rebase before you merge"
- "don't commit on `main`"
- "someone check whether the daemon already pushed"
- "wait, which worktree landed that branch?"

Those rules hold right up until the machine gets busy.

`mainline` turns that social protocol into a local control plane.

It gives a repo one serialized integration path onto protected `main`, one
coalesced publish path to remote, one durable record of what happened, and one
operator surface for seeing the queue in motion.

If you run humans, Codex, Claude, factory daemons, or all of them at once,
`mainline` keeps `main` boring.

---

## The Problem

A modern local repo is no longer one branch and one shell.

It is a little cluster:

- many linked worktrees
- multiple agents editing in parallel
- periodic rebases
- direct pushes to remote
- background jobs that mutate the repo while someone else is still thinking

Plain Git still gives you the primitives:

- branch
- worktree
- rebase
- fast-forward
- push

What it does not give you is local coordination.

So the failure mode is predictable:

1. several branches fork from the same local `main`
2. they finish in a different order than they started
3. one rebases, one pushes, one is now stale, one discovers a conflict late
4. `main` becomes the place where ordering bugs and half-resolved conflicts show up

Nothing explodes. It just gets annoying, lossy, and hard to reason about.

That is the exact class of problem `mainline` is built to remove.

## The Thesis

Treat local branch landing the way a small database treats writes:

- serialize integration
- make state durable
- make operator actions explicit
- keep the hot path simple
- surface the queue instead of hiding it in shell history

`mainline` does not replace Git.

It wraps the Git workflow you already use with just enough coordination to make
parallel worktrees safe:

- feature work still happens on branches
- all commits still live in Git
- rebase and fast-forward semantics still come from Git
- push still means push

But now:

- topic branches are submitted instead of ad hoc merged
- integrations happen one at a time onto protected `main`
- publishes are coalesced so only the latest protected tip matters
- queue state survives restarts
- blocked, retried, cancelled, and published work has an audit trail

Git remains the source of truth for repository semantics.
SQLite becomes the source of truth for queue semantics.

That split is the whole design.

## The Model

There are only three moving parts:

1. A canonical protected-branch worktree

This is the only worktree that matters for landing and publishing. It stays
clean, boring, and easy to inspect.

2. Many topic worktrees

This is where all real work happens. Humans and agents make commits here. If a
rebase conflicts, the conflict stays here too.

3. A durable queue

Branches are submitted into the queue. A worker rebases them in order,
fast-forwards protected `main`, and separately drains publish requests to
remote.

That is it.

No service.
No distributed coordinator.
No custom VCS.
Just enough local structure so parallel work stops feeling random.

## What It Feels Like

The short CLI is `mq`.

In normal use it looks like this:

```bash
mq repo init --repo .
mq submit --repo /path/to/topic-worktree
mq run-once --repo /path/to/main
mq publish --repo /path/to/main
mq watch --repo /path/to/main
```

For a human, the workflow is simple:

- do all edits in a feature worktree
- commit there
- submit that branch
- let `mq` land it onto protected `main`
- let `mq` publish the protected tip

For the machine, the workflow is just as simple:

- one integration at a time
- one publish queue per repo
- newest publish target wins

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

## Why It Works

Because it draws one hard boundary:

`main` is not a feature worktree.

Once that rule is enforced, the rest gets easier:

- all commits happen on topic branches
- conflicts are resolved in the source worktree, not on protected `main`
- ordering becomes explicit instead of accidental
- publish becomes its own queue instead of a side effect of integration
- retry and cancel become state transitions instead of terminal folklore

This is the important part:

`mainline` is not clever.

It is intentionally conservative.

- `go-git` handles repository inspection, config, and ordinary ref/worktree operations
- native `git` is used where exact Git behavior matters on the write path
- SQLite stores durable queue state, locks, and event history

That is a feature, not a limitation. Coordination software should be boring.

## Why Agents Need This

Agents amplify throughput, but they also amplify local Git sloppiness.

One human making a slightly stale merge is tolerable.
Six agents doing it in parallel is a coordination bug generator.

`mainline` is built for the exact environment where normal Git etiquette stops
scaling:

- multiple coding agents on one machine
- many linked worktrees
- direct or frequent pushes to remote
- a protected local `main`
- a need to know what landed, what blocked, and what is next

The repo itself dogfoods this workflow. The committed worktree skill lives at
[.agents/skills/worktree/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/worktree/SKILL.md),
and the repo-specific guardrails live at
[AGENTS.md](/Users/devrel/Projects/recallnet/mainline/AGENTS.md).

The rule is not subtle:

- do work in a feature worktree
- do not mutate protected `main` with raw Git
- land through `mq`

## What Ships

This is already a working system:

- repository discovery for normal repos and bare-clone-plus-worktree layouts
- durable SQLite state stored with shared Git storage
- serialized submission, integration, and publish coordination
- the full operator surface: `submit`, `run-once`, `publish`, `status`, `confidence`, `watch`, `logs`, `events`, `retry`, and `cancel`
- daemon mode through `mainlined`
- hook-aware direct-to-`main` safety gates
- crash/restart recovery for orphaned running submissions and publishes
- shell completions for `bash`, `zsh`, and `fish`
- Homebrew and Nix packaging
- tagged release archives, checksums, and release manifests
- multi-agent stress coverage that simulates parallel worktree submission and publish coalescing
- a real-repo certification matrix runner for disposable mirrors of local sibling repos

## The Stress Result

The repo now ships a first-class stress target:

```bash
make test-stress
```

That test creates ten local worktrees, submits them in parallel while the
daemon drains the queue, introduces a real conflict pair, and verifies the
final integration and publish invariants.

One representative run produced:

- 10 local agent worktrees
- 9 succeeded submissions
- 1 blocked conflict
- 9 publish requests
- 8 superseded publishes
- 1 final successful publish
- clean protected `main`
- remote head equal to local protected head

That is the behavior the tool exists to guarantee.

For repeated evidence instead of a single green run, use the soak runner:

```bash
make soak
```

That reruns the stress workload many times, stores per-run logs and JSON
reports under `artifacts/soak/`, and writes an aggregate `summary.json` with
pass count, fail count, flake rate, duration, and queue-depth metrics.

For seeded race and transient-failure replay instead of only deterministic soak:

```bash
make soak-randomized
```

That runs the randomized stress harness, records the seed used for each run,
and persists enough metadata to replay the same failure class later.

For disposable real-repo certification against the committed local matrix:

```bash
make certify-matrix
```

That exercises `mq` against disposable mirrors of the configured real repos,
using the local `./bin/mq` by default, records the required repo-specific
policy defaults, and writes a machine-readable report under
`docs/certification/`.

To answer "does this checkout currently meet the promotion bar?":

```bash
mq confidence --repo /path/to/main
```

That combines live queue health, queue-derived rates and latencies, the latest
soak summary, and the latest certification report into one explicit gate
report. Evidence only counts if it was produced for the current `mainline`
commit.

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

Stable install details for Homebrew and Nix are in
[install.md](/Users/devrel/Projects/recallnet/mainline/docs/install.md).

## Commands

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

- standard repos with `.git/` in the checked-out worktree
- bare-clone storage with linked worktrees, such as
  `~/Projects/.bare/owner/repo.git` plus `~/Projects/owner/repo`

For bare-clone layouts, queue state and locks live with shared Git storage so
every worktree sees the same truth.

## When To Use It

Use `mainline` when:

- your repo already uses worktrees
- `main` must stay clean and pushable
- several humans or agents share one machine
- branch landing order matters
- you want publish to be deterministic and observable

Do not use it if your workflow is one person, one branch, one push, and no
parallelism. Plain Git is already excellent there.

## Development

```bash
make fmt
make test
make test-stress
make build
make install-hooks
```

When working directly in this repo, install the repo-managed hooks. They mirror
CI locally:

- `pre-commit` runs staged-format checks, `go vet`, `go test`, invariants, workflow lint when needed, and release regression checks when release paths change
- `pre-push` blocks dirty pushes, blocks stale `origin/main`, requires pushes to `origin/main` to come from local branch `main`, and reruns the full suite before remote mutation

More detail is in:

- [ARCHITECTURE.md](/Users/devrel/Projects/recallnet/mainline/docs/ARCHITECTURE.md)
- [FLOWS.md](/Users/devrel/Projects/recallnet/mainline/docs/FLOWS.md)
- [install.md](/Users/devrel/Projects/recallnet/mainline/docs/install.md)
