#!/usr/bin/env bash
set -euo pipefail

runs=25
output_dir="artifacts/soak"
test_pattern='TestStressParallelAgentQueueAndPublishCoalescing'

while [[ $# -gt 0 ]]; do
  case "$1" in
    --runs)
      runs="${2:?missing value for --runs}"
      shift 2
      ;;
    --output)
      output_dir="${2:?missing value for --output}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if ! [[ "${runs}" =~ ^[0-9]+$ ]] || [[ "${runs}" -le 0 ]]; then
  echo "--runs must be a positive integer" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
if [[ "${output_dir}" = /* ]]; then
  soak_root="${output_dir}"
else
  soak_root="${repo_root}/${output_dir}"
fi

rm -rf "${soak_root}"
mkdir -p "${soak_root}/runs"

pass_count=0
fail_count=0
summary_rows=()

for i in $(seq 1 "${runs}"); do
  run_id="$(printf 'run-%03d' "${i}")"
  run_dir="${soak_root}/runs/${run_id}"
  mkdir -p "${run_dir}"
  log_path="${run_dir}/go-test.jsonl"
  report_path="${run_dir}/report.json"

  start_epoch="$(python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
)"

  status="passed"
  if (
    cd "${repo_root}" && \
      MAINLINE_STRESS_REPORT_PATH="${report_path}" \
      go test ./internal/app -run "${test_pattern}" -count=1 -json
  ) >"${log_path}" 2>&1; then
    pass_count=$((pass_count + 1))
  else
    status="failed"
    fail_count=$((fail_count + 1))
  fi

  end_epoch="$(python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
)"
  wall_ms=$((end_epoch - start_epoch))

  summary_rows+=("${run_id}:${status}:${wall_ms}:${report_path}:${log_path}")
done

python3 - "${soak_root}" "${runs}" "${pass_count}" "${fail_count}" "${summary_rows[@]}" <<'PY'
import datetime
import json
import pathlib
import sys

soak_root = pathlib.Path(sys.argv[1])
runs = int(sys.argv[2])
pass_count = int(sys.argv[3])
fail_count = int(sys.argv[4])
rows = sys.argv[5:]

run_summaries = []
duration_values = []
queued_submission_values = []
queued_publish_values = []

for row in rows:
    run_id, status, wall_ms, report_path, log_path = row.split(":", 4)
    wall_ms = int(wall_ms)
    report_file = pathlib.Path(report_path)
    report = None
    if report_file.exists():
        report = json.loads(report_file.read_text())
        duration_values.append(int(report.get("duration_ms", 0)))
        queued_submission_values.append(int(report.get("max_queued_submissions", 0)))
        queued_publish_values.append(int(report.get("max_queued_publishes", 0)))

    run_summaries.append({
        "run_id": run_id,
        "status": status,
        "wall_ms": wall_ms,
        "report_path": str(report_file.relative_to(soak_root)),
        "log_path": str(pathlib.Path(log_path).relative_to(soak_root)),
        "stress_report": report,
    })

summary = {
    "runs": runs,
    "passed_runs": pass_count,
    "failed_runs": fail_count,
    "flake_rate": fail_count / runs,
    "avg_duration_ms": (sum(duration_values) / len(duration_values)) if duration_values else None,
    "max_duration_ms": max(duration_values) if duration_values else None,
    "max_queued_submissions_seen": max(queued_submission_values) if queued_submission_values else None,
    "max_queued_publishes_seen": max(queued_publish_values) if queued_publish_values else None,
    "generated_at": datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds").replace("+00:00", "Z"),
    "runs_detail": run_summaries,
}

(soak_root / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
print(json.dumps(summary, indent=2))
PY
