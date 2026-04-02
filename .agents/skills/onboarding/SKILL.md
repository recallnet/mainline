---
name: onboarding
description: First-pass orientation for contributors developing the mainline repo itself versus operators using mq on a target repo. Verifies the local baseline, explains canonical-root trust, and clarifies which commands should or should not work in this source checkout.
---

# mainline onboarding

Use this skill when someone is new to this repository and needs a reliable first
pass through setup, baseline checks, and terminology.

## Goal

Separate these two jobs immediately:

- developing the `mainline` codebase in this repo
- using `mainline` to manage some other target repo

Do not let the user conflate them.

## Read first

Read these before making claims:

1. [README.md](/Users/devrel/Projects/recallnet/mainline/README.md)
2. [docs/install.md](/Users/devrel/Projects/recallnet/mainline/docs/install.md)
3. [docs/FLOWS.md](/Users/devrel/Projects/recallnet/mainline/docs/FLOWS.md)
4. [SPEC.md](/Users/devrel/Projects/recallnet/mainline/SPEC.md)
5. [PLAN.md](/Users/devrel/Projects/recallnet/mainline/PLAN.md)
6. [AGENTS.md](/Users/devrel/Projects/recallnet/mainline/AGENTS.md)
7. [CONTRIBUTING.md](/Users/devrel/Projects/recallnet/mainline/CONTRIBUTING.md)

## Contributor baseline

From the repo root, run:

```bash
make build
go test ./...
./bin/mq --help
./bin/mq repo show --repo . --json
./bin/mq repo root --repo . --json
./bin/mq status --repo . --json
```

Interpretation:

- `make build` should succeed
- `go test ./...` should succeed; test helpers intentionally neutralize global
  Git signing config for temporary repos
- `mq repo root --repo . --json` is the source of truth for whether the current
  checkout is the canonical protected root checkout
- `mq status --repo . --json` may fail in a fresh source clone until this repo
  has been explicitly initialized as a managed queue target on this machine

## Canonical-root explanation

Explain this concretely:

- the repo root checkout on `main` is the canonical protected root checkout
- topic worktrees are where feature work happens
- if the current checkout is not canonical, contributors should say so plainly
- if `mq repo root --repo . --json` says `trustworthy: false`, explain the
  recommended action rather than pretending the checkout is fine

## Output expectations

At the end of onboarding, summarize:

- whether the user is developing `mainline` or using it on another repo
- whether the current checkout is canonical and trustworthy
- whether the local build/test baseline succeeded
- which commands are expected to work in this checkout right now
- the next step for a contributor versus the next step for an operator
