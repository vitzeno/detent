#!/usr/bin/env python3
"""
Phase 0b feasibility spike for Detent: target/argument resolution.

Given a goal, a capability that has already run, and a code-built list of
real candidate rows that capability's output produced, does Jev pick the one
the goal refers to -- or correctly say none/more-than-one match?

Capability selection and candidate extraction are both held fixed (out of
scope, same discipline as phase 0's spike.py) -- only selection among an
already-built candidate list is tested here. See docs/SPIKE_TARGET_RESOLUTION.md.

Two questions batched per call:
  - target_resolvable (Noul): does exactly one candidate unambiguously match?
  - target (Choice): which one -- criteria built PER CALL from this fixture's
    own candidates, unlike phase 0's static 12-option catalog.

Usage:
    export TYPESAFE_API_KEY=...
    python spike_targets.py ../fixtures/targets.yaml --repeats 3
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
MODEL = "jev-1.13.0"
NO_MATCH = "no_match"


def load_dotenv():
    """Minimal .env loader, same as spike.py: searches this script's directory
    and its parents so it works regardless of cwd. Real env vars win."""
    for d in (Path(__file__).resolve().parent, *Path(__file__).resolve().parents):
        env_path = d / ".env"
        if not env_path.is_file():
            continue
        for line in env_path.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            os.environ.setdefault(key.strip(), value.strip().strip("'\""))
        return


def build_questions(fixture: dict) -> dict:
    """target's criteria are built PER CALL from this fixture's own
    candidates -- the new mechanic this spike exists to test."""
    criteria = {c["id"]: c["desc"] for c in fixture["candidates"]}
    criteria[NO_MATCH] = "No candidate matches, or more than one plausibly does"
    return {
        "target_resolvable": {
            "type": "noul",
            "instructions": (
                "Given the goal and the candidates in `state.candidates`, does "
                "exactly one candidate unambiguously match what the goal "
                "refers to? Answer no if none match, or if more than one "
                "plausibly does (for example, two candidates tied on the "
                "distinguishing criterion the goal implies)."
            ),
        },
        "target": {
            "type": "choice",
            "instructions": (
                f"The capability '{fixture['capability']}' is about to run "
                f"against one of these candidates. Which one does the goal "
                f"refer to? Choose {NO_MATCH} if none match or more than one "
                f"plausibly does -- never guess the closest-looking one."
            ),
            "criteria": criteria,
        },
    }


def ask(state: dict, questions: dict, session: requests.Session, key: str) -> tuple[dict, float]:
    payload = {"model": MODEL, "state": state, "questions": questions}
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


def run_fixture(fixture: dict, repeat: int, session: requests.Session, key: str, writer):
    state = {
        "goal": fixture["goal"],
        "capability": fixture["capability"],
        "candidates": [
            {"id": c["id"], **c.get("fields", {})} for c in fixture["candidates"]
        ],
    }
    questions = build_questions(fixture)
    state_tokens = len(json.dumps(state)) // 4

    try:
        resp, latency_ms = ask(state, questions, session, key)
    except requests.HTTPError as e:
        print(f"  ! HTTP {e.response.status_code}: {e.response.text[:200]}", file=sys.stderr)
        return

    target_ans = resp["answers"]["target"]
    chosen = target_ans["choice"]
    resolvable_noul = resp["answers"]["target_resolvable"]["noul"]
    want = fixture["expected_target"]
    want_resolvable = int(want != NO_MATCH)

    writer.writerow({
        "fixture_id": fixture["goal"],
        "category": fixture["category"],
        "n_candidates": len(fixture["candidates"]),
        "repeat": repeat,
        "chosen": chosen,
        "expected": want,
        "correct": int(chosen == want),
        "confidence": target_ans.get("confidence"),
        "probabilities_json": json.dumps(target_ans.get("probabilities", {})),
        "resolvable_noul": resolvable_noul,
        "expected_resolvable": want_resolvable,
        "state_tokens": state_tokens,
        "input_tokens": resp.get("usage", {}).get("input_tokens"),
        "latency_ms": round(latency_ms, 1),
        "model": resp.get("model"),
    })

    mark = "ok " if chosen == want else "MISS"
    print(f"  [{mark}] chose {chosen} (want {want}) "
          f"conf={target_ans.get('confidence')} resolvable_noul={resolvable_noul}")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("fixtures")
    p.add_argument("--repeats", type=int, default=3, help="variance is a result, not noise")
    p.add_argument("--out", default=None, help="output filename (default: results_targets.csv)")
    p.add_argument("--run-dir", default=None,
                    help="directory to write results into (default: runs/<timestamp>/)")
    p.add_argument("--limit", type=int, default=None,
                    help="only run the first N fixtures (for a smoke test before a full run)")
    args = p.parse_args()

    load_dotenv()
    key = os.environ.get("TYPESAFE_API_KEY")
    if not key:
        sys.exit("TYPESAFE_API_KEY not set (checked environment and .env)")

    run_dir = Path(args.run_dir) if args.run_dir else Path("runs") / time.strftime("%Y%m%d_%H%M%S")
    run_dir.mkdir(parents=True, exist_ok=True)
    out_path = run_dir / (args.out or "results_targets.csv")

    with open(args.fixtures) as f:
        fixtures = yaml.safe_load(f)
    if args.limit:
        fixtures = fixtures[: args.limit]

    fields = ["fixture_id", "category", "n_candidates", "repeat", "chosen", "expected",
              "correct", "confidence", "probabilities_json", "resolvable_noul",
              "expected_resolvable", "state_tokens", "input_tokens", "latency_ms", "model"]

    with requests.Session() as session, open(out_path, "w", newline="") as fh:
        writer = csv.DictWriter(fh, fieldnames=fields)
        writer.writeheader()
        for rep in range(args.repeats):
            for fx in fixtures:
                print(f"\n[r{rep}] {fx['goal']}")
                run_fixture(fx, rep, session, key, writer)

    print(f"\nwrote {out_path} — this is a new question shape, sanity-check the first "
          f"few rows before trusting a full batch")


if __name__ == "__main__":
    main()
