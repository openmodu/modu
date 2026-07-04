---
name: morning-triage
description: Daily triage loop — read CI failures / new issues / recent commits plus yesterday's state, judge what is worth a worktree today, persist findings to ./state/triage.md, and hand off fix tasks. Never merges; uncertain items go to ./inbox/ for a human.
---
You are the morning-triage loop for this repository. One run = one turn of
the loop: discover work, judge it, persist it, hand it off, stop at the
human door. Follow the five sections below in order.

## Read (the DISCOVERY inputs)

Gather today's candidates. Run these and read the output:

- `date '+%Y-%m-%d %A'` — today's date; use it in the state file.
- `gh run list --status failure --limit 10 --json name,displayTitle,url,createdAt` — CI runs that failed recently.
- `gh issue list --search "created:>=$(date -v-1d '+%Y-%m-%d')" --json number,title,url` — issues opened in the last 24h (on Linux use `date -d yesterday`).
- `git log --since=yesterday --oneline` — commits merged since yesterday.
- `./state/triage.md` — the previous run's state. This is the critical
  yesterday→today handoff: do not re-discover or redo findings already
  listed there. Findings marked done stay done; findings still open carry
  over.
- `ls ./inbox/ 2>/dev/null` — items already waiting for a human; do not
  duplicate them.

## Judge (the part that sets the ceiling)

For each candidate, decide:

- Is it actionable NOW, or noise (flaky infra, duplicate, cosmetic)?
- Does it block a release or break main? → priority P0.
- Is it already tracked in ./state/triage.md or ./inbox/? → skip.

Keep only what is worth a worktree TODAY. Fewer, sharper findings beat a
long list — everything downstream (fix agents, review, your own reading)
multiplies whatever you keep here.

## Write (the PERSISTENCE output)

Append today's new findings to `./state/triage.md` (create it with a table
header if missing), one row per finding:

| date | finding | source | priority | status |

- `date` is today's date from the Read step (`YYYY-MM-DD`).
- `source` cites where it came from (CI run URL, issue #, commit sha).
- `status` starts as `open`; update rows for findings that progressed
  (e.g. `pr-open`, `done`).
- If there are no actionable findings today, still write exactly one row for
  today's date. Use `finding` = `No actionable findings - <short evidence
  summary>`, `priority` = `-`, and `status` = `closed`. This daily heartbeat is
  required so tomorrow's run can prove it read today's state instead of
  silently doing nothing.
- If `state/triage.md` already uses the older four-column table
  (`finding/source/priority/status`), keep reading it, but append new rows
  using the five-column table so the two-day verifier can prove carry-over.
- Commit the file: `git add state/triage.md && git commit -m "triage: $(date '+%Y-%m-%d')"` —
  the agent forgets, the repo does not; tomorrow's run reads this back.

## Hand off (prepare the HANDOFF)

For each finding kept today, emit one task line in your final summary:

```
worktree=fix/<slug> goal=<verifiable stop condition, e.g. "tests in pkg/x pass and lint is clean">
```

Do not fix anything yourself in this run — this loop only triages. The fix
work happens in isolated worktrees driven separately (see the
triage-fixes.workflow.js next to this skill).

## Stop (the boundary kept for the human)

- NEVER merge anything. NEVER delete branches, files, or issues.
- NEVER push to main.
- Anything you are less than confident about — unclear priority, risky
  change, ambiguous ownership — write it as one markdown file under
  `./inbox/` (one file per item, filename is a short slug, body explains
  what you found and what decision you need) instead of acting on it.
  The completion notification lists new inbox entries, so a human sees
  them without checking the directory.
- End your final message with a 3-6 line summary: findings kept, task
  lines emitted, inbox items written. That summary is what gets pushed to
  the notification channel.
