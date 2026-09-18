#!/usr/bin/env python3
"""
Turns spike.py's raw CSVs into the one-page answer SPIKE.md asks for.

Usage:
    python analyze.py results_raw.csv results_reduced.csv results_truncated.csv \
        --fixtures fixtures.yaml

Ground truth for E3 (goal_achieved) is derived by rejoining each row against
fixtures.yaml on (fixture_id, step_idx): a row's step_idx equals the number of
findings already appended when that call was made, so the goal is genuinely
achieved once step_idx >= the fixture's real (non-terminal) step count. For
"unsatisfiable" fixtures the goal is never achieved — the correct outcome is
cannot_proceed, not done — so every row is ground-truth "not achieved" there.
"""

import argparse
import sys

import pandas as pd
import yaml

TERMINALS = {"done", "cannot_proceed"}


def load_ground_truth(fixtures_path):
    fixtures = yaml.safe_load(open(fixtures_path))
    gt = {}
    for fx in fixtures:
        seq = fx["expected_sequence"]
        real_steps = [s for s in seq if s not in TERMINALS]
        is_unsatisfiable = any(s in TERMINALS for s in seq)
        gt[fx["goal"]] = (len(real_steps), is_unsatisfiable)
    return gt


def actually_achieved(row, gt):
    n_real, unsatisfiable = gt.get(row["fixture_id"], (None, False))
    if n_real is None:
        return None  # fixture not found in fixtures.yaml — shouldn't happen
    if unsatisfiable:
        return False
    return row["step_idx"] >= n_real


def pct(x):
    return f"{100 * x:.1f}%"


def falsification_numbers(df):
    print("## 1. Falsification numbers\n")

    step1 = df[df.step_idx == 0]
    acc1 = step1.correct.mean() if len(step1) else float("nan")
    verdict1 = "FAIL — loop not viable" if acc1 < 0.60 else "pass"
    print(f"- Step-1 selection accuracy: {pct(acc1)}  ({verdict1}, threshold 60%)")

    by_step = df.groupby(["strategy", "step_idx"]).correct.mean()
    drop_verdicts = []
    for strat in df.strategy.unique():
        s = by_step.get(strat)
        if s is None or 0 not in s.index:
            continue
        base = s[0]
        later = s[s.index >= 3]
        if len(later) == 0:
            continue
        worst_drop = base - later.min()
        drop_verdicts.append((strat, worst_drop))
    if drop_verdicts:
        for strat, drop in drop_verdicts:
            v = "FAIL — context rot dominates" if drop > 0.20 else "pass"
            print(f"- Accuracy drop step1→step4+ ({strat}): {pct(drop)}  ({v}, threshold 20pts)")
    else:
        print("- Accuracy drop step1→step4+: not enough steps recorded to compute")

    df = df.copy()
    df["actually_done"] = df.apply(lambda r: actually_achieved(r, GT), axis=1)
    not_done = df[df["actually_done"] == False]  # noqa: E712
    done = df[df["actually_done"] == True]  # noqa: E712
    if len(not_done):
        false_complete_rate = (not_done.goal_noul > THRESHOLD).mean()
        v = "FAIL — goal test cannot terminate runs safely" if false_complete_rate > 0.15 else "pass"
        print(f"- False \"complete\" rate (noul > {THRESHOLD} while not actually done): "
              f"{pct(false_complete_rate)}  ({v}, threshold 15%, the dangerous direction)")
    else:
        print("- False \"complete\" rate: no not-yet-done rows recorded")
    if len(done):
        false_incomplete_rate = (done.goal_noul <= THRESHOLD).mean()
        print(f"- False \"incomplete\" rate (noul <= {THRESHOLD} while actually done): "
              f"{pct(false_incomplete_rate)}  (no SPIKE.md threshold — burns budget, not dangerous)")
    else:
        print("- False \"incomplete\" rate: no actually-done rows recorded")

    if "raw" in df.strategy.unique() and "reduced" in df.strategy.unique():
        acc_raw = df[df.strategy == "raw"].correct.mean()
        acc_reduced = df[df.strategy == "reduced"].correct.mean()
        v = "FAIL — reduction design is wrong" if acc_reduced <= acc_raw else "pass"
        print(f"- Reduced vs raw accuracy: reduced={pct(acc_reduced)} raw={pct(acc_raw)}  ({v})")
    else:
        print("- Reduced vs raw accuracy: need both 'raw' and 'reduced' strategy runs to compare")
    print()


def accuracy_by_step(df):
    print("## 2. Accuracy by step, per strategy\n")
    table = df.groupby(["strategy", "step_idx"]).correct.agg(["mean", "count"])
    for strat in sorted(df.strategy.unique()):
        print(f"**{strat}**")
        sub = table.loc[strat] if strat in table.index.get_level_values(0) else None
        if sub is None:
            continue
        for step_idx, row in sub.iterrows():
            print(f"  step {step_idx}: {pct(row['mean'])}  (n={int(row['count'])})")
    print()


def goal_test_separation(df):
    print("## 3. Goal-test (Noul) separation\n")
    df = df.copy()
    df["actually_done"] = df.apply(lambda r: actually_achieved(r, GT), axis=1)
    done_vals = df[df["actually_done"] == True].goal_noul.dropna()  # noqa: E712
    not_done_vals = df[df["actually_done"] == False].goal_noul.dropna()  # noqa: E712

    if len(done_vals) == 0 or len(not_done_vals) == 0:
        print("Not enough rows in one of the two classes to assess separation.\n")
        return

    print(f"actually-done noul:     mean={done_vals.mean():.3f}  median={done_vals.median():.3f}  "
          f"n={len(done_vals)}")
    print(f"actually-not-done noul: mean={not_done_vals.mean():.3f}  median={not_done_vals.median():.3f}  "
          f"n={len(not_done_vals)}")

    best = None
    for t in [i / 100 for i in range(5, 100, 5)]:
        tpr = (done_vals > t).mean()
        fpr = (not_done_vals > t).mean()
        j = tpr - fpr  # Youden's J
        if best is None or j > best[1]:
            best = (t, j, tpr, fpr)
    t, j, tpr, fpr = best
    if j < 0.3:
        print(f"\nBest threshold found ({t}) still yields Youden's J = {j:.2f} "
              f"(TPR={pct(tpr)}, FPR={pct(fpr)}). The distributions overlap too much — "
              f"no usable threshold exists; the goal test needs replacing, not tuning.")
    else:
        print(f"\nRecommended threshold: {t}  (TPR={pct(tpr)}, FPR={pct(fpr)}, J={j:.2f})")
    print()


def latency(df):
    print("## 4. Latency\n")
    for strat in sorted(df.strategy.unique()):
        sub = df[df.strategy == strat].latency_ms.dropna()
        if len(sub) == 0:
            continue
        p50 = sub.quantile(0.50)
        p95 = sub.quantile(0.95)
        print(f"{strat}: p50={p50:.0f}ms  p95={p95:.0f}ms  n={len(sub)}")
    overall_p95 = df.latency_ms.dropna().quantile(0.95)
    if pd.notna(overall_p95):
        # crude step-budget-under-N-seconds implications
        for budget_s in (5, 10, 20):
            steps = int(budget_s * 1000 / overall_p95)
            print(f"  at p95 latency, a {budget_s}s soft budget implies ~{steps} steps")
    print()


def main():
    p = argparse.ArgumentParser()
    p.add_argument("csvs", nargs="+", help="one or more results CSVs from spike.py")
    p.add_argument("--fixtures", default="fixtures.yaml")
    p.add_argument("--threshold", type=float, default=0.5,
                    help="noul threshold to evaluate for the false-complete/incomplete check")
    args = p.parse_args()

    global GT, THRESHOLD
    GT = load_ground_truth(args.fixtures)
    THRESHOLD = args.threshold

    frames = [pd.read_csv(f) for f in args.csvs]
    df = pd.concat(frames, ignore_index=True)

    missing = set(df.fixture_id) - set(GT)
    if missing:
        print(f"WARNING: {len(missing)} fixture_id(s) in the CSV have no match in "
              f"{args.fixtures} — ground truth for E3 will be skipped for those rows.",
              file=sys.stderr)

    print(f"# Spike results — {len(df)} rows from {len(args.csvs)} file(s), "
          f"strategies: {sorted(df.strategy.unique())}\n")

    falsification_numbers(df)
    accuracy_by_step(df)
    goal_test_separation(df)
    latency(df)

    print("## 5. Recommendation\n")
    print("(fill in by hand after reading sections 1-4 — this script reports numbers, "
          "not the call. See SPIKE.md's falsification table and 'reading the results' "
          "section before deciding.)")


if __name__ == "__main__":
    main()
