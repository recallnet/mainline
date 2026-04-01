# Flows

These are the intended operator flows for `mainline` today.

## Solo Developer

Initialize once:

```bash
mq repo init --repo .
git add mainline.toml && git commit -m "Initialize mainline repo policy"
./scripts/install-hooks.sh
mq config edit --repo .
mq doctor --repo .
```

Use a topic worktree, then land it:

```bash
git worktree add ../feature-login -b feature/login main
cd ../feature-login
# edit, test, commit
mq submit
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
mq config edit --repo /path/to/main
mq doctor --repo /path/to/main
mq status --repo /path/to/main
```

Submit from any linked worktree in the same repo:

```bash
cd /path/to/topic-worktree
mq submit
```

## Agent-Heavy Repo

Run the daemon in the protected worktree and let agents only submit:

```bash
mainlined --repo /path/to/main --interval 2s --json
cd /path/to/agent-worktree
mq submit --check-only --json
mq submit --wait --timeout 15m --json
mq events --repo /path/to/main --follow --json --lifecycle
mq confidence --repo /path/to/main
mq watch --repo /path/to/main
```

This is the intended dogfooding direction for the repo-local worktree skill: agents do all edits and commits in topic worktrees, then land through `mq` instead of manually merging into `main`.

## Repo-Local Skill

This repo ships the canonical agent instructions in [.agents/skills/worktree/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/worktree/SKILL.md).

That skill is now expected to use the real end-to-end flow:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
mq submit --wait --timeout 15m --json
mq status --repo /path/to/main --json
mq run-once --repo /path/to/main
mq publish --repo /path/to/main
mq watch --repo /path/to/main
mq events --repo /path/to/main --follow --json --lifecycle
```

For agent wrappers that only need to know whether their branch landed cleanly, prefer:

```bash
cd /path/to/topic-worktree
mq submit --wait --timeout 10m --json
```

Exit codes:

- `0`: integrated
- `1`: blocked, failed, or cancelled
- `2`: timed out waiting for integration

For cheap preflight before expensive local gates, use:

```bash
cd /path/to/topic-worktree
mq submit --check-only --json
```

That dry run verifies the branch is clean, includes the current protected tip, and is not already active in the queue at the same branch SHA.

If the repo has `[integration].MaxQueueDepth` set, `mq submit` will fail once the queued integration depth hits that limit. `--check-only` still works for dry-run validation in that state, which lets agents fail fast against a dead queue without silently growing it.

If a queue item needs operator intervention:

```bash
mq cancel --repo /path/to/main --submission 17
mq retry --repo /path/to/main --submission 17
mq cancel --repo /path/to/main --publish 4
mq retry --repo /path/to/main --publish 4
```
