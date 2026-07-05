# morning-triage — 第一个完整 loop 的参考实现

对照 loop-engineering 的五动作,这个目录把 modu 的六部件串成一个"小到不像系统"的端到端 loop:每天早上自己醒来,扫 CI/issues/commits,挑出今天值得做的事,写进磁盘状态,把修复派到隔离 worktree,让对抗式 reviewer 挑刺,PASS 才开 draft PR——永不 merge,拿不准的进 `./inbox/` 等人。

| 五动作 | 落在哪 |
|--------|--------|
| Discovery(找活) | `skills/morning-triage/SKILL.md` 的 Read+Judge 段 |
| Handoff(派活+隔离) | `triage-fixes.workflow.js`:每条 finding 一个 worktree |
| Verification(say no 的门) | 任务级 `goal:` + goal verifier(maker-checker);修复段还有 workflow 里的 adversarial reviewer(同 `examples/agents/reviewer.md` 契约) |
| Persistence(记下) | `./state/triage.md` 提交回 repo;agent 会忘,repo 不会 |
| Scheduling(定下一轮) | `tasks.yaml`(本机,内嵌调度器)或 `github-actions-triage.yml`(云端) |

## 先说清楚:这里没有需要你执行的 shell

这个目录只有四类东西:skill(markdown)、任务配置(yaml)、workflow(js)、云端 CI 变体(yaml)。**你在 modu_code 里的全部操作是说话**——"每个工作日早上 6 点跑 /morning-triage"、"run the triage-fixes workflow"、"删掉 smoke-test 任务"。

SKILL.md 里出现的 `gh run list` / `git log` 这类命令,是写给 **agent** 的操作指引——它用自己的 bash 工具去执行,你不需要碰。写具体命令而不是"看看 CI 有没有挂"是刻意的:弱一点的模型照着命令走更稳,不会自己发明数据源。

验证 loop 是否工作同样不需要 shell 脚本:代码行为由 Go 测试守着(`go test ./pkg/cron/... ./pkg/coding_agent/plugins/extension/goal/`),链路是否通用下面的「快速功能验证」跑一遍,长期质量靠「Read a Sample」纪律和飞书通知里的证据。

## 安装(本机变体)

1. 把 skill 装到用户级(所有仓库可用):

   ```
   cp -r examples/loops/morning-triage/skills/morning-triage ~/.modu/skills/
   ```

2. (可选但推荐)装 reviewer 模板,goal verifier 和 review 共用同一份人设:

   ```
   mkdir -p ~/.modu/agents && cp examples/agents/reviewer.md ~/.modu/agents/
   ```

3. (可选)把修复 workflow 存成 saved workflow,之后一句"run the triage-fixes workflow"就能跑:

   ```
   mkdir -p ~/.modu/workflows && cp examples/loops/morning-triage/triage-fixes.workflow.js ~/.modu/workflows/triage-fixes.js
   ```

4. 建任务——直接在 `modu_code` 里说"每个工作日早上 6 点跑 /morning-triage,目标是更新 state/triage.md 并把拿不准的放进 inbox,超时 30 分钟,单次最多 40 万 token"。参考 `tasks.yaml` 里的完整字段(含 `goal:` 和三档 cap)。**cap 和 `daily_budget_tokens` 在第一次无人值守运行之前就要配好**(Cap Before You Ship)。

5. 开着 `modu_code`(交互 TUI)——调度器就嵌在里面,到点自动跑。运行记录:精简日志在 `~/.modu/cron/logs/morning-triage/`,完整 session 在 `~/.modu/sessions/` 里名为 `cron:morning-triage`。

云端变体见 `github-actions-triage.yml`(机器关着也跑);两个变体共享 `state/triage.md`(提交在 repo 里),可并存。

## `goal:` 字段——调度和裁判焊在一起

任务声明 `goal:` 后,每个 tick 开始时 runner 直接创建 session goal 再发 prompt;agent 干完调 `update_goal(status=complete)` 时由 goal verifier(若开启)独立查证,REJECT 会带理由驳回并续跑,直到 PASS、连续驳回转 paused、或被 cap 打断。运行证据都在日志里:`run_start` 带 `trigger`(scheduler/manual)、`timezone`、`has_goal`、`goal_verifier`;`run_end` 带 `goal_status`。`goal_paused` / `goal_budget_limited` / `goal_unavailable` 是断路器状态,不触发重试。

## 快速功能验证(几分钟,不占用真实任务)

想确认链路(Scheduling→Goal→Verifier→State)是通的,不需要等到明天早上,加一个短高频的一次性任务即可。在 modu_code 里说:

> 加一个测试任务 loop-smoke:每 30 秒跑一次,目标是"state/loop-smoke.md 存在且最后一行是本次运行的时间戳",prompt 是"读 state/loop-smoke.md(它是你上一轮的记忆),然后追加一行当前时间戳",超时 3 分钟,单次 10 万 token,不要通知

跑两三轮后检查:`~/.modu/cron/logs/loop-smoke/` 里每条 `run_end` 都是 `status:"ok"` 且 `goal_status:"complete"`;`state/loop-smoke.md` 每轮多一行(feeds itself)。然后说"删掉 loop-smoke 任务,把 state/loop-smoke.md 也删了"。

## inbox 约定(人门)

`./inbox/` 是 loop 的"不确定就交给人"出口:一事一文件,markdown,文件名是短 slug,正文写清发现了什么、需要人决定什么。**完成通知会自动带上本次 run 新增的 inbox 条目和新开的 PR 链接**——门被送到你眼前(telegram/feishu 里直接看到今天有几件事等你),不会写了没人看。处理完一条就删掉对应文件。

## Read a Sample(每天读一个)

loop 高速产出时,你和代码库的距离会悄悄拉远。纪律:不读全部(那就白搭了 loop),也不读零——**每天读一个 sample PR 或一次 run 的完整 session(`modu_code --resume` 按 `cron:morning-triage` 找),逼自己解释这次 loop 改了什么、为什么**。解释不出来 = 你脑里的地图掉队了,当天修比 incident 那天修便宜得多。

## 验收:连跑两天

这条只有跑出来才知道,不要脑补:第二天早上检查——

- `state/triage.md` 里昨天未完成的 finding 状态延续,已完成的没有被重做;
- 昨天写的 `./inbox/` 条目还在(没人处理就不该消失);
- 通知里出现的是**今天新增**的 inbox 条目,不是昨天的存量;
- 自然调度的 log 第一行是 `trigger:"scheduler"`,最后一行 `goal_status:"complete"`。

另一条验尸式检查(Nodding Loop 探测):最近几轮里 verifier 至少真 REJECT 过一次。几百轮全 PASS 在任何真实负载下都是统计学上不可能的——那说明门没在工作。
