---
name: worktree
description: Work in a dedicated Git worktree, make all commits there, and land changes through `mq` instead of merging directly into `main`. Use when the user asks how to use worktrees for this repo, how agents should branch and commit, or how to dogfood `mq`.
---

# Worktree

Use this skill when working on `mainline` from a feature worktree and landing
that work back through `mq`.

The goal is simple:

- `main` stays clean
- each task gets its own worktree
- all commits happen in the feature worktree
- landing happens through `mq`, not manual merge into `main`

Do not use this skill to bypass `mq`. It exists to dogfood the workflow that
`mainline` is building for other repos.

## Operating rules

1. Never do feature work on the protected branch worktree.
2. Never commit unfinished feature work onto `main`.
3. Never merge a feature branch into `main` manually when `mq` can perform the
   integration path being dogfooded.
4. Never push directly to `origin/main` from a feature worktree as part of the
   normal flow.
5. Make all code changes, tests, and commits from the feature worktree tied to
   the branch being submitted.
6. Use explicit branch and worktree paths when that removes ambiguity.

## Standard flow

### 1. Start from a clean protected branch worktree

Use the canonical repo worktree as the protected branch worktree.

Example:

```bash
cd ~/Projects/recallnet/mainline
git status --short
git branch --show-current
```

Expected:

- worktree is clean
- branch is `main`

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

When the branch is ready to land, submit it from the feature worktree:

```bash
mq submit --repo .
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

Recommended verification loop:

```bash
mq status --repo ~/Projects/recallnet/mainline --json
```

Expected:

- submission is visible in durable queue state
- integration result is visible after `mq run-once`
- publish result is visible after `mq publish` or daemon-driven publish

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
