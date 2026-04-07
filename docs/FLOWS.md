# Flows

These are the intended operator flows for `mainline` today.

If an agent wrapper, factory daemon, or operator UI parses `--json` output,
bind only to the documented `v1` machine contract in
[JSON_CONTRACTS.md](/Users/devrel/Projects/recallnet/mainline/docs/JSON_CONTRACTS.md).
That document is the compatibility policy. The internal Go structs are not the
public contract.

## Solo Developer

Initialize once:

```bash
mq repo init --repo . 
git add mainline.toml && git commit -m "Initialize mainline repo policy"
./scripts/install-hooks.sh
mq repo root --repo . --json
mq repo audit --repo . --json
mq config edit --repo .
mq doctor --repo .
```

If protected `main` is dirty, start with `mq doctor --repo .`: it reports that the queue is blocked and tells you to take ownership of cleaning or resolving the protected root checkout before retrying.

`mainline.toml` is the runtime config authority for the repo. The SQLite state
store keeps queue identity, events, and publish/integration history.

Use a topic worktree, then land it:

```bash
git worktree add ../feature-login -b feature/login main
cd ../feature-login
# edit, test, commit
mq submit --wait --for landed --json --timeout 30m
mq repo audit --repo /path/to/protected-worktree --json
mq status --repo . --json
```

If the caller needs a durable handle instead of waiting inline:

```bash
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
```

Use `submission_id` plus `mq wait --submission ...` as the normal follow path.
Do not use sleeps, branch-name polling, `mq logs`, `mq events`, or `mq watch`
as the primary completion path.

## Worktree-Heavy Repo

Keep one canonical protected-branch worktree and many topic worktrees:

```bash
mq repo init --repo /path/to/main --main-worktree /path/to/main
mq repo root --repo /path/to/main --json
mq repo audit --repo /path/to/main --json
mq config edit --repo /path/to/main
mq doctor --repo /path/to/main
mq status --repo /path/to/main
```

For ordinary repos, `/path/to/main` should be the repo root checkout that
humans inspect and local wrappers build from. Keep that checkout clean and on
the protected branch. Topic worktrees are where feature edits belong.
If config drift ever points the canonical main worktree somewhere else, prefer
`mq repo root --repo /path/to/main --adopt-root` once `/path/to/main` is clean
and back on the protected branch. The goal is that humans do not need to know
some extra `protected-main/` path just to inspect or operate the repo.

Submit from any linked worktree in the same repo:

```bash
cd /path/to/topic-worktree
mq submit
```

## Agent-Heavy Repo

Use the daemonless default flow and let agents submit directly:

```bash
cd /path/to/agent-worktree
mq submit --check-only --json
mq submit --wait --for landed --timeout 30m --json
mq repo audit --repo /path/to/main --json
mq confidence --repo /path/to/main
mq status --repo /path/to/main --json
```

For unattended agent repos, set `[publish].Mode = 'auto'`. Otherwise
`mq submit --wait` only proves `integrated`, not that remote `main` has been
updated.

If a repo needs local environment repair or cache warmup after integration and
before push, configure that in `[checks].PreparePublish`. This is the right
place for commands like `pnpm install --frozen-lockfile` or targeted build-cache
warmup.

Put read-only publish-time verification commands in `[checks].ValidatePublish`.

Prepare commands run in the protected worktree immediately before push. They
may leave ignored caches behind, but they must not leave tracked or other
non-ignored drift; `mq` now fails the publish if prepare commands dirty
protected `main`. Legacy `[checks].PrePublish` still works for compatibility,
but new configs should use the explicit prepare/validate split.
When explicit publish checks are configured, `mq` bypasses inherited local
`pre-push` hooks for the final push. That keeps protected `main` from being
mutated by hook side effects and makes `mainline.toml` the single publish gate.
Use `mq status --json` when publish is slow or blocked; it now shows the
effective hook policy plus the active publish phase (`prepare`, `validate`, or
`push`).

If the repo uses `[publish].Mode = 'auto'` and the caller wants one blocking
submit call through publish:

```bash
mq submit --wait --for landed --timeout 30m --json
```

This is the intended dogfooding direction for the repo-local worktree skill: agents do all edits and commits in topic worktrees, then land through `mq` instead of manually merging into `main`.

## Repo-Local Skill

This repo ships the canonical agent instructions in [.agents/skills/worktree/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/worktree/SKILL.md).

That skill is now expected to use the real end-to-end flow:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
mq repo audit --repo /path/to/main --json
mq status --repo /path/to/main --json
```

For wrappers and factories, prefer a durable submission-id flow:

```bash
cd /path/to/topic-worktree
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
```

That is the default agent/controller subscription model: submit once, keep the
`submission_id`, and wait on that submission until it lands or fails.

If you want to prove that some other process handled a specific submission, queue
without opportunistic drain:

```bash
cd /path/to/topic-worktree
mq submit --queue-only --json
mq wait --submission 42 --for landed --json --timeout 30m
```

That avoids branch-name polling and gives one stable handle per queued change.
Plain `mq submit` also tries to drain immediately after queueing. If another
worker already holds the integration lock, it exits cleanly and the active
drainer keeps working.

For agent wrappers that only need to know whether their branch landed cleanly, prefer:

```bash
cd /path/to/topic-worktree
mq submit --wait --timeout 10m --json
```

That waits for `integrated`, not `landed`.

If the wrapper needs the full remote result, prefer:

```bash
cd /path/to/topic-worktree
mq land --json --timeout 30m
```

Or, if it wants an explicit submission-id flow:

```bash
cd /path/to/topic-worktree
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
```

Use `mq logs`, `mq events`, and `mq watch` only when you need audit/debug
detail beyond the submission-id flow.

If a factory keeps appending commits to the same queued branch and wants the
newest descendant tip instead of a hard failure on head drift, submit with:

```bash
cd /path/to/topic-worktree
mq submit --allow-newer-head --wait --timeout 10m --json
```

That only permits forward movement. If the queued branch rewinds or moves to a
non-descendant tip, `mq` still fails the submission and asks for a resubmit.

Exit codes:

- `0`: integrated
- `1`: blocked, failed, or cancelled
- `2`: timed out waiting for integration

Treat those exit codes as integration-only unless you waited for `landed`.

For cheap preflight before expensive local gates, use:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
```

That dry run verifies the branch is clean, includes the current protected tip, and is not already active in the queue at the same branch SHA.

For branch ancestry certainty instead of narration, use:

```bash
mq repo audit --repo /path/to/main --json
```

An empty `unmerged` list means every discovered local/worktree branch tip is already reachable from the protected branch.

If the repo has `[integration].MaxQueueDepth` set, `mq submit` will fail once the queued integration depth hits that limit. `--check-only` still works for dry-run validation in that state, which lets agents fail fast against a dead queue without silently growing it.

If a queue item needs operator intervention:

```bash
mq blocked --repo /path/to/main --json
mq rebase --repo /path/to/topic-worktree --submission 17 --json
mq retry --repo /path/to/main --all-safe --json
mq cancel --repo /path/to/main --blocked --json
mq cancel --repo /path/to/main --submission 17
mq retry --repo /path/to/main --submission 17
mq cancel --repo /path/to/main --publish 4
mq retry --repo /path/to/main --publish 4
```

Use `mq blocked` as the primary blocked-submission operator surface. It lists
blocked submissions, their recovery commands, and the exact `retry` / `cancel`
commands for each item.

Use `mq rebase` as the canonical repair path when a topic branch is behind the
local protected branch or a submission is blocked on rebase conflict. It finds
the right source worktree, syncs protected `main` first when needed, and rebases
onto local protected `main` instead of asking operators to guess whether they
should use `main` or `origin/main`.

`mq retry --all-safe` is intentionally conservative: it only retries blocked
submissions whose last blocked reason is currently considered safe to retry
without human branch surgery.

`mq cancel --blocked` bulk-cancels blocked submissions when they are obsolete
and you want to clear them out of the active queue surface.

`mq status --json` now projects publish correlation back onto succeeded
submissions through `publish_request_id`, `publish_status`, and `outcome`, so a
factory can answer “did this submission fully land?” from one status surface.
If a protected worktree was ever registered under multiple repo identities,
`mq` now consolidates those rows by protected worktree so status, wait, and
publish no longer split state across multiple repo ids.

Optional only: for multi-repo experiments, one registered-repo host is still available:

```bash
mainlined --all --json
```

Do not make that part of default repo onboarding. The intended default is still
daemonless `mq` commands plus `mq wait --submission <id> ...` for follow-up.

If old deleted repos are still showing up in that optional global mode, clean
the registry with:

```bash
mq registry prune --json
```
