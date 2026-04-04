# mainline

Git is very good at branching.

Git is not a coordinator.

That distinction does not matter when one person is working in one checkout.
It matters a lot when one machine is running five agents, ten worktrees, and a
protected local `main` that still needs to stay clean and pushable all day.

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
Do not run environment-mutating helpers like `npm skills` from the protected
root checkout; run them in the topic worktree you are changing so local
lockfile drift does not block publish.

## The Daily Shape

The short CLI is `mq`, because that is what it is: the main queue for one
machine.

The agent path should feel like this:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
mq submit --wait --timeout 15m --json
```

Important: `mq submit --wait` stops at `integrated`. In a repo with
`[publish].Mode = 'manual'`, that means the commit is on local protected
`main` but not yet pushed to remote.

If the wrapper wants one blocking submit call that waits through auto-publish:

```bash
mq submit --wait --for landed --timeout 30m --json
```

Or, when the caller wants a durable machine handle instead of a one-shot wait:

```bash
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
```

That is the primary follow path. Use the returned `submission_id` and wait on it.
Treat `mq logs` and `mq events` as audit/debug surfaces, not the normal way to
decide whether your land finished.

If the wrapper expects remote landing as part of the same job, prefer:

```bash
mq land --json --timeout 30m
```

For agent-heavy and factory-style repos, set `[publish].Mode = 'auto'` in
`mainline.toml` unless there is a real operator reason to keep publish manual.

If you want to prove that some other process, not `submit`, handled a specific change:

```bash
mq submit --queue-only --json
mq wait --submission 42 --for landed --json --timeout 30m
```

The controller path should feel like this:

```bash
mq land --json --timeout 30m
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
humans, or all of them at once. `mainline` does not ask those actors to become
perfectly disciplined. It turns the safe path into the normal path.

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
- optional manual `mainlined` helper mode for experiments or multi-repo hosting
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
- JSON contract tests for `status`, raw `events`, lifecycle `events`, `watch`, and host logs
- bare-repo plus linked-worktree runs

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
```

`mq` is the default product path. `mainlined` is optional.

Homebrew and Nix details are in
[install.md](/Users/devrel/Projects/recallnet/mainline/docs/install.md).

## Developing vs Using

There are two valid ways to be here:

- developing the `mainline` codebase itself
- using `mainline` to manage some other repo

Those are not the same thing.

In this source checkout:

- committed docs and policy files live here
- `mq repo root --repo . --json` tells you whether this checkout is the
  canonical protected root checkout
- a fresh clone is not automatically an initialized managed repo just because
  `mainline.toml` exists in Git
- queue/state commands like `mq status --repo . --json` can still fail until
  this checkout has been explicitly initialized on this machine

If you are onboarding as a contributor to `mainline` itself, start with:

```bash
make build
go test ./...
./bin/mq --help
./bin/mq repo show --repo . --json
./bin/mq repo root --repo . --json
```

The contributor-specific flow is also summarized in
[CONTRIBUTING.md](/Users/devrel/Projects/recallnet/mainline/CONTRIBUTING.md)
and encoded for agents in
[.agents/skills/onboarding/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/onboarding/SKILL.md).
Use the onboarding skill before the worktree skill. Onboarding answers "what is
this repo, what works in this checkout, and what should I run first?" The
worktree skill answers "I already understand the setup, now how do I
contribute safely through `mq`?"

Recommended first-time repo setup after install:

```bash
cd /path/to/repo-root
mq repo init --repo .
git add mainline.toml
git commit -m "Initialize mainline repo policy"
./scripts/install-hooks.sh
```

That init commit matters. It turns the repo’s queue policy into versioned,
reviewable state instead of one more local convention that agents have to infer.
For normal repos, the root checkout should be the canonical protected `main`.
Keep it clean and boring. Humans inspect that path, and the machine wrapper
builds `mq` from it. If it is dirty, the wrapper should refuse
to build rather than silently drift.
Run package-manager helpers and lockfile generators in topic worktrees, not in
the protected root checkout.
Use `mq repo root --repo . --json` to verify that the root checkout is still
trustworthy. Use `mq repo root --repo . --adopt-root` only when the root
checkout is already clean and on the protected branch.
Repo-local AstroChicken scaffolding under `.astrochicken/` is treated as
tool-owned authoring surface, not protected-branch drift, so it does not by
itself make the checkout untrustworthy.

## The Core Commands

Setup:

```bash
mq repo init --repo /path/to/repo-root
mq repo root --repo /path/to/repo-root --json
mq repo audit --repo /path/to/repo-root --json
mq config edit --repo /path/to/repo-root
mq doctor --repo /path/to/repo-root --fix --json
```

If protected `main` is dirty, `mq doctor` is the takeover command: it tells you the queue is blocked, shows the dirty paths, and tells you to save, clean, or resolve the protected root checkout before retrying.

Submit and land:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
mq submit --allow-newer-head --wait --timeout 15m --json
mq submit --wait --timeout 15m --json
mq submit --wait --for landed --timeout 30m --json
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
mq land --json --timeout 30m
```

Operate and observe:

```bash
mq status --repo /path/to/repo-root --json
mq repo audit --repo /path/to/repo-root --json
mq watch --repo /path/to/repo-root
mq events --repo /path/to/repo-root --follow --json --lifecycle
mq registry prune --json
mq retry --repo /path/to/repo-root --submission 17
mq cancel --repo /path/to/repo-root --publish 4
```

The default model is queue-first commands that try to become the drainer
themselves and stay alive until the repo is quiescent, including sleeping
through scheduled publish retries. `mainlined` still exists as an optional
manual helper mode, but it is not part of the default machine setup.

## Repository Layouts

`mainline` supports both:

- normal repos with `.git/` inside the checked-out worktree
- bare-clone storage with linked worktrees, such as
  `~/Projects/.bare/owner/repo.git` with `~/Projects/owner/repo`

For bare-clone layouts, queue state and locks live with shared Git storage so
every worktree sees the same queue truth.
The bare storage path is not a human-facing checkout, so `root_checkout.exists
= false` is expected there. Point `mq repo init` at a clean local checkout on
the protected branch, not at the bare storage directory itself.

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
