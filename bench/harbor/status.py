"""Where each Harbor job stands: trials done, running, and scored.

    python3 bench/harbor/status.py [jobs dir]    (default ~/.cache/detent-bench/jobs)
"""

import json
import sys
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path


def load(path: Path) -> dict:
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return {}


def age(trial: Path, stamp: str | None) -> str:
    # A running trial has no result yet, so its directory says when it began.
    if stamp:
        started = datetime.fromisoformat(stamp.replace("Z", "+00:00"))
        if started.tzinfo is None:
            started = started.astimezone()
    else:
        stat = trial.stat()
        started = datetime.fromtimestamp(getattr(stat, "st_birthtime", stat.st_ctime), timezone.utc)
    mins = int((datetime.now(timezone.utc) - started).total_seconds() // 60)
    return f"{mins}m"


def steps(trial: Path) -> int:
    return sum(log.read_text().count('"step.ended"') for log in trial.glob("agent/detent/*.jsonl"))


def main() -> None:
    root = Path(sys.argv[1] if len(sys.argv) > 1 else "~/.cache/detent-bench/jobs").expanduser()
    for job in sorted(root.iterdir(), key=lambda p: p.stat().st_mtime):
        if not (job / "config.json").exists():
            continue
        summary = load(job / "result.json")
        trials = sorted(t for t in job.iterdir() if t.is_dir() and "__" in t.name)
        by_task: dict[str, list[str]] = defaultdict(list)
        running, scores = [], []
        for t in trials:
            r = load(t / "result.json")
            reward = ((r.get("verifier_result") or {}).get("rewards") or {}).get("reward")
            error = (r.get("exception_info") or {}).get("exception_type")
            task = t.name.split("__")[0]
            if reward is not None:
                scores.append(reward)
                by_task[task].append("✓" if reward >= 1 else "✗")
            elif error:
                scores.append(0.0)
                by_task[task].append("!")
            else:
                by_task[task].append("…")
                n = steps(t)
                running.append(f"{task} ({age(t, r.get('started_at'))}" + (f", {n} steps)" if n else ")"))
        mean = f"{sum(scores) / len(scores):.3f}" if scores else "-"
        state = "done" if summary.get("finished_at") else "running"
        print(f"{job.name}  [{state}]  {len(scores)}/{len(trials)} finished, mean {mean}")
        for task, marks in sorted(by_task.items()):
            print(f"  {task:32} {' '.join(marks)}")
        for r in running:
            print(f"  running: {r}")
        print()
    print("✓ passed  ✗ failed  ! errored  … running")


if __name__ == "__main__":
    main()
