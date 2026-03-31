#!/usr/bin/env python3
import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path


def run(cmd, cwd=None, env=None, capture_output=True):
    merged_env = os.environ.copy()
    if env:
        merged_env.update(env)
    return subprocess.run(
        cmd,
        cwd=cwd,
        env=merged_env,
        check=True,
        text=True,
        capture_output=capture_output,
    )


def format_process_error(err: subprocess.CalledProcessError):
    return {
        "command": err.cmd,
        "returncode": err.returncode,
        "stdout": err.stdout or "",
        "stderr": err.stderr or "",
    }


def git(path, *args, capture_output=True):
    result = run(["git", "-C", str(path), *args], capture_output=capture_output)
    if capture_output:
        return result.stdout.strip()
    return ""


def head_contains_path(repo_root: Path, rel_path: str) -> bool:
    try:
        run(["git", "-C", str(repo_root), "cat-file", "-e", f"HEAD:{rel_path}"])
        return True
    except subprocess.CalledProcessError:
        return False


def replace_config_value(path: Path, key: str, value: str):
    text = path.read_text()
    needle = f"{key} = 'inherit'"
    if needle in text:
        text = text.replace(needle, f"{key} = '{value}'")
    else:
        raise RuntimeError(f"expected config key {key} in {path}")
    path.write_text(text)


def config_path_for_repo(repo_root: Path) -> Path:
    common_git_dir = Path(git(repo_root, "rev-parse", "--git-common-dir"))
    if not common_git_dir.is_absolute():
        common_git_dir = (repo_root / common_git_dir).resolve()
    if common_git_dir.name == ".git":
        return common_git_dir.parent / "mainline.toml"
    return common_git_dir / "mainline.toml"


def prepare_regular_layout(origin_bare: Path, work_root: Path) -> Path:
    main = work_root / "main"
    run(["git", "clone", str(origin_bare), str(main)])
    return main


def prepare_bare_worktree_layout(origin_bare: Path, work_root: Path) -> Path:
    repo_bare = work_root / "repo.git"
    main = work_root / "main"
    run(["git", "clone", "--bare", str(origin_bare), str(repo_bare)])
    run(["git", f"--git-dir={repo_bare}", "worktree", "add", str(main), "main"])
    return main


def configure_clone(repo_root: Path, mq_bin: str, hook_policy: str):
    repo_root = repo_root.resolve()
    hooks_disabled = repo_root.parent / "hooks-disabled"
    hooks_disabled.mkdir(exist_ok=True)
    git(repo_root, "config", "user.name", "Certification Runner", capture_output=False)
    git(repo_root, "config", "user.email", "certification@recallnet.local", capture_output=False)
    git(repo_root, "config", "core.hooksPath", str(hooks_disabled), capture_output=False)

    run([mq_bin, "repo", "init", "--repo", str(repo_root), "--main-worktree", str(repo_root)])
    config_path = config_path_for_repo(repo_root)
    replace_config_value(config_path, "HookPolicy", hook_policy)
    rel_config = os.path.relpath(config_path, repo_root)
    if not rel_config.startswith(".."):
        git(repo_root, "add", "--force", "--", rel_config, capture_output=False)
        staged_diff = subprocess.run(
            ["git", "-C", str(repo_root), "diff", "--cached", "--quiet", "--", rel_config],
            text=True,
            capture_output=True,
        )
        if staged_diff.returncode not in (0, 1):
            raise RuntimeError(staged_diff.stderr.strip() or f"failed to inspect staged config for {repo_root}")
        if staged_diff.returncode == 1:
            run(
                ["git", "-c", "core.hooksPath=/dev/null", "commit", "-m", "configure mainline certification policy"],
                cwd=repo_root,
            )
        if not head_contains_path(repo_root, rel_config):
            raise RuntimeError(f"expected {rel_config} to be committed in HEAD for {repo_root}")

        status = git(repo_root, "status", "--short", "--untracked-files=all")
        if status:
            raise RuntimeError(f"protected worktree remained dirty after config setup: {repo_root}")


def create_feature_cycle(repo_root: Path, cycle_root: Path, repo_id: str, cycle_index: int):
    repo_root = repo_root.resolve()
    branch = f"cert/{repo_id}-{cycle_index}"
    worktree = cycle_root / f"feature-{cycle_index}"
    run(["git", "-C", str(repo_root), "worktree", "add", "-b", branch, str(worktree)])
    cert_dir = worktree / ".mainline-cert"
    cert_dir.mkdir(exist_ok=True)
    cert_file = cert_dir / f"cycle-{cycle_index}.txt"
    cert_file.write_text(f"{repo_id} cycle {cycle_index}\n")
    git(worktree, "add", str(cert_file.relative_to(worktree)), capture_output=False)
    run(
        ["git", "-c", "core.hooksPath=/dev/null", "commit", "-m", f"certification cycle {cycle_index}"],
        cwd=worktree,
    )
    return branch, worktree


def run_cycle(repo_root: Path, mq_bin: str, worktree: Path):
    repo_root = repo_root.resolve()
    run([mq_bin, "submit", "--repo", str(worktree)])
    integrate = run([mq_bin, "run-once", "--repo", str(repo_root)]).stdout.strip()
    publish = run([mq_bin, "publish", "--repo", str(repo_root)]).stdout.strip()
    publish_run = run([mq_bin, "run-once", "--repo", str(repo_root)]).stdout.strip()
    return {
        "submit_worktree": str(worktree),
        "integrate_result": integrate,
        "publish_result": publish,
        "publish_run_result": publish_run,
    }


def certify_entry(entry: dict, repo_root: Path, mq_bin: str, output_root: Path):
    common_git_dir = Path(git(repo_root, "rev-parse", "--git-common-dir"))
    if not common_git_dir.is_absolute():
        common_git_dir = (repo_root / common_git_dir).resolve()
    canonical_repo_root = common_git_dir.parent

    raw_source = Path(entry["source"])
    if raw_source.is_absolute():
        source = raw_source
    else:
        source = (canonical_repo_root / raw_source).resolve()
    work_root = output_root / entry["id"]
    work_root.mkdir(parents=True, exist_ok=True)
    origin_bare = work_root / "origin.git"
    run(["git", "clone", "--bare", str(source), str(origin_bare)])

    if entry["layout"] == "bare-worktree":
        clone_root = prepare_bare_worktree_layout(origin_bare, work_root)
    elif entry["layout"] == "regular":
        clone_root = prepare_regular_layout(origin_bare, work_root)
    else:
        raise RuntimeError(f"unknown layout {entry['layout']}")

    configure_clone(clone_root, mq_bin, entry["policy_defaults"]["hook_policy"])

    cycles = []
    for cycle_index in (1, 2):
        _, feature_worktree = create_feature_cycle(clone_root, work_root, entry["id"], cycle_index)
        cycles.append(run_cycle(clone_root, mq_bin, feature_worktree))

    status = json.loads(run([mq_bin, "status", "--repo", str(clone_root), "--json"]).stdout)
    doctor = run([mq_bin, "doctor", "--repo", str(clone_root), "--json"]).stdout
    remote_head = run(["git", "rev-parse", "refs/heads/main"], cwd=origin_bare).stdout.strip()
    local_head = git(clone_root, "rev-parse", "HEAD")

    return {
        "id": entry["id"],
        "source": entry["source"],
        "layout": entry["layout"],
        "classes": entry["classes"],
        "policy_defaults": entry["policy_defaults"],
        "findings": entry["findings"],
        "result": "passed" if local_head == remote_head and status["counts"]["queued_submissions"] == 0 and status["counts"]["queued_publishes"] == 0 else "failed",
        "cycles": cycles,
        "local_head": local_head,
        "remote_head": remote_head,
        "status": status,
        "doctor": json.loads(doctor),
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--matrix", default="docs/certification/matrix.json")
    parser.add_argument("--output", default="docs/certification/latest-report.json")
    parser.add_argument("--keep-workdirs", action="store_true")
    parser.add_argument("--keep-workdirs-dir", default=os.environ.get("CERT_KEEP_WORKDIRS_DIR", ""))
    parser.add_argument("--mq-bin", default=os.environ.get("MQ_BIN", "mq"))
    args = parser.parse_args()

    repo_root = Path(__file__).resolve().parent.parent
    mainline_commit = git(repo_root, "rev-parse", "HEAD")
    matrix_path = (repo_root / args.matrix).resolve()
    output_path = (repo_root / args.output).resolve()
    matrix = json.loads(matrix_path.read_text())

    with tempfile.TemporaryDirectory(prefix="mainline-cert-") as temp_dir:
        temp_root = Path(temp_dir)
        results = []
        for entry in matrix["repos"]:
            try:
                results.append(certify_entry(entry, repo_root, args.mq_bin, temp_root))
            except subprocess.CalledProcessError as err:
                results.append(
                    {
                        "id": entry["id"],
                        "source": entry["source"],
                        "layout": entry["layout"],
                        "classes": entry["classes"],
                        "policy_defaults": entry["policy_defaults"],
                        "findings": entry["findings"],
                        "result": "failed",
                        "error": format_process_error(err),
                    }
                )
            except Exception as err:
                results.append(
                    {
                        "id": entry["id"],
                        "source": entry["source"],
                        "layout": entry["layout"],
                        "classes": entry["classes"],
                        "policy_defaults": entry["policy_defaults"],
                        "findings": entry["findings"],
                        "result": "failed",
                        "error": {"message": str(err)},
                    }
                )

        report = {
            "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z"),
            "mainline_commit": mainline_commit,
            "mq_bin": args.mq_bin,
            "matrix": str(matrix_path.relative_to(repo_root)),
            "result": "passed" if all(item["result"] == "passed" for item in results) else "failed",
            "repos": results,
        }

        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps(report, indent=2))

        if args.keep_workdirs:
            if args.keep_workdirs_dir:
                keep_root = Path(args.keep_workdirs_dir).resolve()
                if keep_root.exists():
                    shutil.rmtree(keep_root)
            else:
                keep_root = Path(tempfile.mkdtemp(prefix="mainline-cert-workdirs-")).resolve()
            shutil.copytree(temp_root, keep_root)
            print(f"preserved certification workdirs at {keep_root}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    sys.exit(main())
