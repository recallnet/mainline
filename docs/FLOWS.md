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

Use a topic worktree, then land it:

```bash
git worktree add ../feature-login -b feature/login main
cd ../feature-login
# edit, test, commit
mq submit
mq repo audit --repo /path/to/protected-worktree --json
mq status --repo . --json
mq run-once --repo /path/to/protected-worktree
mq publish --repo /path/to/protected-worktree
mq watch --repo /path/to/protected-worktree
mq logs --repo /path/to/protected-worktree --follow
mq events --repo /path/to/protected-worktree --follow
```

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
and back on the protected branch.

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
mq submit --wait --timeout 15m --json
mq repo audit --repo /path/to/main --json
mq events --repo /path/to/main --follow --json --lifecycle
mq confidence --repo /path/to/main
mq watch --repo /path/to/main
```

For unattended agent repos, set `[publish].Mode = 'auto'`. Otherwise
`mq submit --wait` only proves `integrated`, not that remote `main` has been
updated.

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
mq submit --wait --timeout 15m --json
mq repo audit --repo /path/to/main --json
mq status --repo /path/to/main --json
mq run-once --repo /path/to/main
mq publish --repo /path/to/main
mq watch --repo /path/to/main
mq events --repo /path/to/main --follow --json --lifecycle
```

For wrappers and factories, prefer a durable submission-id flow:

```bash
cd /path/to/topic-worktree
mq submit --json
mq wait --submission 42 --for landed --json --timeout 30m
```

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
mq cancel --repo /path/to/main --submission 17
mq retry --repo /path/to/main --submission 17
mq cancel --repo /path/to/main --publish 4
mq retry --repo /path/to/main --publish 4
```

`mq status --json` now projects publish correlation back onto succeeded
submissions through `publish_request_id`, `publish_status`, and `outcome`, so a
factory can answer “did this submission fully land?” from one status surface.

For multi-repo experiments, one registered-repo daemon is still available:

```bash
mainlined --all --json
```

If old deleted repos are still showing up in that optional global mode, clean
the registry with:

```bash
mq registry prune --json
```
