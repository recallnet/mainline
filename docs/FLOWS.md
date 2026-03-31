# Flows

These are the intended operator flows for `mainline` today.

## Solo Developer

Initialize once:

```bash
mq repo init --repo .
mq doctor --repo .
```

Use a topic worktree, then land it:

```bash
git worktree add ../feature-login -b feature/login
cd ../feature-login
# edit, test, commit
mq submit --repo .
mq status --repo . --json
mq run-once --repo /path/to/protected-worktree
mq publish --repo /path/to/protected-worktree
```

## Worktree-Heavy Repo

Keep one canonical protected-branch worktree and many topic worktrees:

```bash
mq repo init --repo /path/to/main --main-worktree /path/to/main
mq doctor --repo /path/to/main
mq status --repo /path/to/main
```

Submit from any linked worktree in the same repo:

```bash
mq submit --repo /path/to/topic-worktree
```

## Agent-Heavy Repo

Run the daemon in the protected worktree and let agents only submit:

```bash
mainlined --repo /path/to/main --interval 2s --json
mq submit --repo /path/to/agent-worktree
mq status --repo /path/to/main --json
```

This is the intended dogfooding direction for the repo-local worktree skill: agents do all edits and commits in topic worktrees, then land through `mq` instead of manually merging into `main`.

## Repo-Local Skill

This repo ships the canonical agent instructions in [.agents/skills/worktree/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/worktree/SKILL.md).

That skill is now expected to use the real end-to-end flow:

```bash
mq submit --repo /path/to/topic-worktree
mq status --repo /path/to/main --json
mq run-once --repo /path/to/main
mq publish --repo /path/to/main
```
