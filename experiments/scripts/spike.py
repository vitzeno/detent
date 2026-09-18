#!/usr/bin/env python3
"""
Phase 0 feasibility spike for Detent, a natural-language action harness.

Replays canned command output through a simulated loop and records what Jev
picks at each step. Nothing is executed; nothing mutates.

Usage:
    export TYPESAFE_API_KEY=...
    python spike.py fixtures.yaml --strategy reduced --repeats 3 --out results.csv

UNTESTED against the live API — verify the request shape on the first run
(docs.typesafe.ai/api) before trusting a batch of results.
"""

import argparse
import csv
import json
import os
import sys
import time
from pathlib import Path

import requests
import yaml

ENDPOINT = "https://api.typesafe.ai/v1/systemone"
MODEL = "jev-1.13.0"  # pinned deliberately: an alias moving mid-spike invalidates it


def load_dotenv():
    """Minimal .env loader: KEY=VALUE per line, no external dependency.
    Searches this script's directory and its parents so it works regardless
    of cwd. Real environment variables always win over the file."""
    for d in (Path(__file__).resolve().parent, *Path(__file__).resolve().parents):
        env_path = d / ".env"
        if not env_path.is_file():
            continue
        for line in env_path.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            key, value = key.strip(), value.strip().strip("'\"")
            os.environ.setdefault(key, value)
        return

# The capability catalog. Descriptions are the actual experimental variable —
# when a fixture fails on "right evidence, wrong pick", this is what you change.
#
# 2026-09-19a: reworded cannot_proceed plus 4 capabilities that kept getting
# wrongly picked on unsatisfiable fixtures (list_files, process_list,
# kill_process, git_status), using the documented what/not_for/examples
# structure (docs.typesafe.ai/primitives/choice) instead of one-line strings.
# Result: unsatisfiable step-0 accuracy 20% -> 71%. One regression found:
# list_files's new "not for a fuzzy description" clause made "read whichever
# readme" pick find_files instead (9/9) — the word "whichever" pattern-matched
# "fuzzy description". Fixed below by making the list_files/find_files
# boundary symmetric instead of one-sided.
#
# 2026-09-19b: extended the same structure to all remaining capabilities
# (port_listeners, disk_usage, find_files, read_file, search_content,
# tail_log, git_log), since the pairwise confusions run in more directions
# than the 4 already fixed (find_files/list_files, read_file/search_content,
# tail_log/read_file, git_log/git_status, port_listeners/process_list). Also
# sharpened read_file against the still-unsolved "install the missing npm
# dependency" holdout (0/9 correct) — it kept picking read_file to inspect
# package.json, treating "gather information about X" as progress toward "do
# X". Added an explicit not_for clause for that.
#
# 2026-09-19c: a 9-replicate-per-fixture stability check (same input, 3
# strategies x 3 repeats at step 0) found zero run-to-run variance anywhere —
# every failure is a stable, systematic miscalibration, never flaky — and
# surfaced 3 previously-unseen stable-wrong fixtures beyond the known
# find_files/list_files "whichever" regression:
#   - "the go module file" / "the go module for glow" -> find_files instead
#     of read_file (find_files's "cannot already name" phrasing over-triggers
#     on files named by ROLE rather than literal filename)
#   - "most recently CHANGED file" -> git_status instead of find_files (the
#     word "changed" lexically collides with git_status's own "uncommitted
#     changes" wording, even though the goal means filesystem mtime, not git)
# Fix: distinguish files with a single strict, tool-enforced name (go.mod is
# ALWAYS go.mod for any Go module — read_file can resolve it without a find
# step) from files whose name varies by project (a readme could be README.md,
# readme.txt, etc. — deliberately NOT added to read_file's examples, so this
# doesn't undo the list_files fix from 19a by making read_file over-eager
# too). And de-collide "changed" from git_status by making find_files
# explicitly own "most recently modified" and git_status explicitly disown
# filesystem timestamps.
CAPABILITIES = {
    "port_listeners": {
        "what": "Show which single process is listening on one specific network port.",
        "not_for": "Listing all running processes in general — use process_list for that.",
    },
    "process_list": {
        "what": "List running processes with their CPU and memory usage, to identify one by resource usage.",
        "not_for": "Restarting, reconfiguring, or otherwise changing how a service runs — it only shows what is currently running.",
    },
    "disk_usage": {
        "what": "Show how much disk space directories or files are using, to find what's largest.",
        "not_for": "Listing file names for their own sake (use list_files or find_files) or freeing up space — it only reports sizes.",
    },
    "find_files": {
        "what": "Search across a directory tree for files matching a name pattern, how recently they were modified, or size.",
        "not_for": "Listing everything in one specific, already-known directory (use list_files) or searching inside file contents (use search_content). Also not for a file with a single strict, tool-enforced name — e.g. a Go module file is always named exactly go.mod — read that directly instead of searching for it. Merely mentioning \"file\" or \"this file\" in a goal does not by itself mean a file needs to be located — check whether the goal's actual action is even something a file-related capability can do.",
    },
    "list_files": {
        "what": "List the file and directory names inside one specific, already-known directory.",
        "not_for": "Renaming, moving, deleting, creating, or changing a file — listing what exists does not accomplish any of those. If the directory itself is not yet known, that's find_files instead.",
    },
    "read_file": {
        "what": "Show the full contents of one specific file — including one identified by a strict, tool-enforced naming convention (such as a Go module file, always exactly go.mod) even if that exact filename wasn't spelled out, since there's only one possible name and location for it.",
        "not_for": "Installing, modifying, fixing, or otherwise acting on what the file contains — reading it is not the same as acting on it. Also not for searching across many files (use search_content), or a file whose name varies by project rather than being fixed by convention (e.g. a readme could be README.md, readme.txt, or something else) — list the directory first to see what's actually there.",
    },
    "search_content": {
        "what": "Search inside file contents across a directory tree for a string or pattern, to find which file(s) mention something.",
        "not_for": "Showing the full contents of one already-known file (use read_file), or finding files by name, age, or size rather than content (use find_files).",
    },
    "tail_log": {
        "what": "Show the most recent lines of one specific, already-known log file.",
        "not_for": "Reading a full non-log file top to bottom (use read_file), or searching a log for a specific string across its whole history (use search_content).",
    },
    "git_status": {
        "what": "Show which tracked files have uncommitted changes (differences from the last commit), and which files are untracked.",
        "not_for": "Deploying, publishing, or shipping code — it only reports local working-tree state. Also not for finding which file was most recently modified by filesystem timestamp (use find_files) — that's a disk-mtime question, not a git question, even though both can be described as a file that 'changed'.",
    },
    "git_log": {
        "what": "Show recent commit history for the current repository.",
        "not_for": "What is currently modified but not yet committed — use git_status for that; this only shows already-committed history. Also not for finding which file was most recently modified on disk (use find_files) — that's a filesystem timestamp question, unrelated to git, even when the goal says a file 'changed' recently.",
    },
    "kill_process": {
        "what": "Terminate one specific, already-identified running process by PID.",
        "not_for": "Restarting a service or any lifecycle action beyond ending that one process. If no relevant process has been identified yet, or the goal needs more than terminating a process, this alone does not make progress.",
    },
}

TERMINALS = {
    "done": "The goal has been achieved; nothing further is needed",
    "cannot_proceed": {
        "what": "No capability in this list can perform the specific action the goal names, even if one of them touches the same general area (files, processes, git, disk, logs). Before picking any other option, check the goal's actual verb (rename, restart, deploy, install, update, upgrade, encrypt, send, compress, translate, etc.) against what each capability literally does — not just whether the goal mentions a matching noun like 'file' or 'process'. Investigating, reading, or listing information related to the goal is not the same as performing it — inspecting what exists does not install, update, upgrade, deploy, rename, or otherwise change anything. Choose this when running every capability here would still leave the goal's actual action undone.",
        "not_for": "A goal that some capability here can directly satisfy, including as a first investigative step before a further action that this same list can also perform.",
        "examples": ["compress this video", "translate this document to French",
                     "reboot the router", "print this page", "encrypt this file",
                     "upgrade the database schema"],
    },
}

MAX_STEPS = 6


def build_questions() -> dict:
    return {
        "next_action": {
            "type": "choice",
            "instructions": (
                "Given the goal and the findings so far, what should happen next?"
            ),
            "criteria": {**CAPABILITIES, **TERMINALS},
        },
        "goal_achieved": {
            "type": "noul",
            "instructions": (
                "Has the stated goal been fully achieved given the findings so far?"
            ),
        },
        # Prototype (2026-09-19): decouples "is this possible at all" from "which
        # one" instead of asking cannot_proceed to win a 13-way vote against 12
        # positive options inside next_action. Self-contained (terse capability
        # gist, not the full what/not_for text) to stay cheap and to work whether
        # or not sibling question definitions are visible to each other.
        "goal_satisfiable": {
            "type": "noul",
            "instructions": (
                "The only actions available are: show what's listening on a port, "
                "list running processes, show disk usage, find files by name/age/size, "
                "list a directory's files, read one file's contents, search file "
                "contents for a pattern, tail a log file, show git status, show git "
                "log, and terminate one already-identified process. Can the goal be "
                "fully accomplished by using one or more of these in sequence — for "
                "example, using one to identify a target and a second to act on it? "
                "Answer yes if some combination or single use of them reaches the "
                "goal. Answer no only if the goal needs a different action entirely — "
                "installing, updating, upgrading, deploying, renaming, restarting a "
                "service, sending a message, or anything else not in that list — even "
                "if the goal mentions a file, process, or repository that these can "
                "inspect. Investigating or reading about something is not the same as "
                "doing it."
            ),
        },
    }


def ask(state: dict, session: requests.Session, key: str) -> tuple[dict, float]:
    """One call, both questions batched. Returns (answers, latency_ms)."""
    payload = {"model": MODEL, "state": state, "questions": build_questions()}
    t0 = time.perf_counter()
    r = session.post(
        ENDPOINT,
        json=payload,
        headers={"Authorization": f"Bearer {key}"},
        timeout=60,
    )
    latency_ms = (time.perf_counter() - t0) * 1000
    r.raise_for_status()
    return r.json(), latency_ms


# --- state building strategies (experiment E4) -------------------------------
# The A/B that decides whether the whole reduction design is worth building.

def make_fact(fixture: dict, action: str, strategy: str):
    raw = fixture["world"].get(action, "")
    if strategy == "raw":
        return raw
    if strategy == "truncated":
        return "\n".join(raw.splitlines()[:5])
    if strategy == "reduced":
        # Hand-written facts stand in for reducers you have not built yet.
        # Falls back to raw so a missing entry is visible in results rather
        # than silently degrading to a different strategy.
        reduced = fixture.get("reduced", {})
        if action not in reduced:
            print(f"  ! no reduced fact for {action}; falling back to raw", file=sys.stderr)
            return raw
        return reduced[action]
    raise ValueError(f"unknown strategy {strategy}")


# SPIKE.md E2 cheap variant: "inject two irrelevant findings into state at the
# start and re-run E1. If accuracy collapses, the reducers carry the entire
# design and need to be far more aggressive than planned." Fixed, goal-generic
# noise capabilities so every fixture gets the same controlled distractor
# volume, isolating pure state-volume effects from the "run already derailed"
# confound in the normal step-by-step curve (a correctly-behaving 2-step
# fixture never naturally produces a step-4 row to measure).
NOISE_CAPS = ["disk_usage", "tail_log"]


def run_fixture_noise_probe(fixture, strategy, repeat, session, key, writer):
    """One call per fixture: prepend NOISE_CAPS as fake prior findings, then
    ask whether the model still makes the correct FIRST real decision. Compare
    against that fixture's ordinary step-0 accuracy (0 findings) to isolate
    whether volume alone — not derailment — degrades the decision."""
    findings = [
        {"step": i + 1, "action": cap, "facts": make_fact(fixture, cap, strategy)}
        for i, cap in enumerate(NOISE_CAPS)
    ]
    state = {"goal": fixture["goal"], "findings": findings}
    state_tokens = len(json.dumps(state)) // 4

    try:
        resp, latency_ms = ask(state, session, key)
    except requests.HTTPError as e:
        print(f"  ! HTTP {e.response.status_code}: {e.response.text[:200]}", file=sys.stderr)
        return

    action_ans = resp["answers"]["next_action"]
    chosen = action_ans["choice"]
    want = fixture["expected_sequence"][0]

    writer.writerow({
        "fixture_id": fixture["goal"],
        "strategy": strategy,
        "repeat": repeat,
        "step_idx": 0,
        "chosen": chosen,
        "expected": want,
        "correct": int(chosen == want),
        "confidence": action_ans.get("confidence"),
        "probabilities_json": json.dumps(action_ans.get("probabilities", {})),
        "goal_noul": resp["answers"]["goal_achieved"]["noul"],
        "satisfiable_noul": resp["answers"].get("goal_satisfiable", {}).get("noul"),
        "state_tokens": state_tokens,
        "input_tokens": resp.get("usage", {}).get("input_tokens"),
        "latency_ms": round(latency_ms, 1),
        "model": resp.get("model"),
    })

    mark = "ok " if chosen == want else "MISS"
    print(f"  [{mark}] +2 noise findings: chose {chosen} (want {want}) "
          f"conf={action_ans.get('confidence')} noul={resp['answers']['goal_achieved']['noul']}")


def run_fixture(fixture, strategy, repeat, session, key, writer):
    """Simulate one loop run. Wrong picks are replayed too — a run must be able
    to go astray, or the experiment proves nothing."""
    findings = []
    expected = fixture["expected_sequence"]

    for step in range(MAX_STEPS):
        state = {"goal": fixture["goal"], "findings": findings}
        state_tokens = len(json.dumps(state)) // 4  # rough; replace with real count

        try:
            resp, latency_ms = ask(state, session, key)
        except requests.HTTPError as e:
            print(f"  ! HTTP {e.response.status_code}: {e.response.text[:200]}", file=sys.stderr)
            return

        action_ans = resp["answers"]["next_action"]
        chosen = action_ans["choice"]
        want = expected[step] if step < len(expected) else "done"

        writer.writerow({
            "fixture_id": fixture["goal"],
            "strategy": strategy,
            "repeat": repeat,
            "step_idx": step,
            "chosen": chosen,
            "expected": want,
            "correct": int(chosen == want),
            "confidence": action_ans.get("confidence"),
            "probabilities_json": json.dumps(action_ans.get("probabilities", {})),
            "goal_noul": resp["answers"]["goal_achieved"]["noul"],
            "satisfiable_noul": resp["answers"].get("goal_satisfiable", {}).get("noul"),
            "state_tokens": state_tokens,
            "input_tokens": resp.get("usage", {}).get("input_tokens"),
            "latency_ms": round(latency_ms, 1),
            "model": resp.get("model"),
        })

        mark = "ok " if chosen == want else "MISS"
        print(f"  [{mark}] step {step}: chose {chosen} (want {want}) "
              f"conf={action_ans.get('confidence')} noul={resp['answers']['goal_achieved']['noul']}")

        if chosen in TERMINALS:
            return

        findings.append({
            "step": step + 1,
            "action": chosen,
            "facts": make_fact(fixture, chosen, strategy),
        })


def main():
    p = argparse.ArgumentParser()
    p.add_argument("fixtures")
    p.add_argument("--strategy", choices=["raw", "reduced", "truncated"], default="reduced")
    p.add_argument("--repeats", type=int, default=3, help="variance is a result, not noise")
    p.add_argument("--out", default=None,
                    help="output filename (default: results_<strategy>.csv). Always written "
                         "inside --run-dir, never the bare cwd.")
    p.add_argument("--run-dir", default=None,
                    help="directory to write results into (default: runs/<timestamp>/, created "
                         "if missing). Pass the same --run-dir to multiple --strategy invocations "
                         "so a full raw/reduced/truncated matrix lands in one run folder together.")
    p.add_argument("--limit", type=int, default=None,
                    help="only run the first N fixtures (for a smoke test before a full run)")
    p.add_argument("--ambient", default=None,
                    help="fallback world/reduced facts merged under every fixture's own, "
                         "so a wrong pick outside a fixture's anticipated capabilities gets "
                         "real (if generic) output instead of a silent empty fact. Defaults "
                         "to ambient.yaml next to the given --fixtures file.")
    p.add_argument("--inject-noise", action="store_true",
                    help="SPIKE.md's cheap E2 variant: one call per fixture with 2 irrelevant "
                         "findings (disk_usage, tail_log) prepended, testing the first real "
                         "decision under forced state volume instead of the normal loop")
    args = p.parse_args()

    load_dotenv()
    key = os.environ.get("TYPESAFE_API_KEY")
    if not key:
        sys.exit("TYPESAFE_API_KEY not set (checked environment and .env)")

    run_dir = Path(args.run_dir) if args.run_dir else Path("runs") / time.strftime("%Y%m%d_%H%M%S")
    run_dir.mkdir(parents=True, exist_ok=True)
    out_name = args.out or f"results_{args.strategy}.csv"
    out_path = run_dir / out_name

    with open(args.fixtures) as f:
        fixtures = yaml.safe_load(f)
    if args.limit:
        fixtures = fixtures[: args.limit]

    ambient_path = Path(args.ambient) if args.ambient else Path(args.fixtures).resolve().parent / "ambient.yaml"
    if ambient_path.exists():
        with open(ambient_path) as f:
            ambient = yaml.safe_load(f)
        for fx in fixtures:
            fx["world"] = {**ambient.get("world", {}), **fx.get("world", {})}
            fx["reduced"] = {**ambient.get("reduced", {}), **fx.get("reduced", {})}
    else:
        print(f"  ! no ambient file at {ambient_path}; wrong picks outside a fixture's own "
              f"world will get an empty fact", file=sys.stderr)

    fields = ["fixture_id", "strategy", "repeat", "step_idx", "chosen", "expected",
              "correct", "confidence", "probabilities_json", "goal_noul", "satisfiable_noul",
              "state_tokens", "input_tokens", "latency_ms", "model"]

    with requests.Session() as session, open(out_path, "w", newline="") as fh:
        writer = csv.DictWriter(fh, fieldnames=fields)
        writer.writeheader()
        probe = run_fixture_noise_probe if args.inject_noise else run_fixture
        for rep in range(args.repeats):
            for fx in fixtures:
                print(f"\n[{args.strategy} r{rep}] {fx['goal']}")
                probe(fx, args.strategy, rep, session, key, writer)

    print(f"\nwrote {out_path} — analyse per SPIKE.md, do not eyeball it")


if __name__ == "__main__":
    main()
