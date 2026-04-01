# mainline Agent Rules

These rules are repo-specific and apply on top of the machine-wide agent
instructions.

## Protected Main Worktrees

- Treat `/Users/devrel/Projects/_wt/recallnet/mainline/protected-main` as the
  canonical protected `main` worktree for this repo.
- Treat `/Users/devrel/Projects/recallnet/mainline` as protected too unless you
  have explicitly confirmed it is a disposable feature checkout. Do not assume
  the root checkout is safe for feature commits.
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
  - capture `submission_id` from JSON when a wrapper needs durable tracking
  - `mq wait --submission <id> --for landed --json --timeout 30m` when the
    wrapper needs integrate-plus-publish confirmation by id
  - `mq repo audit --repo /Users/devrel/Projects/_wt/recallnet/mainline/protected-main --json`
- Controllers and factory-style daemons should prefer:
  - `mq land --json --timeout 30m`
  - or a long-lived `mainlined --repo /Users/devrel/Projects/_wt/recallnet/mainline/protected-main --json`
- Use `mq events --follow --json --lifecycle` from the protected worktree when a
  long-running agent or daemon needs push/integration notifications.
- Treat `submission_id`, not branch name, as the stable factory handle for a
  queued change.
- If an agent or daemon parses machine-readable output, bind only to the
  documented contracts in
  [docs/JSON_CONTRACTS.md](/Users/devrel/Projects/recallnet/mainline/docs/JSON_CONTRACTS.md).
- If a branch is claimed to be landed, verify it with
  `mq repo audit --repo /Users/devrel/Projects/_wt/recallnet/mainline/protected-main --json`.
  An empty `unmerged` list is the source of truth.

## Intent

This repo dogfoods `mq`. Agents should not silently bypass the workflow the
project is built to enforce.

## Repo Context

- Use `codecontext` before broad repo changes so you read the current local
  implementation and docs instead of relying on stale assumptions.
- Prefer repo-aware discovery first, then change code and docs together.

## Docs Governance

- Treat docs, skills, hooks, and JSON contract docs as governed product
  surface, not optional commentary.
- If you change command behavior, update the README, flow docs, skills, and
  [docs/JSON_CONTRACTS.md](/Users/devrel/Projects/recallnet/mainline/docs/JSON_CONTRACTS.md)
  in the same slice.
- Keep docs concrete. Do not leave milestone sludge, vague marketing, or
  undocumented machine-readable output changes behind.
- Follow `remark-ai` style governance: product claims must match shipped
  behavior, and machine-readable contracts must stay explicit and versioned.
