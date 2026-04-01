# mainline Agent Rules

These rules are repo-specific and apply on top of the machine-wide agent
instructions.

## Protected Main Worktrees

- Treat `/Users/devrel/Projects/recallnet/mainline` and
  `/Users/devrel/Projects/_wt/recallnet/mainline/protected-main` as protected
  `main` worktrees.
- Do not use native mutating `git` commands from that worktree while on branch
  `main`.
- Blocked examples: `git commit`, `git merge`, `git rebase`, `git push`,
  `git pull`, `git reset`, `git switch`, `git checkout`, `git cherry-pick`.
- Allowed examples: `git status`, `git diff`, `git log`, `git show`,
  `git fetch`, `git worktree add`.

## Required Flow

- Create a feature worktree under `~/Projects/_wt/recallnet/mainline/`.
- Make all code changes, tests, and commits in that feature worktree.
- Most coding agents should finish with:
  - `mq submit --check-only --json`
  - `mq submit --wait --timeout 15m --json`
  - `mq repo audit --repo /Users/devrel/Projects/_wt/recallnet/mainline/protected-main --json`
- Controllers and factory-style daemons should prefer:
  - `mq land --json --timeout 30m`
  - or a long-lived `mainlined --repo /Users/devrel/Projects/_wt/recallnet/mainline/protected-main --json`
- Use `mq events --follow --json --lifecycle` from the protected worktree when a
  long-running agent or daemon needs push/integration notifications.
- If a branch is claimed to be landed, verify it with
  `mq repo audit --repo /Users/devrel/Projects/_wt/recallnet/mainline/protected-main --json`.
  An empty `unmerged` list is the source of truth.

## Intent

This repo dogfoods `mq`. Agents should not silently bypass the workflow the
project is built to enforce.
