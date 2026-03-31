# mainline Agent Rules

These rules are repo-specific and apply on top of the machine-wide agent
instructions.

## Protected Main Worktree

- Treat `/Users/devrel/Projects/recallnet/mainline` as the protected `main`
  worktree.
- Do not use native mutating `git` commands from that worktree while on branch
  `main`.
- Blocked examples: `git commit`, `git merge`, `git rebase`, `git push`,
  `git pull`, `git reset`, `git switch`, `git checkout`, `git cherry-pick`.
- Allowed examples: `git status`, `git diff`, `git log`, `git show`,
  `git fetch`, `git worktree add`.

## Required Flow

- Create a feature worktree under `~/Projects/_wt/recallnet/mainline/`.
- Make all code changes, tests, and commits in that feature worktree.
- Land work through `mq submit`, `mq run-once`, and `mq publish` instead of
  native merge/push on `main`.

## Intent

This repo dogfoods `mq`. Agents should not silently bypass the workflow the
project is built to enforce.
