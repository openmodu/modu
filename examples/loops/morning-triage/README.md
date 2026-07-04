# morning-triage — 第一个完整 loop 的参考实现

对照 loop-engineering 的五动作,这个目录把 modu 的六部件串成一个"小到不像系统"的端到端 loop:每天早上自己醒来,扫 CI/issues/commits,挑出今天值得做的事,写进磁盘状态,把修复派到隔离 worktree,让对抗式 reviewer 挑刺,PASS 才开 draft PR——永不 merge,拿不准的进 `./inbox/` 等人。

| 五动作 | 落在哪 |
|--------|--------|
| Discovery(找活) | `skills/morning-triage/SKILL.md` 的 Read+Judge 段 |
| Handoff(派活+隔离) | `triage-fixes.workflow.js`:每条 finding 一个 worktree |
| Verification(say no 的门) | workflow 里的 adversarial reviewer(同 `examples/agents/reviewer.md` 契约);goal 长跑则由 goal verifier 把关 |
| Persistence(记下) | `./state/triage.md` 提交回 repo;agent 会忘,repo 不会 |
| Scheduling(定下一轮) | `tasks.yaml`(本机,内嵌调度器)或 `github-actions-triage.yml`(云端) |

## 安装(本机变体)

1. 把 skill 装进目标仓库或全局:

   ```
   cp -r examples/loops/morning-triage/skills/morning-triage ~/.modu/skills/
   ```

2. (可选但推荐)装 reviewer 模板,让 goal verifier 和 review 共用同一份人设:

   ```
   mkdir -p ~/.modu/agents && cp examples/agents/reviewer.md ~/.modu/agents/
   ```

3. (可选)把修复 workflow 存成项目级 saved workflow,之后一句"run the triage-fixes workflow"就能跑:

   ```
   mkdir -p .coding_agent/workflows && cp examples/loops/morning-triage/triage-fixes.workflow.js .coding_agent/workflows/triage-fixes.js
   ```

4. 建任务——直接在 `modu_code` 里说"每个工作日早上 6 点跑 /morning-triage,超时 30 分钟,单次最多 40 万 token",或手工把 `tasks.yaml` 的内容并进 `~/.modu/cron/tasks.yaml`。**三档 cap 和 `daily_budget_tokens` 在第一次无人值守运行之前就要配好**(Cap Before You Ship)。

5. 开着 `modu_code`(交互 TUI)——调度器就嵌在里面,到点自动跑。运行记录:精简日志在 `~/.modu/cron/logs/morning-triage/`,完整 session 在 `~/.modu/sessions/` 里名为 `cron:morning-triage`。

云端变体见 `github-actions-triage.yml`(机器关着也跑);两个变体共享 `state/triage.md`(提交在 repo 里),可并存。

## inbox 约定(人门)

`./inbox/` 是 loop 的"不确定就交给人"出口:一事一文件,markdown,文件名是短 slug,正文写清发现了什么、需要人决定什么。**完成通知会自动带上本次 run 新增的 inbox 条目和新开的 PR 链接**——门被送到你眼前(telegram/feishu 里直接看到今天有几件事等你),不会写了没人看。处理完一条就删掉对应文件。

## Read a Sample(每天读一个)

loop 高速产出时,你和代码库的距离会悄悄拉远。纪律:不读全部(那就白搭了 loop),也不读零——**每天读一个 sample PR 或一次 run 的完整 session(`modu_code --resume` 按 `cron:morning-triage` 找),逼自己解释这次 loop 改了什么、为什么**。解释不出来 = 你脑里的地图掉队了,当天修比 incident 那天修便宜得多。

## 验收:连跑两天

这条只有跑出来才知道,不要脑补:第二天早上检查——

- `state/triage.md` 里昨天未完成的 finding 状态延续,已完成的没有被重做;
- 昨天写的 `./inbox/` 条目还在(没人处理就不该消失);
- 通知里出现的是**今天新增**的 inbox 条目,不是昨天的存量。

可以先跑一遍辅助检查,它只验证硬证据(两次 ok run、每条 cron log 都能连回一个名为 `cron:morning-triage` 的完整 session、每条 run 的日期写进 `state/triage.md`、state 至少两个日期、同一 finding 不从 closed/done 回到 open),不会替你判断质量:

```
bash examples/loops/morning-triage/verify-two-day.sh
```

这个两天验收脚本的正/负例也可以单独跑,会拒绝 strict 模式下的手动触发、同一天重复 log、缺少 issue 扫描的 session:

```
bash examples/loops/morning-triage/verify-two-day-fixtures.sh
```

等待真实 cron 之前,可以先检查本仓库和本机 cron 表是否装对(不会打印 token/secret):

```
bash examples/loops/morning-triage/verify-loop-readiness.sh
```

`tasks.yaml` 里同时给出 `morning-triage` 和 `morning-market-daily` 两个 loop。A 股任务必须写入固定结构的 `state/watchlist.md`,并在写完后读回确认标题、日期、题材表、标的表和已移出观察表存在;非交易日或核心数据为空时不创建也不更新 watchlist。

如果要确认自然 cron 真的有机会触发,还要确认交互式 `modu_code` 正在跑。用 tmux 挂着时可以这样查:

```
MODU_CRON_TMUX_SESSION=modu-loop-cron bash examples/loops/morning-triage/verify-scheduler-running.sh
```

想看下一次自然 tick 的具体时间,用这个只读检查。它加载真实 `~/.modu/cron/config.yaml` 和 task file,按 `pkg/cron/scheduler.Next` 算出 enabled tasks 的下一次触发。默认还要求 `morning-triage` 和 `morning-market-daily` 都是 enabled 且带 `goal:`,并且下一次触发分别落在 Asia/Shanghai 工作日 06:00 和 10:20;调试其它任务时可用 `EXPECTED_TASKS=""` 关闭这个约束:

```
bash examples/loops/morning-triage/verify-next-run-windows.sh
```

这个 verifier 自身的正/负例可以单独跑,会拒绝缺 goal、错时间、错时区和周末触发:

```
bash examples/loops/morning-triage/verify-next-run-windows-fixtures.sh
```

想先确认 harness 链路已经真跑过一次,可以查高频 canary 证据。它验证临时 `loop-smoke-fast` 曾由 scheduler 触发、创建 `goal:`、写入并读回 `state/loop-smoke.md`、调用 `update_goal(status=complete)`,同时确认临时 task 和临时 state 已清理;它只证明 Scheduling→Goal→Verifier→State 链路,不替代两天 triage 或交易日 A 股验收:

```
bash examples/loops/morning-triage/verify-loop-smoke-canary.sh
```

这个 checker 自身也有离线正/负例,会拒绝 manual trigger、缺 `update_goal`、临时 task 未删除、临时 state 未删除:

```
bash examples/loops/morning-triage/verify-loop-smoke-canary-fixtures.sh
```

`verify-live-loop.sh` 默认按 `modu-loop-cron` 这个 tmux 会话名检查;如果你换了会话名,运行前设置 `MODU_CRON_TMUX_SESSION`。
这个 preflight 还会比较 tmux 里的 `modu_code` 启动时间和 `cmd/`、`pkg/` 下最新 Go 源文件时间;如果改了 harness Go 代码但没重启 scheduler,它会失败,避免未来自然 tick 跑旧代码。它也会比较当前 daemon lifecycle 的 task load/reload 时间和 cron config/task file 的最新修改时间;如果 live task prompt 改了但 scheduler 还没 reload,同样失败。

最终验收可以跑聚合脚本。默认是严格模式:安装面、embedded scheduler 正在运行、goal verifier 已启用、下一次自然 tick、high-frequency canary 证据及其 fixture、修复合约、triage-fixes live verifier fixture、真实 triage-fixes reviewer/draft PR 证据、A 股 workflow 合约、A 股 live verifier fixture、两条自然 cron log、真实 `state/watchlist.md` 和对应的 scheduler-triggered/Asia-Shanghai/verifier-enabled/goal-complete 行情 cron log 都必须通过:

```
bash examples/loops/morning-triage/verify-live-loop.sh
```

如果只是查看当前 bootstrap 证据,可以临时打开:

```
ALLOW_BOOTSTRAP=1 MANUAL_DAY1_SESSION=/path/to/session.jsonl bash examples/loops/morning-triage/verify-live-loop.sh
```

修复半段的静态合约也可以单独检查:它确认 `triage-fixes` 仍然是 worktree 隔离、adversarial reviewer、PASS 才 draft PR、REJECT 写 inbox,并且完成通知会带新 inbox 条目和 PR 链接,但不替代真实 finding 的端到端实跑:

```
bash examples/loops/morning-triage/verify-triage-fixes-contract.sh
```

`triage-fixes` 入口也有一个空队列 smoke:它只证明 saved workflow 能被 workflow tool 启动、Load 阶段读了 `state/triage.md` 并返回 0 个 open finding;因为没有进入 Fix/Review/Deliver,它不替代真实 reviewer/draft PR 证据:

```
bash examples/loops/morning-triage/verify-triage-fixes-empty-smoke.sh
```

真实 reviewer/draft PR 路径用 live verifier 查:它要求 `state/triage.md` 至少有一个带当前仓库 PR URL 的 `pr-open` finding,同一个 session/log 证据里能看到 reviewer、`gh pr create --draft`、`state/triage.md` 更新路径和这同一个 PR URL,并且 `gh pr view` 确认 PR 是 open draft:

```
bash examples/loops/morning-triage/verify-triage-fixes-live.sh
```

如果第一天是手动 bootstrap 跑的 `/morning-triage`,第二天才交给 cron,可以显式传入第一天的完整 session:

```
MANUAL_DAY1_SESSION=/path/to/session.jsonl bash examples/loops/morning-triage/verify-two-day.sh
```

这个模式仍要求至少一条 `morning-triage` cron ok log 和 `state/triage.md` 里至少两个日期;传入的 day-1 session 还必须来自本仓库,并包含 CI/issues/recent commits/state 的 morning-triage 读取信号。它只是承认第一天不是 cron 触发。最终验收仍以两条定时 cron log 更硬。

未来自然调度器写出的新 log 会在第一行 `run_start` 带 `trigger:"scheduler"`、`timezone:"Asia/Shanghai"`、`has_goal:true` 和 `goal_verifier:true`,并在最后一行 `run_end` 带 `goal_status:"complete"`。要做严格的两工作日自然 cron 验收,不要传 `MANUAL_DAY1_SESSION`,并且必须有两条不同日期的 scheduler-triggered、Asia-Shanghai、verifier-enabled、goal-complete log:

```
REQUIRE_SCHEDULER_TRIGGER=1 bash examples/loops/morning-triage/verify-two-day.sh
```

另一条验尸式检查(Nodding Loop 探测):最近几轮里 reviewer 至少真 REJECT 过一次。几百轮全 PASS 在任何真实负载下都是统计学上不可能的——那说明门没在工作。
