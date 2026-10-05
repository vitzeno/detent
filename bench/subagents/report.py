"""What subagents did in recorded sessions, read from detent's events.db.

    python3 bench/subagents/report.py <session-id>...
    python3 bench/subagents/report.py --last 8

One line per session: whether subagents were on, how the first request
ended, its wall-clock and tokens, how many subagents it started and how
they ended, how many did two calls or fewer (over-delegation), and how many
paths the root read again after a subagent of the same request had read them.
"""

import json
import os
import sqlite3
import sys
from collections import Counter

DB = os.path.expanduser("~/.local/state/detent/events.db")


def sessions(db, args):
    if args[:1] == ["--last"]:
        n = int(args[1]) if len(args) > 1 else 10
        return [r[0] for r in db.execute("SELECT id FROM sessions ORDER BY started DESC LIMIT ?", (n,))][::-1]
    return args


def report(db, sid):
    rows = db.execute(
        "SELECT kind, at, agent, payload FROM events WHERE session = ? ORDER BY ordinal", (sid,)
    ).fetchall()
    on, start, end, reason, tokens = None, None, None, "-", 0
    ended, calls, child_reads = Counter(), Counter(), set()
    rereads = 0
    for kind, at, agent, payload in rows:
        p = json.loads(payload)
        if kind == "session.started":
            on = p.get("Subagents", False)
        elif kind == "turn.started" and start is None:
            start = at
        elif kind == "turn.ended" and end is None:
            end, reason = at, p.get("Reason", "-")
            u = p.get("Usage") or {}
            tokens = u.get("PromptTokens", 0) + u.get("CompletionTokens", 0)
        elif kind == "agent.ended":
            ended[p.get("Reason", "?")] += 1
        elif kind == "tool_call.proposed":
            path = (p.get("Args") or {}).get("path")
            if agent:
                calls[agent] += 1
                if p.get("Tool") == "read_file" and path:
                    child_reads.add(path)
            elif p.get("Tool") == "read_file" and path in child_reads:
                # Only once a child has read it: what the root read first is not a repeat.
                rereads += 1
    spawned = sum(ended.values())
    small = sum(1 for a in calls if calls[a] <= 2) + (spawned - len(calls))
    secs = (end - start) / 1000 if start and end else 0
    outcome = ", ".join(f"{n} {r}" for r, n in sorted(ended.items())) or "-"
    print(f"{sid}  {'on ' if on else 'off'}  {reason:7} {secs:6.0f}s {tokens:>8} tok  "
          f"agents {spawned} ({outcome})  small {small}  re-read {rereads}")


def main():
    db = sqlite3.connect(DB)
    for sid in sessions(db, sys.argv[1:]):
        report(db, sid)


if __name__ == "__main__":
    main()
