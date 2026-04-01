---
name: worktree
description: Work in a dedicated Git worktree, make all commits there, and land changes through `mq` instead of merging directly into `main`. Use when the user asks how to use worktrees for this repo, how agents should branch and commit, or how to dogfood `mq`.
---

# Worktree

Use this skill when working on `mainline` from a feature worktree and landing
that work back through `mq`.

The goal is simple:

- `main` stays clean
- the repo root checkout stays boring and trustworthy
- each task gets its own worktree
- all commits happen in the feature worktree
- landing happens through `mq`, not manual merge into `main`

Default commands to optimize for:

- `mq submit --check-only --json`
- `mq submit --queue-only --json`
- `mq submit --wait --timeout 15m --json`
- `mq wait --submission <id> --for landed --json --timeout 30m`
- `mq land --json --timeout 30m`
- `mainlined --all --json`
- `mq events --follow --json --lifecycle`

If you want the machine-global daemon on macOS, install and verify the exact
launch-agent label:

- install: `./scripts/install-launch-agent.sh`
- verify: `launchctl print gui/$(id -u)/com.recallnet.mainline.global`
- if macOS says `Could not find service "com.recallnet.mainline.global"`, the
  daemon is not installed yet
- when proving the daemon path is nominal, inspect:
  - `tail -n 50 ~/Library/Logs/mainline/mainlined.out.log`
  - `tail -n 50 ~/Library/Logs/mainline/mainlined.err.log`
  - `mq repo root --repo ~/Projects/recallnet/mainline --json`
  - `mq doctor --repo ~/Projects/recallnet/mainline --json`

Do not use this skill to bypass `mq`. It exists to dogfood the workflow that
`mainline` is building for other repos.

Treat [PLAN.md](/Users/devrel/Projects/recallnet/mainline/PLAN.md) as the
forward product plan and [SPEC.md](/Users/devrel/Projects/recallnet/mainline/SPEC.md)
as the current product contract. If a change alters the supported operator
model or product boundary, those docs need to move with the code.

Supported agent clients may also enforce this with hooks. If a hook blocks a
native `git` mutation on the protected `main` worktree, that is not a false
positive to work around. Move to a feature worktree and continue there.

## Operating rules

1. Never do feature work on the protected branch worktree.
2. Never commit unfinished feature work onto `main`.
3. Never merge a feature branch into `main` manually when `mq` can perform the
   integration path being dogfooded.
4. Never push directly to `origin/main` from a feature worktree as part of the
   normal flow.
5. Make all code changes, tests, and commits from the feature worktree tied to
   the branch being submitted.
6. Prefer `mq submit` from the current worktree instead of manually spelling
   `--repo` when the current shell is already in the topic worktree.
7. Use explicit protected-worktree paths when invoking operator commands like
   `mq run-once`, `mq publish`, `mq land`, `mq watch`, or `mainlined`.

## Standard flow

### 1. Start from a clean protected branch worktree

Use the repo root checkout as the canonical protected branch worktree.

Example:

```bash
cd ~/Projects/recallnet/mainline
git status --short
git branch --show-current
```

Expected:

- worktree is clean
- branch is `main`
- this root checkout is the one humans inspect and wrappers build from

Before trusting that assumption on a machine you did not set up yourself:

```bash
mq repo root --repo ~/Projects/recallnet/mainline --json
```

If the root checkout is already clean and on `main` but config drift points the
canonical main worktree somewhere else, repair the configuration instead of
trying to outsmart the wrapper:

```bash
mq repo root --repo ~/Projects/recallnet/mainline --adopt-root
```

### 2. Create a dedicated feature worktree

Prefer the machine worktree layout convention:

```bash
mkdir -p ~/Projects/_wt/recallnet/mainline
git worktree add ~/Projects/_wt/recallnet/mainline/<branch-name> -b <branch-name> main
```

Example:

```bash
git worktree add ~/Projects/_wt/recallnet/mainline/m4-run-once -b m4-run-once main
```

### 3. Do all work inside the feature worktree

```bash
cd ~/Projects/_wt/recallnet/mainline/m4-run-once
git status --short
git branch --show-current
```

All of these should now happen in the feature worktree:

- editing files
- running tests
- making commits
- amending or stacking follow-up commits

Do not hop back to the protected branch worktree to finish the task.

### 4. Commit only from the feature worktree

Example:

```bash
git add <files>
git commit -m "Implement run-once integration worker"
```

If review finds a defect, make the fix as another commit on the same feature
branch unless there is an explicit reason to rewrite history.

### 5. Submit through `mq`

Most agents should finish from the feature worktree with:

```bash
mq submit --check-only --json
mq submit --wait --timeout 15m --json
```

Treat `mq submit --wait` as an integration answer, not a remote-publish answer.
If the repo keeps `[publish].Mode = 'manual'`, use `mq land` or
`mq wait --for landed` when the job is not done until remote `main` moves.
Use `mq submit --queue-only --json` when the point is to prove the daemon
handled the queued submission instead of opportunistic submit-side drain.

That gives the agent:

- a deterministic dry-run before expensive follow-up work
- a blocking integrated-or-blocked answer without inventing a poll loop
- stable JSON for wrappers and daemon orchestration

If the wrapper needs a durable handle instead of waiting inline:

```bash
mq submit --json
mq wait --submission <id> --for landed --json --timeout 30m
```

Use that pattern when the caller wants to track a specific queued submission by
id instead of polling by branch name.

If the branch is ready to hand off asynchronously instead:

```bash
mq submit
```

That now queues first and then opportunistically tries to drain. If another
worker already holds the integration lock, the submit still succeeds and the
active drainer owns the rest.

If a controller owns the full integrate-plus-publish path:

```bash
mq land --json --timeout 30m
```

If needed, be explicit:

```bash
mq submit \
  --repo ~/Projects/recallnet/mainline \
  --branch <branch-name> \
  --worktree ~/Projects/_wt/recallnet/mainline/<branch-name>
```

Submission expectations:

- the worktree must be clean
- the branch must not be `main`
- the worktree must belong to the same repo
- the submitted branch head is recorded in durable queue state

### 6. Let `mq` land the change

Use `mq` instead of manual merge:

```bash
mq run-once --repo ~/Projects/recallnet/mainline
```

When publish behavior is part of the dogfood target:

```bash
mq publish --repo ~/Projects/recallnet/mainline
```

For machine-wide steady state, prefer one global daemon instead of one daemon
per repo:

```bash
mainlined --all --json
```

Recommended verification loop:

```bash
mq status --repo ~/Projects/recallnet/mainline --json
mq repo audit --repo ~/Projects/recallnet/mainline --json
mq events --repo ~/Projects/recallnet/mainline --follow --json --lifecycle
```

Expected:

- submission is visible in durable queue state
- integration result is visible after `mq run-once`
- publish result is visible after `mq publish` or daemon-driven publish
- `status --json` can correlate a succeeded submission to `publish_request_id`,
  `publish_status`, and `outcome`
- `unmerged` is empty once the branch is truly reachable from protected `main`
- `mq repo show` and `mq doctor` do not warn that the repo root checkout is
  dirty or non-canonical

## Review and fix loop

If code review finds an issue after submission-ready work:

1. Return to the same feature worktree.
2. Make the fix there.
3. Commit the fix on the same branch.
4. Re-submit if the queued SHA must be refreshed.

Do not patch `main` directly to fix a feature-branch defect.

## Cleanup

After the branch is integrated and no longer needed:

```bash
git worktree remove ~/Projects/_wt/recallnet/mainline/<branch-name>
git branch -d <branch-name>
```

Only delete the branch after the integration result is confirmed.

## What to avoid

- `git checkout main` in the feature worktree and continuing work there
- `git merge <branch>` from the protected branch worktree as the default path
- `git push origin main` as a substitute for `mq`
- reusing one worktree for multiple unrelated branches
- committing from the wrong worktree because the shell is pointed at the wrong directory

## Current project note

This skill is the committed self-hosting path for `mainline`.

If the shipped command surface changes, update this skill alongside the README,
flow docs, and plan so agents are never instructed to follow stale workflow
steps.
