# Morning triage loop

`morning-triage` is a reference loop that reads CI failures, new issues, recent commits, and yesterday's state; selects work for today; persists findings; and hands fixes to isolated worktrees. Its boundary is deliberate: the triage run does not fix findings, merge Pull Requests, push to `main`, or guess when ownership or risk is unclear.

## Loop structure

| Action | Implementation |
|---|---|
| Discovery | The Read and Judge sections in `skills/morning-triage/SKILL.md` |
| Handoff and isolation | `triage-fixes.workflow.js`, with one Worktree per finding |
| Verification | Task-level `goal` and Goal Verifier; fix tasks also use the adversarial Reviewer contract in `examples/agents/reviewer.md` |
| Persistence | `state/triage.md` committed to the repository |
| Scheduling | Local `tasks.yaml` or cloud `github-actions-triage.yml` |

The local and cloud variants may coexist because both use the repository file as state. Prevent duplicate runs at the scheduling layer if both are enabled for the same time window.

## Install the local variant

1. Install the Skill for all repositories:

   ```bash
   cp -r examples/loops/morning-triage/skills/morning-triage ~/.modu/skills/
   ```

2. Install the Reviewer persona if Goal verification or the fix workflow should use the repository's reference contract:

   ```bash
   mkdir -p ~/.modu/agents
   cp examples/agents/reviewer.md ~/.modu/agents/
   ```

3. Save the fix workflow if you want to start it later by name:

   ```bash
   mkdir -p ~/.modu/workflows
   cp examples/loops/morning-triage/triage-fixes.workflow.js \
     ~/.modu/workflows/triage-fixes.js
   ```

4. In the interactive `modu_code` TUI, say:

   > 每个工作日早上 6 点运行 /morning-triage，目标是更新 state/triage.md，并把不确定项写入 inbox；超时 30 分钟，单次最多 40 万 Token。

   `tasks.yaml` contains the reference fields. Configure `timeout`, `max_tokens_per_run`, and `daily_budget_tokens` before the first unattended run.

5. Keep that interactive `modu_code` process running. The embedded scheduler stops when the process exits. Short logs are written below `~/.modu/cron/logs/<task_uuid>/`; complete Sessions are named `cron:morning-triage:<uuid-prefix>` below `~/.modu/sessions/`.

The commands inside `SKILL.md`, such as `gh run list` and `git log`, are instructions for the Agent's Bash tool. After installation, task creation, execution, and deletion can all be requested in natural language from `modu_code`.

## Why the task declares a Goal

At each Tick, the Runner creates the task-level Goal before sending `/morning-triage`. When the Agent calls `update_goal(status=complete)`, the configured Verifier checks the claimed state. A rejection continues the Session; repeated rejection, Goal budget exhaustion, timeout, or Token limits stop it.

The short log records this contract: `run_start` includes `trigger`, `timezone`, `has_goal`, and `goal_verifier`; `run_end` includes `goal_status`. `goal_paused`, `goal_budget_limited`, and `goal_unavailable` are circuit-breaker states and do not trigger error retries.

## Verify the pipeline in minutes

Do not wait for the next morning to test Scheduling → Goal → Verifier → State. In `modu_code`, create a temporary task:

> 添加测试任务 loop-smoke：每 30 秒运行一次；目标是“state/loop-smoke.md 存在，且最后一行是本次运行的时间戳”；Prompt 是“读取 state/loop-smoke.md（如果存在），再追加一行当前时间戳”；超时 3 分钟；单次最多 10 万 Token；不发送通知。

After two or three runs, accept the pipeline only if every `run_end` has `status:"ok"` and `goal_status:"complete"`, and `state/loop-smoke.md` gained one line per run. Then ask `modu_code` to remove the task and the temporary state file.

Go tests cover the code paths independently:

```bash
go test ./pkg/cron/... ./pkg/coding_agent/plugins/extension/goal/
```

## Human decision boundary

`inbox/` is the output for uncertainty. Use one Markdown file per decision, with a short slug as the filename. Each file must state what the loop found and what a human must decide. Completion notifications include files created or changed during the run, so unresolved decisions do not remain hidden in the repository.

Delete an Inbox file only after the decision is resolved.

## Read one sample each day

Do not review every generated artifact; that defeats the purpose of the loop. Do not review none either. Each day, open one sample Pull Request or resume one complete `cron:morning-triage:<uuid-prefix>` Session and explain what changed and why. If you cannot explain it, stop increasing automation until the model, Skill, or Verifier is corrected.

## Two-day acceptance

This loop is accepted only after two scheduled runs demonstrate persistence:

- Yesterday's open findings remain in `state/triage.md`; completed findings are not recreated.
- Existing `inbox/` files remain until a human resolves them.
- The notification lists only Inbox files created or changed by today's run.
- A naturally scheduled log starts with `trigger:"scheduler"` and ends with `goal_status:"complete"`.
- Across a meaningful sample of real runs, the Verifier sometimes rejects a completion claim. If it never rejects, inspect whether the verification gate is actually checking repository state.
