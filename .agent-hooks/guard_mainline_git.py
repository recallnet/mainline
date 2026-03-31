#!/usr/bin/env python3

import json
import os
import pathlib
import shlex
import subprocess
import sys


SCRIPT_PATH = pathlib.Path(__file__).resolve()
REPO_ROOT = SCRIPT_PATH.parent.parent.resolve()
PROTECTED_BRANCH = "main"

BLOCKED_SUBCOMMANDS = {
    "am",
    "checkout",
    "cherry-pick",
    "commit",
    "merge",
    "pull",
    "push",
    "rebase",
    "reset",
    "restore",
    "revert",
    "switch",
}


def read_event():
    try:
        return json.load(sys.stdin)
    except json.JSONDecodeError:
        return {}


def run_git(cwd, *args):
    result = subprocess.run(
        ["git", *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        check=False,
    )
    return result.returncode, result.stdout.strip(), result.stderr.strip()


def same_repo(cwd):
    code, stdout, _ = run_git(cwd, "rev-parse", "--show-toplevel")
    if code != 0 or not stdout:
        return False
    try:
        return pathlib.Path(stdout).resolve() == REPO_ROOT
    except FileNotFoundError:
        return False


def current_branch(cwd):
    code, stdout, _ = run_git(cwd, "branch", "--show-current")
    if code != 0:
        return ""
    return stdout


def main():
    event = read_event()
    if event.get("tool_name") != "Bash":
        return 0

    command = str(event.get("tool_input", {}).get("command", "")).strip()
    if not command:
        return 0

    try:
        tokens = shlex.split(command)
    except ValueError:
        return 0

    if not tokens or tokens[0] != "git":
        return 0
    if len(tokens) < 2:
        return 0

    cwd = pathlib.Path(event.get("cwd") or os.getcwd()).resolve()
    if not same_repo(str(cwd)):
        return 0
    if current_branch(str(cwd)) != PROTECTED_BRANCH:
        return 0

    subcommand = tokens[1]
    if subcommand not in BLOCKED_SUBCOMMANDS:
        return 0

    sys.stderr.write(
        "Blocked native git mutation on the protected main worktree for this repo.\n"
        "Create or move to a feature worktree under ~/Projects/_wt/recallnet/mainline/, "
        "make commits there, and land through `mq submit`, `mq run-once`, and `mq publish`.\n"
        "Allowed on main: read-only inspection and worktree creation. "
        "Blocked here: git commit/merge/rebase/push/pull/reset/switch/checkout and similar branch-mutating commands.\n"
    )
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
