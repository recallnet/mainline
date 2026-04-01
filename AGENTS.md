# mainline Agent Rules

These rules are repo-specific and apply on top of the machine-wide agent
instructions.

## Protected Main Worktrees

- Treat `/Users/devrel/Projects/recallnet/mainline` as the canonical protected
  `main` checkout for this repo.
- The root checkout must stay clean and on branch `main`. Humans inspect it,
  wrappers build from it, and docs refer to it.
- Do not run local environment-mutating helpers like `npm skills` from that
  canonical protected root checkout. Run them in the topic worktree you are
  changing so generated lockfiles and cache drift do not block publish.
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
  - treat `submit --wait` as `integrated`, not remote-published
  - use `mq submit --wait --for landed --timeout 30m --json` when one blocking submit call must include auto-publish too
  - capture `submission_id` from JSON when a wrapper needs durable tracking
  - use `mq submit --queue-only --json` only when the point is to let some other process, not submit, own the drain
  - `mq wait --submission <id> --for landed --json --timeout 30m` when the
    wrapper needs integrate-plus-publish confirmation by id
  - or `mq land --json --timeout 30m` when remote landing is the actual end of the job
  - `mq repo audit --repo /Users/devrel/Projects/recallnet/mainline --json`
- Controllers and factory-style callers should prefer:
  - `mq land --json --timeout 30m`
  - `mainlined --all --json` only as an explicit manual experiment, not as the default machine setup
- If `mq repo show` or `mq doctor` warns that the root checkout is dirty or not
  canonical, fix that before trusting local binaries or local docs.
- Use `mq repo root --repo /Users/devrel/Projects/recallnet/mainline --json`
  as the explicit source of truth for whether the canonical root checkout is
  trustworthy.
- In bare-repository-plus-worktree layouts, do not expect a human-facing root
  checkout at the bare repo path. Trust the configured canonical protected
  worktree instead.
- Use `mq repo root --repo /Users/devrel/Projects/recallnet/mainline --adopt-root`
  only after the root checkout is already clean and on branch `main`.
- Plain `mq submit` now opportunistically tries to drain after queueing. If the
  integration lock is already held, it exits cleanly and the active worker keeps
  draining.
- Use `mq events --follow --json --lifecycle` from the protected worktree when a
  long-running agent or controller needs push/integration notifications.
- Use `mq registry prune --json` if stale temp repos or deleted repos are
  polluting the optional global registry.
- Treat `submission_id`, not branch name, as the stable factory handle for a
  queued change.
- If an agent or controller parses machine-readable output, bind only to the
  documented contracts in
  [docs/JSON_CONTRACTS.md](/Users/devrel/Projects/recallnet/mainline/docs/JSON_CONTRACTS.md).
- If a branch is claimed to be landed, verify it with
  `mq repo audit --repo /Users/devrel/Projects/recallnet/mainline --json`.
  An empty `unmerged` list is the source of truth.
- For agent-heavy repos, prefer `[publish].Mode = 'auto'` so `submit --wait`
  does not leave integrated-but-unpublished work sitting on local `main`.

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
- Treat [PLAN.md](/Users/devrel/Projects/recallnet/mainline/PLAN.md) as the
  forward product plan and [SPEC.md](/Users/devrel/Projects/recallnet/mainline/SPEC.md)
  as the current product contract. Update them when the supported operator
  model, product boundary, or public integration surface changes.
- Keep docs concrete. Do not leave milestone sludge, vague marketing, or
  undocumented machine-readable output changes behind.
- Follow `remark-ai` style governance: product claims must match shipped
  behavior, and machine-readable contracts must stay explicit and versioned.
