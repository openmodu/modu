# modu_code Loop Engineering 设计与验收记录

本文回答一个问题：modu 要承载可长期运行的 loop，已经具备哪些能力，还缺哪些验收证据。它既是设计方案，也是按日期追加的真机验证记录。

状态标记：`[完成]`、`[部分完成]`、`[待做]`。

## 1. 判断框架

Loop engineering 位于 prompt、context 和 harness 之上：人不再逐轮派发任务，而是设计一个能定时启动、派生子 Agent、复用上一轮结果的循环。

一个可持续运行的 loop 必须包含五个动作：

- **Discovery（发现任务）**：缺失后仍需人工每天派活。
- **Handoff（分派与隔离）**：缺失后多个 Agent 会修改同一工作目录。
- **Verification（独立验收）**：缺失后执行者会自行批准结果。
- **Persistence（保存状态）**：缺失后每轮都要重新发现和判断。
- **Scheduling（安排下一轮）**：缺失后只能运行一次演示。

这五个动作分别依赖 Automations、Worktrees、Skills、Connectors、Sub-agents 和 Memory。判断实现是否可用时，再加三条硬约束：

- 单次预算、每日预算和最大重试次数必须在第一次自动运行前生效。
- PR 不自动合并；不确定项进入 inbox，并由人定期抽样。
- 停止条件由独立 evaluator 判断，不能由 generator 自行确认。

`/loop` 按间隔重复执行，不判断目标是否完成；`/goal` 在独立模型确认条件成立后停止。混用二者，只能得到定时执行，得不到独立验收。

## 2. 六项能力盘点

### 2.1 Automations / Scheduling — [完成，含断路器]

已完成：

- `pkg/cron` 使用 robfig/cron 六字段表达式，支持 `on_overlap: skip|queue|kill`，连续重叠会告警。
- 配置支持 fsnotify 热加载；加载失败时继续使用旧调度器。
- `cron_add`、`cron_list`、`cron_remove` 通过内置 `cron` 扩展注册到交互会话和渠道会话，不再维护独立的 cron CLI 或机器人。
- 每个 tick 创建独立 `CodingSession`；NDJSON 日志以 `run_end` 作为运行结束标记。
- webhook、Telegram、Feishu webhook 和 Feishu bot 可发送成功或失败通知，内容包含状态、耗时、日志路径和最后一段 Assistant 文本。
- `modu_code -p "<prompt>" -json` 可由 GitHub Actions 等外部调度器执行。

原缺口已补齐（见 §3.2）：已实现单次运行超时、Token 上限、最大重试次数和全局日额度，失控任务会被三档断路器终止。

当前调度器运行在普通 `modu_code` 交互式 TUI 进程中，TUI 退出时随 context 取消；`-p`、`-rpc`、`-acp` 等一次性或外部驱动模式不会启动调度器。因此“无人值守”要求至少有一台机器通过 tmux、screen 或服务管理器持续运行 `modu_code`。

cron tick 会加载 goal、subagent 和 workflow 扩展。`tasks.yaml` 的 `goal:` 字段由 runner 直接转成 session goal；无头 runner 继续执行 hidden continuation，直到 verifier 接受完成状态、goal 进入 paused/budgetLimited，或超时与 Token 上限终止运行。扩展配置损坏时，本轮降级为不加载扩展。

任务表通过 flock 和原子写避免跨进程损坏，fsnotify 负责接收变更。完整 session 以 `cron:<task_id>` 命名并写入 `~/.modu/sessions/`；精简运行日志写入 `~/.modu/cron/logs/<id>/`，调度器自身日志写入 `~/.modu/cron/daemon.log`。

自然调度验收要求连续两个工作日各有一份日志：`run_start` 必须包含 `trigger:"scheduler"`、`timezone:"Asia/Shanghai"`、`has_goal:true`、`goal_verifier:true`，`run_end` 必须包含 `goal_status:"complete"`；每个 `session_id` 都能定位到完整 session；`state/triage.md` 中同一 finding 不能从 closed/done 回退到 open。

### 2.2 Worktrees / Handoff — [基本完成]

已完成：

- `modu_code -worktree` 启动后进入隔离 worktree。
- `enter_worktree` 和 `exit_worktree` 允许 Agent 自行创建或退出 worktree。
- workflow 子 Agent 可设置 `isolation: "worktree"`，并行分支使用不同目录。
- workflow 默认并发数为 4，上限为 16；单次 workflow 最多创建 1000 个 child。

边界：“每条 finding 使用一个 worktree”仍是 skill 或 workflow 层的约定，harness 不会自动替任务选择隔离粒度。

### 2.3 Skills / Discovery — [完成]

已完成：

- `pkg/skills` 负责发现 SKILL.md，解析 frontmatter、多路径和 ignore 规则，并按 Agent Skills 规范注入 system prompt。
- slash 命令找不到内置模板时会回落到同名 skill。cron 的 `prompt` 可直接填写 `/skill-name`，例如 `modu_code -p "/morning-triage"`。

边界：Discovery 的 Read、Judge、Write、Stop 规则属于每个 loop 自己的 SKILL.md；§3.4 只提供参考实现。

### 2.4 Connectors — [完成]

已完成：

- `pkg/channels` 提供 Feishu、Telegram 和 bridge，用于入站消息与出站通知。
- workflow child 可显式请求 `web_fetch` 和 `web_search`。
- MCP client 支持 stdio 和 Streamable HTTP，session 可动态注册工具并转发给 child（见 §3.6）。

边界：外部系统是否可用仍取决于对应 MCP server、认证信息和网络权限；Bash 与 `gh` CLI 只覆盖 GitHub 一类场景，不能替代通用 connector。

### 2.5 Sub-agents / Verification — [完成：已接入 verifier]

已完成：

- `pkg/coding_agent/plugins/subagent` 使用 Markdown 定义子 Agent。frontmatter 支持工具白名单、`disallowed_tools`、模型覆盖、隔离方式、最大轮数、memory scope 和 permission mode；正文作为 system prompt。
- workflow 插件提供 `agent()`、`parallel()` 和 `pipeline()`；child 可通过 `schema` 校验 JSON Schema，并在失败后重试一次。
- `/goal` 支持隐藏续跑、Token 预算和证据式完成检查。

原核心缺口已补齐（见 §3.1）：goal 不再由 generator 自行判定完成。`update_goal(status=complete)` 会先经过独立、全新上下文的 verifier；拒绝结果必须给出理由，连续拒绝后转为 paused 并交给人工处理。

### 2.6 Memory / Persistence — [完成]

已完成：

- `pkg/coding_agent/services/memory` 与 `tools/memory` 保存持久记忆；`memory_scope` 支持 user 和 project，sub-agent 与 workflow child 均可声明。
- session 持久化与 `pkg/runtime` checkpoint journal 支持事件回溯、恢复和重入。
- modu cron 运行日志使用 NDJSON 落盘。

边界：`./state/triage.md` 和 `./inbox/` 由具体 skill 读写。memory 保存在磁盘，context 每轮重建，二者不能互相替代。

## 3. 实现与验收记录

### 3.1 P0 · Goal 独立裁判（maker-checker）— [2026-07-03 完成]

**问题**:`update_goal complete` 原先无条件生效,五动作里 Verification 这一动等于没装。这是五反例里最贵的 Nodding loop,也是文章 `/goal` 定义的本义("停止条件由 fresh model 判定")。

**已落地的实现**(`pkg/coding_agent/plugins/extension/goal/verifier.go` + tools.go/store.go/goal.go):

- 配置走 `~/.modu/extensions.yaml` 的 `config:` 块(复用扩展已有的 ApplyConfig 机制;旧路径 `~/.modu_code/extensions.yaml` 仍兼容)。该文件是**增量覆盖**而非白名单:只写 goal 一项,其余 builtin 扩展照常启用;要关某个扩展用 `enabled: false`。不配置 verifier 时保持自判,向后兼容:

  ```yaml
  extensions:
    - name: goal
      config:
        verifier:
          enabled: true
          model: ""        # 可选,verifier 换用的 model ID(建议与 generator 不同)
          max_rejects: 3   # 连续驳回多少次后转 paused,默认 3
          max_turns: 12    # verifier 子 agent 的轮数上限,默认 12
  ```

- 触发:agent 调 `update_goal(status=complete)` 时先过 verifier——`api.ForkSession` 起一个 **fresh context** 子 agent(Name=goal-verifier,不带 generator 的对话史)
  - system prompt 为 adversarial 模板:`ASSUME the objective is NOT met until proven otherwise`,要求 execute-don't-read、逐条对照 objective 找证据、末行输出 JSON verdict
  - 工具白名单 `read/grep/ls/find/bash`,无 write/edit
  - verdict 从最终文本解析(容忍 prose/代码围栏,取最后一个合法 `{"verdict":...,"reasons":[...]}`)
- 裁决:
  - PASS → 照常 `MarkComplete`,并通知用户"completion confirmed by independent check"
  - REJECT / verdict 不可解析 → `update_goal` 返回带逐条 reasons 的错误(`reject N/M`),goal 保持 active,现有 continuation 驱动下一轮修复;`Goal.VerifierRejects` 落盘计数
  - 连续 REJECT 达 `max_rejects` → goal 转 paused + 通知用户(人门);用户 resume 时计数清零,重新给一轮机会
  - ForkSession 基础设施错误 → **fail-open**(接受完成 + 通知"accepting completion unverified"),坏掉的裁判不 brick 已完成的 goal
- verifier 的消耗经由已有的 `subagent_child_usage` 事件自动折入 goal 的 token budget

**验收(已过)**:单测覆盖 REJECT 后 goal 仍 active 且错误含 reasons、连续驳回转 paused、resume 清零、PASS 完成、fail-open、不可解析驳回、禁用时不 fork(`verifier_test.go`,10 个用例全绿)。

### 3.2 P0 · modu_cron 三档上限 — [2026-07-03 完成]

**问题**:cron 任务原先没有任何断路器(仅调度器里一个写死的 30 分钟超时)。一个 bug 让 agent 整夜空转,唯一的发现方式是第二天看账单——文章四债务里的 token blowout,原文定性:"cap 不是省钱,是把开放性风险变成有界风险"。

**已落地的实现**(`pkg/cron/{config,runner,scheduler,runlog,cli,notify}`):

```yaml
# tasks.yaml 每任务三档
tasks:
  - id: morning-triage
    cron: "0 0 6 * * *"
    timezone: Asia/Shanghai
    prompt: "/morning-triage"
    goal: "Update state/triage.md with today's actionable findings and leave uncertain items in inbox/"
    timeout: 45m               # per-run 时长上限,默认 30m;run_end 记 status=timeout
    max_tokens_per_run: 500000 # per-run token 上限(input+output),越线立即 cancel,status=token_cap
    max_retries: 2             # 仅 status=error 时原地重试(退避 30s 起翻倍,上限 5m)

# config.yaml 全局日额度
daily_budget_tokens: 3000000
```

- per-run timeout:`runner.Execute` 内 `context.WithTimeout(task.EffectiveTimeout())`,daemon tick 和手动 `run <id>` 走同一道闸;调度器原来写死的 30m 改为 runner 负责
- per-run token cap:runner `session.Subscribe` 累计 assistant usage(input+output,与 goal budget 同口径),越线 cancel
- daily budget:`~/.modu/cron/logs/usage.json` 按本地时区天聚合(保留 31 天),tick 前检查、超额拒跑(`status=budget_exceeded`,run_start/run_end 照记,channel 照发通知),run 后累加
- max retries:`runner.ExecuteWithRetries`,**只重试 status=error**——timeout/token_cap/budget_exceeded/goal_unavailable/goal_paused/goal_budget_limited 是断路器,重试它们等于拆保险丝
- `run_end` 新增 `tokens` 字段;`status` 取值 `ok/error/timeout/token_cap/budget_exceeded/goal_unavailable/goal_paused/goal_budget_limited`;完成通知的 status 同步区分
- cap 配置非法时 `scheduler.Add` 报错 → daemon 热加载失败回滚(与非法 cron 同一套保护)

**验收(已过)**:单测覆盖字段解析/round-trip、EffectiveTimeout 回退、ValidateCaps、台账累加与 31 天裁剪、超日额度拒跑且 run_end 状态可区分、重试只发生在 plain error、ctx 取消停止重试(config/runlog/runner 三包全绿)。留一条实测项:配好真实 model 后,用一个死循环 prompt 验证三档至少一档能打断(見 README「三档 cap」节)。

### 3.3 P1 · Evaluator sub-agent 约定与内置模板 — [2026-07-03 完成]

**问题**:sub-agent 机制齐了,但仓库里没有一份"reviewer 长什么样"的参考,用户从零写容易漏掉 ASSUME BROKEN / execute-don't-read / VERDICT 三要素。

**已落地的实现**:

- `examples/agents/reviewer.md`:完整模板,对齐文章 §V-D——ROLE(adversarial maker-checker)/ ASSUME(broken until proven)/ CHECK(execute-don't-read,四步)/ USE(bash 跑测试、gh 查 CI)/ VERDICT(PASS 需全项通过,REJECT 必须逐条给理由,末行 JSON)。frontmatter:`tools: read, grep, ls, find, bash`、`max_turns: 12`、注释建议 `model` 覆盖("同 model 换 prompt 经常保留盲点")。装法:`cp examples/agents/reviewer.md ~/.modu/agents/`(或项目 `.coding_agent/agents/`)
- **与 §3.1 真打通**(不止文案层面):goal verifier 在 fork 前会查找名为 `reviewer` 的 subagent 定义——找到就用它的 body 当 system prompt(VERDICT JSON 契约会重新附加,保证解析不坏)、用它的 `model` 当默认模型(显式 verifier config 的 model 优先);工具沙箱**不**从定义取,verifier 永远保持自己的 read+bash 白名单。没有定义时回退内置 prompt,完全向后兼容。一份 `reviewer.md` 同时定制 workflow 里的 review agent 和 goal 裁判——一处维护
- 验收调整:modu 的 workflow `agent()` 没有按定义名解析的选项(那是设计时的假设,实际 API 是 prompt + tools/schema),所以 workflow 侧的 reviewer 用法是同一契约的内联 prompt + `schema: VERDICT`(见 §3.4 的 triage-fixes 脚本);"跑通一次真实 REJECT" 由 goal 侧单测覆盖(自定义 reviewer 定义被采用、契约被附加、model 优先级、沙箱不变,`verifier_test.go` 新增 2 例)

### 3.4 P1 · `morning-triage` 参考实现 — [2026-07-03 完成]

**问题**:六部件都在,但没有一个把五动作串起来的端到端样例。文章的态度:第一个 loop 要"小到不像一个系统"。

**已落地的实现**(`examples/loops/morning-triage/`,一比一落文章附录 A 的完整 first loop):

- `skills/morning-triage/SKILL.md`:Read(gh run list 失败 CI / 24h 新 issue / git log --since / 昨天的 `./state/triage.md` + `./inbox/`)→ Judge(actionable vs noise,"只留今天值得开 worktree 的")→ Write(追加 `./state/triage.md` 五列表格(date/finding/source/priority/status),无 actionable finding 也写一条当日 `No actionable findings`/`closed` heartbeat,commit 回仓库)→ Hand off(每条 finding 输出 `worktree=fix/slug goal=<可验证停止条件>`,本 run 只 triage 不修)→ **Stop 段**(Never merge / Never delete / Never push main / 不确定的一事一文件写 `./inbox/`)
- `tasks.yaml`:工作日 06:00 触发 `/morning-triage`,三档 cap + `daily_budget_tokens` + feishu_bot channel 全配好(Cap Before You Ship)
- `triage-fixes.workflow.js`:Load(只读 `state/triage.md` 解析 open findings,schema 校验,不在 Load 阶段扫源码/git/CI)→ Fix(每条 finding `isolation:'worktree'` 起草,批量上限 3——review 带宽即天花板)→ Review(对抗式 reviewer,同 `examples/agents/reviewer.md` 契约,`schema: VERDICT`)→ Deliver(PASS 才 `gh pr create --draft` 永不 merge;REJECT 写 `./inbox/<slug>.md`)。用 modu 的真实 workflow 方言(`meta({...})` 调用、非 ES module、camelCase opts)——初稿照 Claude Code 语法写的版本在 modu 跑不起来,已按 `tool.go` 的 API 说明重写
- `github-actions-triage.yml`:云端变体,`modu_code -p "/morning-triage" -json --no-approve`,与本机内嵌调度器共享 repo 里的 `state/triage.md`,可并存
- `README.md`:五动作对照表、安装步骤、inbox 约定、Read-a-Sample 纪律、两天连跑验收清单 + Nodding Loop 验尸检查

**验证**:三份模板都过了真实解析器——SKILL.md 经 `pkg/skills` Manager 发现且内容完整、reviewer.md 经 `subagent.ParseDefinition`(tools/max_turns 正确)、workflow 脚本经 goja 按 runtime 同款包裹编译通过。文章 L4 那条"连跑两天"的验收只能实跑:第二天 state/inbox 延续、通知里只出现当天新增条目(检查方法已写进示例 README)。

### 3.5 P1 · 人工入口：inbox 约定与待审通知 — [2026-07-03 完成]

**问题**:人门的三种形态里,通知已有、PR 不 merge 靠 Stop 段约定,但"不确定的进 inbox 等人"原先没有任何呈现——inbox 文件写了也没人知道。

**已落地的实现**(`pkg/cron/notify`):

- `Completion` 签名加 `cwd`(run 的工作目录,daemon 传入);payload 新增 `inbox_new` 和 `pr_links` 两个字段,通知文本追加 `inbox: N new item(s) waiting for you: a.md, b.md` 和 `pr: <url>` 行
- inbox 采集:`<cwd>/inbox/` 下 mtime 在本次 run 开始之后的文件(跳过 dotfile/子目录),按名排序,列最多 10 个、计数是真实总数——**只报当天新增**,昨天的存量不重复轰炸
- PR 链接:正则扫本次 run 的精简日志提取 GitHub PR URL,按首次出现去重,上限 5 条——agent 开了 draft PR,通知里直接可点
- `./inbox/` 目录语义(一事一文件、处理完即删)和 Read-a-Sample 纪律写进 §3.4 示例的 README
- 单测:新增/存量区分(Chtimes 造旧文件)、PR 去重与顺序、空 cwd 不采集(`notify_test.go` 新增 2 例)

### 3.6 P2 · MCP connector 支持 — [2026-07-15 完成]

**问题**:视野半径受限于内置工具 + Bash/gh。接 Jira、Linear、Playwright 这类系统目前没有标准姿势。

**已落地的实现**:

- 使用官方 Go SDK 实现 stdio 与 Streamable HTTP MCP client，负责初始化握手、工具分页发现、工具调用、timeout 和连接关闭；不混用已废弃的 legacy HTTP+SSE transport
- 全局 `~/.modu/config.toml` 采用 Codex 兼容的根级 `[mcp_servers.<name>]`；项目 `.coding_agent/settings.json` 使用 `mcpServers`
- stdio 支持 `command`、`args`、`env`、`cwd`；Streamable HTTP 支持 `url`、bearer token env、静态 header 和环境变量 header；两种 transport 共用 `enabled`、`required`、启动/调用 timeout 和工具 allow/deny filter
- 工具以 `mcp__<server>__<tool>` 动态注册进 session 工具目录，主 agent 可直接调用；workflow/subagent child 通过现有 `tools` allowlist 显式转发
- `/doctor` 显示已连接 server/tool 数量并报告 optional server 启动失败；required server 失败会阻止 session 启动
- 当前验收边界是两种标准 transport 上的 tools；OAuth login、resources、prompts 和动态 `list_changed` 独立后续排期

## 4. 落地顺序与自检

实施顺序(每步都留在"能跑"状态,遵循文章"先证明它能停掉一个坏 agent,才赢得跑更多 agent 的权利"):

1. `[完成]` §3.2 cron caps：先接入断路器，再执行后续实验（2026-07-03）。
2. `[完成]` §3.1 goal verifier：补上独立拒绝机制（2026-07-03）。
3. `[完成]` §3.3 reviewer 模板：`examples/agents/reviewer.md` 与 goal verifier 共用定义（2026-07-03）。
4. `[完成]` §3.4 morning-triage 示例与 §3.5 inbox 通知：五个动作已串联；连续两天验收仍需按示例 README 实跑（2026-07-03）。
5. `[完成]` §3.6 MCP：stdio、Streamable HTTP tool client、session 动态注册和 child 转发已完成（2026-07-15）。

完成后对照文章 First-Loop Checklist 六问自检:

- Discovery source：skill 按 timer 读取 CI、issues、commits 和 inbox，见 `examples/loops/morning-triage`。
- State file：`./state/triage.md` 保存跨轮状态，由 skill 的 Write 阶段提交回仓库。
- Evaluator：在 `extensions.yaml` 启用 `verifier.enabled`，并可通过 `reviewer.md` 定制独立检查。
- Isolation：`triage-fixes` 为每个 finding 创建独立 worktree。
- Token cap：任务级三档限制与 `daily_budget_tokens` 共同终止失控任务。
- Human review：draft PR 不自动合并；新增 inbox 条目和 PR 链接进入完成通知；verifier 连续拒绝后转为 paused。

一句话总结:**P0 + P1 + stdio/Streamable HTTP MCP tools 已落地——goal 有 fresh-model 裁判(可用 reviewer.md 定制),cron 有三档断路器 + 日额度 + 人门通知,morning-triage 示例把五动作端到端串齐；剩余的是 MCP OAuth/resources/prompts 等扩展，以及只能实跑的"连跑两天"验收。**

## 5. 测试方案

分两层:自动化测试(每次改动都跑,已全绿)和真机 E2E(需要真实 model,按阶段手工走一遍)。

### 5.1 自动化测试(已有,随手可跑)

```
go test ./pkg/cron/... ./pkg/coding_agent/plugins/extension/goal/ ./pkg/coding_agent/plugins/extension/ ./pkg/coding_agent/plugins/extension/cron/
```

覆盖对照:

- §3.1 goal verifier:`goal/verifier_test.go`(12 例)——REJECT 后 goal 保持 active 且错误带逐条理由、连续驳回转 paused、resume 清零计数、PASS 完成、ForkSession 故障 fail-open、verdict 不可解析按 REJECT 处理、禁用时不 fork、**reviewer.md 定义被采用/契约重附/model 优先级/工具沙箱不变**
- §3.2 三档 cap:`cron/config/config_test.go`(EffectiveTimeout 回退、ValidateCaps、字段 round-trip)、`cron/runlog/usage_test.go`(日台账累加/隔天独立/31 天裁剪)、`cron/runner/runner_test.go`(超日额度拒跑且 run_end 可区分、重试只发生在 status=error、timeout/token/budget/goal 断路器状态不重试、ctx 取消停止重试)
- §3.3 模板可解析:三份模板都过真实解析器——SKILL.md 过 `pkg/skills` Manager、reviewer.md 过 `subagent.ParseDefinition`、workflow 脚本过 goja(runtime 同款包裹)。这一步在交付时验证过;若改模板,用同样方式回归
- §3.5 inbox 人门:`cron/notify/notify_test.go`——本次 run 新增/历史存量按 mtime 区分、PR 链接去重保序、空 cwd 不采集
- 调度骨架:`cron/daemon_test.go`(热加载换新调度器、坏 config/坏 cron 表达式回滚保留旧调度器)、`cron/scheduler/scheduler_test.go`(skip/queue/kill 并发策略)

### 5.2 TUI 亲手验证 playbook(全程说话,不写 shell)

原则:你的操作面只有两样——在 TUI 里说话,和在**另一个终端**看两类观察点(`tail -f ~/.modu/cron/daemon.log`、`~/.modu/cron/logs/<task>/` 里的 NDJSON)。每个 case 都写明:说什么 → 看什么 → 怎么判定。

**Case 0 · 准备(一次,约 2 分钟)**

1. **停掉所有已在跑的 modu_code**(`ps aux | grep modu_code`;调度器嵌在每个 TUI 进程里,两个实例会把同一份任务表各跑一遍,双倍执行双倍花钱)。测试期间全程只保留你手上这一个。
2. `go install ./cmd/modu_code`——**必须重装**,goal 字段是 07-04 才进的,旧二进制没有。
3. 确认 `~/.modu/extensions.yaml` 有 goal verifier(增量覆盖语义,只写 goal 一项即可):`extensions: [{name: goal, config: {verifier: {enabled: true}}}]`。
4. 另开终端:`tail -f ~/.modu/cron/daemon.log`。启动 `modu_code`(在 modu 仓库目录),daemon.log 应出现 `loaded 2 task(s)` + `cron scheduler started`。
5. 背景事实:周末测试不受真实任务干扰(两个真实任务都是工作日 cron);测试消耗计入 `daily_budget_tokens`(量很小);working_dir 是仓库根,临时 state 文件会落在仓库里,最后清掉。

**Case 1 · 说话建任务 + 热加载(约 1 分钟)**

- 说:"现在有哪些 cron 任务?" → agent 调 `cron_list`,列出 morning-market-daily 和 morning-triage。
- 说:"加一个测试任务 loop-smoke:每 30 秒跑一次,goal 是 state/loop-smoke.md 存在且最后一行是本次运行的时间戳,prompt 是'读 state/loop-smoke.md(它是你之前几轮的记忆),用 date 追加一行当前时间戳,然后读回确认',超时 3 分钟,单次最多 15 万 token,不要通知"。
- 看:daemon.log 出现 `config file changed, reloading...` → `reloaded: 3 task(s)`。
- 判定：通过。`cron_add` 支持 goal 和 timezone；Agent 也可直接编辑 `task.yaml` 补充三档限制，热加载会接收两种修改。

**Case 2 · goal loop 核心链路(等 2-3 轮,约 2 分钟)**

- 什么都不用说,等。每 ~30 秒一轮。
- 看(另一终端):`ls ~/.modu/cron/logs/loop-smoke/`;最新文件第一行应含 `"trigger":"scheduler"`、`"has_goal":true`、`"goal_verifier":true`;最后一行应是 `"status":"ok"` + `"goal_status":"complete"`;仓库里 `state/loop-smoke.md` 每轮多一行。
- 判定：Scheduling → Goal → Verifier → State 全链路通过，并能将本轮输出作为下一轮输入。参考值：单轮 12～22 秒，约 7 万 Token（含 verifier）。

**Case 3 · verifier 会说不(决定性,约 1 分钟)**

- 说:"测试一下 goal 裁判:用 create_goal 建一个 goal,objective 是'文件 /tmp/goal-reject-tui.txt 存在且内容为 hello'。然后**不要做任何工作**(故意的),直接调 update_goal 标记完成,把工具返回的原文给我看。"
- 看:工具返回 `update_goal rejected by the independent goal verifier (reject 1/3)... 文件不存在`;通知区出现 `goal: verifier REJECT (1/3)`。
- 加分:让它再谎报两次 → 第 3 次后 goal 转 paused + 通知(人门)。
- 收尾:说"清掉这个 goal"。
- 判定：maker-checker 会驳回不成立的完成声明，并返回具体理由；evaluator 已在真实用例中至少拒绝过一次。

**Case 4 · 三档断路器(约 5 分钟;4a/4b 可同时挂,4c 最后做)**

4a timeout——在 TUI 里逐字说:

> 加一个测试任务 cap-timeout:每 30 秒跑一次,timeout 15 秒,不要通知。prompt 是:用 bash 工具在前台(background=false)运行 `python3 -c "import time; time.sleep(90); print('done')"`,你需要它的输出,等它打印 done 再回复。

(python 的 sleep 不会被 bash 工具的防呆拦——它只拦以 `sleep` 开头的 shell 命令;prompt 里必须强调前台+要输出,否则 agent 会丢后台绕过去。)等约 45 秒,观察终端跑:

```
tail -1 "$(ls -t ~/.modu/cron/logs/cap-timeout/*.log | head -1)"
```

判定:`"status":"timeout"`、`duration_ms` ≈ 15000、error 是 `run timed out after 15s`。

4b token cap——说:

> 加一个测试任务 cap-token:每 30 秒跑一次,单次 token 上限 3000,不要通知。prompt 是:逐个读取 pkg/ 目录下的 Go 文件并写非常详细的总结。

等约 45 秒,同样 tail 最新日志。判定:`"status":"token_cap"`,error 形如 `per-run token cap reached: 10372 tokens >= cap 2000`(system prompt 本身就超 3000,第一条回复结束即切断)。

4c 日额度(会拦住**所有**任务,所以放最后、立刻恢复)——说:

> 把 cron 配置里的 daily_budget_tokens 临时改成 1000

等下一轮任何测试任务触发,tail 它的最新日志。判定:`"status":"budget_exceeded"`、`duration_ms:0`、整个文件只有 run_start/run_end 两行(session 都没建)。**马上说**:

> 把 daily_budget_tokens 改回 3000000

三档验完顺手说"删掉 cap-timeout 和 cap-token"。附加判定:三种断路器状态的下一行日志不会出现重试(max_retries 只对 status=error 生效)。

**Case 5 · 人门:通知 + inbox(等两轮,约 2 分钟)**

前提:loop-smoke 还在(没有就按 Case 1 再建)。说:

> 修改 loop-smoke 任务:加上通知渠道 feishu-daily;prompt 里追加一句——如果 ./inbox/tui-test.md 不存在,就创建它,内容是"测试:这件事需要人决定";如果已经存在,不要动它。

(**"不存在才创建"是关键**:通知按 mtime 判定"本轮新增",prompt 若每轮重写该文件,它每轮都算新增,就测不出去重了。)

- 第 1 轮后看飞书:消息应包含 task/status/耗时/摘要,以及 `inbox: 1 new item(s) waiting for you: tui-test.md`。
- 什么都不做,等第 2 轮:新的飞书消息**不应再有** inbox 那行(文件还在磁盘上,但不是本轮新增)。
- 判定：待审项会送达人工入口，并且只报告当天新增内容。

**Case 6 · session 按 job id 关联(半分钟)**

- TUI 里输入 `/sessions`(注意带 s;`/session` 是另一个命令)→ 列表里应出现名为 **cron:loop-smoke** 的会话(每轮 tick 一条,名字相同)。
- 想看内容:`/resume <id 或唯一前缀>`(文件路径也行)切进去——**屏幕会立刻回放目标会话的完整历史**(与启动时 `--resume` 相同的渲染):goal 创建、agent 干活、`update_goal` 被 verifier 放行/驳回的现场。看完用 `/resume <原会话id>` 切回来(自己的会话也在 `/sessions` 列表里)。
- 传错 id 会明确报错且不切换(旧版会静默"切"到一个空会话——已修)。
- 判定：每次 cron run 都能按任务 ID 找到并完整回放 session。

**Case 7 · 清理 + 收尾**

- 说:"删掉 loop-smoke、cap-timeout、cap-token 三个任务,把 state/loop-smoke.md 和 inbox/tui-test.md 也删掉"。
- 看:daemon.log `reloaded: 2 task(s)`;仓库干净。

**Case 8 · 自然两天验收(不用操作,周一之后看)**

- 前提:一直挂着**一个** modu_code(tmux/screen)。
- 周一 06:00 / 10:20 自然触发后看:日志第一行 `"trigger":"scheduler"`(不是 manual)、末行 `goal_status:"complete"`;`state/triage.md` 延续上周状态、已完成 finding 不回退;飞书通知只报当天新增 inbox。连续两个工作日满足即为最终验收(标准见 §2.1 补充)。

### 5.3 真机执行记录(2026-07-03,deepseek-v4-flash)

用无头 harness 直接驱动 `pkg/cron.RunScheduler` + `modu_code -p`,以上阶段已实跑一遍:

- **调度器**：3 个任务并发 tick，fsnotify 热加载，75 秒内干净退出；运行日志写入 `~/.modu/cron/logs/`。
- **Token 上限**：返回 `status:"token_cap"` 和 `10372 tokens >= cap 2000`，能在每轮精确切断。
- **超时**：返回 `status:"timeout"`，15 秒时切断。不能用 `sleep` 构造慢任务：Agent 会将它放到后台，bash 工具也会拒绝前台运行 2 秒以上的 sleep；测试需改用真实计算负载。
- **日额度**：返回 `status:"budget_exceeded"`，0 毫秒拒绝执行，不创建 session，并向 channel 发送告警。
- **人工入口**：运行中写入 `./inbox/test-item.md` 后，飞书完成通知包含 `inbox: 1 new item(s)`。
- **goal verifier PASS**：写文件的真实目标完成后，`update_goal` 通过。
- **goal verifier REJECT**：让 Agent 在未创建文件时声明完成，`update_goal` 返回 `rejected by the independent goal verifier (reject 1/3)`，理由明确指出文件不存在。
- **cron `goal:` smoke（2026-07-03）**：真实 `runner.Execute` 先创建 goal，Agent 在临时目录写入 `smoke.txt`，verifier 通过后记录 `run_end status=ok`。同一任务设置 `max_tokens_per_run=30000` 后返回 `status=token_cap`。Token 订阅覆盖 hidden continuation，但 slim NDJSON 对后续 hidden turn 的可观测性仍需加强；验收以 `run_end status`、goal 状态和完整 session 为准。
- **morning-triage day-1 bootstrap(2026-07-03 本仓库实跑)**:已把 `examples/loops/morning-triage` 装到本仓库 `.coding_agent/` 和 `~/.modu/cron/task.yaml`,手动跑 `go run ./cmd/modu_code -p "/morning-triage" -json`;agent 扫 CI/issues/commits 后生成并提交 `state/triage.md`,提交为 `84cd1f3 triage: 2026-07-03`,完整 session 为 `~/.modu/sessions/--Users-ityike-Code-go-src-github.com-openmodu-modu--/b3c3a69b-18ed-437e-b295-46cf71ffa357.jsonl`。这只证明 day-1 loop 行为和 state 写入,还不替代两天 cron 验收;第二天需看 `~/.modu/cron/logs/morning-triage/` 的 ok log 以及 `state/triage.md` 是否读回昨天状态。
- **morning-triage day-2 cron runner(2026-07-04 本仓库实跑)**:用真实 `runner.Execute` 执行同一 `~/.modu/cron/task.yaml` 里的 `morning-triage` 任务(不改工作日 cron 表;7 月 4 日是周六,自然 cron 不会触发),runner 先创建 `goal:` 再跑 `/morning-triage`;`state/triage.md` 读回 2026-07-03 状态并追加 2026-07-04 行,提交为 `32bab63 triage: 2026-07-04`,cron slim log 为 `~/.modu/cron/logs/morning-triage/2026-07-04T00-05-49.155932000+08-00.log`,最后一行为 `run_end status=ok tokens=115149`,完整 session 为 `~/.modu/sessions/--Users-ityike-Code-go-src-github.com-openmodu-modu--/0edac18e-498d-4fa9-aa69-ffd7ad81ef5c.jsonl`。day-1 手动 + day-2 runner 的状态延续已由上述 commit 与 state/triage.md 证明;更硬的"两条自然 cron log"仍需两个工作日常驻 `modu_code`。
- **loop smoke fast canary(2026-07-04 本仓库实跑)**:为快速证明 Scheduling→Goal→Verifier→State 链路,临时向 live `~/.modu/cron/task.yaml` 加入 `loop-smoke-fast`(`*/30 * * * * *`,无通知,不提交),目标是写 `state/loop-smoke.md`、读回确认、`update_goal(status=complete)`。第一轮因 `max_tokens_per_run=50000` 触发 `token_cap`,证明 cap 生效;调到 200000 后,真实 scheduler tick `2026-07-04T10-21-30.022243000+08-00.log` 跑通:`trigger=scheduler`、`timezone=Asia/Shanghai`、`has_goal=true`、`goal_verifier=true`、写入并读回 `2026-07-04 10:21:32 CST`、`run_end status=ok goal_status=complete`,完整 session 为 `~/.modu/sessions/--Users-ityike-Code-go-src-github.com-openmodu-modu--/97530e33-ac05-4273-809d-e2d42b697641.jsonl`。随后已删除临时 task 和 `state/loop-smoke.md`,scheduler reload 回 2 个正式任务。该 canary 证明链路可快速自醒并由 verifier 完成,但不替代 morning-triage 两工作日验收或交易日 A 股 watchlist 验收。**这也是今后推荐的快速功能验证法**(简单、短、高频,不占用真实任务):在 modu_code 里说一句加个 30 秒一轮、goal 为"state 文件末行是本轮时间戳"的临时任务,看两三轮 `run_end goal_status=complete` + state 文件逐轮增长即可,验完删掉——做法已写进示例 README「快速功能验证」一节。
- **triage-fixes empty-queue smoke(2026-07-04 本仓库实跑)**:用 `go run ./cmd/modu_code -p "Use the workflow tool to run the saved project workflow named triage-fixes..." -json --no-approve` 验证 saved workflow 入口可用。完整 session 为 `~/.modu/sessions/--Users-ityike-Code-go-src-github.com-openmodu-modu--/4f148c1d-023f-4f08-8051-f30a08f77b89.jsonl`;workflow run 为 `20260704T023636.845901000Z`,Load 阶段读 `state/triage.md` 后发现 0 个 open finding,因此 Fix/Review/Deliver 全部跳过,结果 `{"fixed":[],"rejected":[]}`。这证明 workflow 入口没坏,但不替代真实 open finding 下的 reviewer/draft PR 证据。
- **triage-fixes open-finding attempt(2026-07-04 本仓库实跑)**:发现并登记了一个真实 P1 finding:`modu_code -p "/workflow:triage-fixes" -json --no-approve` 会启动 saved workflow background run 后退出,留下 `20260704T023616.623643000Z/status.json` 为 `running` 且无 snapshot。随后用 foreground workflow tool 跑 `triage-fixes`;workflow run `20260704T024247.907100000Z` 的 Load 阶段读到 1 个 open finding,Fix 阶段在隔离 worktree `/Users/ityike/.modu/worktrees/73c7f05e-f8b9-42c5-b238-12b5d2717371/modu` 写出修复草稿,但 7 分钟无新事件后被人工中断,结果为 `workflow: context canceled`,Fix agent 记为 skipped/aborted。之后人工审阅草稿、跑聚焦测试和 `go test ./...`,推送 `fix/print-mode-workflow-exits-early`,并创建 draft PR https://github.com/openmodu/modu/pull/75,`state/triage.md` 已更新为 `pr-open`。这证明已有 draft PR 等人审,但不是 triage-fixes Deliver 阶段自动创建的 PR,所以 strict live draft-PR gate 仍会要求后续补齐 workflow session 里的 reviewer + `gh pr create --draft` 证据。
- **triage-fixes 合约(2026-07-04)**:workflow 本体(`examples/loops/morning-triage/triage-fixes.workflow.js`)固化了这些约束——worktree 隔离、adversarial reviewer、结构化 verdict、PASS 路径先 `git push -u origin fix/<slug>` 再 `gh pr create --draft` 非交互开 PR 并把 URL 写回 `state/triage.md` 的 `pr-open` 行、REJECT 写 inbox、批量上限 3;完成通知会把新 inbox 条目和 PR 链接带给人审。
- **triage-fixes Load 收敛(2026-07-04)**:复盘 `20260704T024247.907100000Z` 的 open-finding workflow 证据时发现,Load 阶段的唯一职责本应是解析 `state/triage.md`,但 agent 读完 state 后继续 grep/read 了大量源码,单 Load 估算 871k token,把一个小队列检查膨胀成大任务。已把 Load prompt 收紧为"只读 `state/triage.md`,不得扫源码/git/CI/文件系统",并把 Load agent 工具收窄为 `['read']`;contract verifier 现在也检查这条约束。随后重新跑 empty-queue smoke,workflow run `20260704T030434.979690000Z` 的 Load 只调用一次 `read ./state/triage.md`,估算 3098 tokens,结果仍为 `{"fixed":[],"rejected":[]}`;empty-smoke verifier 现在指向这份新证据并拒绝 Load 阶段调用 `grep`/`ls`/`find`/`bash`。这不替代 reviewer/draft PR live 证据,但消除了下一次跑真实 finding 时的明显 token 膨胀点。
- **triage-fixes Load finding 的 Fix/Review/PR(2026-07-04)**:把上面的 Load 膨胀登记为 `state/triage.md` 的真实 open finding 后,重新跑 foreground `triage-fixes`;workflow run `20260704T030800.639159000Z` 的 Load 只读 `state/triage.md`,Fix agent 产出分支 `fix/load-phase-over-explores`,Review agent 执行检查并给出 `PASS`。run 进入 Deliver 阶段后因人工中断返回 `workflow: context canceled`,所以没有由 Deliver agent 完成 `gh pr create --draft` 和 state 更新。之后人工把 workflow 产出的分支推送并创建 draft PR https://github.com/openmodu/modu/pull/76,`state/triage.md` 记录为 `pr-open`。这证明又有一个真实 reviewer PASS + draft PR 等人审,但 strict live draft-PR gate 仍可继续要求"同一个 workflow session 中包含 reviewer、Deliver 的 `gh pr create --draft`、state PR URL 写回"。
- **triage-fixes Deliver finding 的 Fix/Review/PR(2026-07-04)**:把 Deliver 非交互缺口登记为 `state/triage.md` 的真实 open finding 后,再次跑 foreground `triage-fixes`;workflow run `20260704T032025.510037000Z` 的 Load 只读 state,Fix agent 产出分支 `fix/deliver-phase-prompt`,Review agent 执行合约检查并给出 `PASS`,Deliver agent 随后执行 `git push -u origin fix/deliver-phase-prompt`,再执行 `gh pr create --draft --base feat/loop --head fix/deliver-phase-prompt`,GitHub 返回 draft PR https://github.com/openmodu/modu/pull/77,并把同一个 URL 写回 `state/triage.md` 后提交 `d278fb84 triage: update finding to pr-open with draft PR #77`。这补齐了同一 workflow session 内 reviewer → draft PR → state persistence 的完整 live 证据(PR #77)。
- **A 股 watchlist loop 改造(2026-07-04)**:`examples/workflows/a-stock-daily-report.js`、`examples/loops/morning-triage/tasks.yaml` 和真实 `~/.modu/cron/task.yaml` 的 `morning-market-daily` 已要求先读 `state/watchlist.md`,输出上一轮复盘,结束前完整写回 `state/watchlist.md`(题材/标的/明日验证点/移出项),并在写完后读回确认标题、最新日期、题材表、标的表和已移出观察表存在;workflow 日期锚点改为 Asia/Shanghai,避免 UTC 日期污染状态文件。workflow 数据采集已从 4 路并发改为串行,并要求优先 bash 直连腾讯/同花顺/东财公开 API,web_search/web_fetch 只作补充;东财请求必须串行限流,避免违反 `a-stock-data` 的防封约束。周末运行只读取上一轮 watchlist 并直接返回,不写 `state/watchlist.md`,避免把非交易日空数据写成有效观察。交易日验收标准(人工核对):非周末的 watchlist 日期能对上一条 scheduler-triggered、`goal_status=complete` 的 `morning-market-daily` log 和完整 `cron:morning-market-daily` session,session 里有 `state/watchlist.md` 读写和写后读回确认。当前没有创建真实 `state/watchlist.md`,因为 2026-07-04 是周六非早盘,不把无效行情实盘伪装成验收。
- **scheduler armed(2026-07-04)**:用 detached tmux 会话 `modu-loop-cron` 挂着本仓库 `modu_code`,daemon log 显示 `loaded 2 task(s)` 与 `cron scheduler started`——自然 cron 有机会触发的前提就是这个会话活着。注意两个人工检查点:改了 Go 代码要重启这个会话(否则自然 tick 跑旧代码);改了 task prompt 看一眼 daemon.log 有没有 `config file changed, reloading`。
- **验收脚本已整体移除(2026-07-05,按用户决定)**:07-04 期间 examples 下累积了 17 个 verify-*.sh(readiness/next-run/canary/contract/live/fixtures/final gate)。这违背了 modu_code 的使用形态——用户的操作面应该是"跟 modu_code 说话",不是维护一套 shell 验收面;而且脚本本身变成了需要 fixtures 自测的第二套系统(loop 在给自己的检查器写检查器,正是文章警告的过度建设)。已全部删除。验证职责重新划分:代码行为 → Go 测试(`go test ./pkg/cron/... ./pkg/.../goal/`);链路连通 → 示例 README「快速功能验证」的短高频临时任务(几分钟,聊天建/聊天删);长期质量 → 上面那些人工可核对的日志/state/commit 证据标准 + Read-a-Sample 纪律。
- **顺手抓到并修掉一个真 bug**:并发 tick 各自调 `provider.Resolve()` 写全局 `providers.Models` map → fatal concurrent map writes(commit 5910069 加锁修复)。另发现一个**预存在**的流式管道 data race(openai `readSSE` 写 in-flight message vs `pkg/agent.collectAssistantMessage` 读,任何流式会话都有,与 loop 改动无关)——待独立修复
- **`goal:` 字段快速验证（2026-07-05，deepseek-v4-flash）**：按示例 README 的方法，在隔离配置中运行 `loop-smoke`（`@every 20s`，goal 为“`state/loop-smoke.md` 末行是本轮时间戳”）。100 秒内执行 4 轮；每轮 `run_start` 包含 `has_goal:true` 和 `goal_verifier:true`，`run_end` 为 `status:"ok"`、`goal_status:"complete"`；state 文件每轮增加一行。单轮耗时 12～22 秒，约 7 万 Token（含 verifier）。这个短任务能在几分钟内验证 Scheduling → Goal → Verifier → State，不影响正式任务。
