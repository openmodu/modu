# modu_code 实现 Loop Engineering 的设计方案

> 本文回答一个问题:**modu 要成为一个能承载 loop 的 harness,哪些已经有了,哪些还要做。**
> 状态标记:✅ 已完成 · 🟡 部分完成 · ❌ 待做

## 1. 文章核心框架(一页纸)

Loop engineering 是 prompt → context → harness 之上的第四层:人从循环里撤出来,改去设计那个循环。loop 比 harness 多三个动词:**runs on a timer(定时自己醒)、spawns helpers(自己派 sub-agent)、feeds itself(这一轮的产出是下一轮的输入)**。

一次 turn 的五动作,砍掉任何一个就长出对应的 loop 病:

- **Discovery(找活)** — 缺了 = Blind loop,人还在每天派活
- **Handoff(派活+隔离)** — 缺了 = Tangled loop,多个 agent 改同一目录
- **Verification(会 say no 的门)** — 缺了 = Nodding loop,自己给自己点头
- **Persistence(记下)** — 缺了 = Amnesiac loop,每天从头来
- **Scheduling(定下一轮)** — 缺了 = Manual loop,demo 完就停

支撑五动作的六部件:**Automations(调度)、Worktrees(隔离)、Skills(项目知识,还 intent debt)、Connectors(接外部系统)、Sub-agents(generator/evaluator 分离)、Memory(磁盘上的跨轮状态)**。

三条纪律,缺一不可:

- **Cap Before You Ship**:per-run budget / daily budget / max retries 三档断路器,在 loop 第一次自己跑之前装好
- **Keep One Door Open(人门)**:PR 永不 auto-merge、不确定的进 inbox、每天读一个 sample
- **Maker-checker**:停止条件由独立 fresh model 判断,不是 generator 自己说"差不多了"。文章原话:"把一个独立 evaluator 调教成挑剔,比把 generator 调教成自我批判容易得多"

关键区分:`/loop` 是按 interval 重跑(没有停止判断);`/goal` 是跑到条件被**另一个模型**判定为 true 才停。混了这两个,你以为装了 verification,其实只装了 scheduling。

## 2. modu 现状映射:六部件逐项盘点

### 2.1 Automations / Scheduling — ✅ 完成(含断路器)

已完成:

- `pkg/cron`(入口 `modu_code cron`):daemon + CLI 双形态,robfig/cron 六字段表达式,`on_overlap: skip|queue|kill` 并发策略,连续 overlap 告警
- 配置热加载(fsnotify + SIGHUP,失败回滚保留旧调度器)
- `cron_add` / `cron_list` / `cron_remove` 三个 agent 工具,支持自然语言建任务——通过 builtin `cron` 扩展挂进任意 modu_code session(交互式或 modu_code 自带的 Telegram bot),没有 cron 专属的 CLI/bot 实现
- 每个 tick 独立 `CodingSession`,精简 NDJSON 运行日志(`run_end` 保证落盘,可靠的"tick 结束"标记)
- 完成通知 channels:webhook / telegram / feishu_webhook,成败都推送,含状态、耗时、日志路径、最后一段 assistant 文本
- `modu_code -p "<prompt>"` print 模式 + `-json` NDJSON 输出:云端调度(GitHub Actions cron)可以直接用,机器关着也能跑

~~待做~~ ✅ 已补齐(见 §3.2):per-run timeout / token cap / max retries + 全局日额度已实现,cron 任务跑歪时有三档断路器叫停。

补充(2026-07-03 追加):cron 与 coding_agent 已打通双向——① `config`/`crontools`/`scheduler` 提升到 `pkg/cron/`,新增 builtin `cron` 扩展,modu_code 会话内可直接用 `cron_add`/`cron_list`/`cron_remove` 管理任务表(flock + 原子写防跨进程写坏,daemon fsnotify 热加载接住改动);② modu_cron 每个 tick 的 session 现在加载 builtin 扩展(goal/subagent/workflow),cron 任务可以 `create_goal` 长跑并由 §3.1 的 verifier 裁判完成,extensions.yaml 坏了降级为无扩展继续跑。执行面(常驻调度)仍需要独立 daemon 进程——TUI session 是短命进程,"running while you sleep" 需要常驻;再进一步:连独立的 `modu_code cron` 命令/进程都删了——调度器现在**跑在 `modu_code` 正常交互式 TUI 进程里**(`cmd/modu_code/main.go` 启动 TUI 前起一个 goroutine 直接调 `pkg/cron.RunScheduler(ctx, cfgPath)`,ctx 随 TUI 退出而取消;`-p`/`-rpc`/`-acp` 这类一次性或外部客户端驱动的模式不启动它,避免在不预期场合悄悄花 token)。这意味着"loop 自己醒来"现在等价于"你开着一个 `modu_code` 会话"——想要真正人不在也能跑,得让某台机器上一直挂一个 `modu_code`(tmux/screen)。cron 自己那份重复的 env-only provider 删了——`cmd/modu_code/internal/provider` 提升为共享的 `pkg/provider`,cron 任务直接用 `modu_code` 当前激活的 model,不再有第二套模型配置源。CLI 的 `add`/`init`/`list`/`logs`/`rm`/`run` 六个子命令、cron 专属的 Telegram 入站轮询(`telegram_inbound.go`)、以及 `pkg/cron/cli` 这个包本身全删了——task 的增删查没有 CLI 或独立 Telegram bot 存在的必要,`cron` 扩展已经把 cron_add/cron_list/cron_remove 挂进任何 modu_code session(交互式或 modu_code 自带的 Telegram bot),配置文件缺失时调度器也能以零任务起步;`logs` 直接读 `~/.modu/cron/logs/<id>/*.log`(NDJSON,cat/jq 即可)。调度器自身的 log.Printf 输出重定向到 `~/.modu/cron/daemon.log`,不会打进终端糊掉 bubbletea 界面。每次 cron 运行现在还会把完整 session 记录(不是精简 NDJSON,是像交互式会话一样存进 `~/.modu/sessions/` 的那份)按 `cron:<task_id>` 命名,可以在 session 列表/`--resume` 里按任务 id 找到对应的运行历史。通知渠道另支持 `feishu_bot` 类型(飞书应用机器人,凭据自动复用 `~/.modu/channels/feishu/config.toml`)。

补充(2026-07-03 再追加):`tasks.yaml` 现在有一等 `goal:` 字段。cron tick 开始时 runner 直接创建 session goal,再发送 `prompt`;无头 runner 接管 hidden continuation,直到 `update_goal(status=complete)` 被 verifier PASS、goal paused/budgetLimited,或 timeout/token cap 打断。这样 Scheduling 和 Verification 不再靠 prompt 自觉拼接:调度器负责建目标,goal 扩展负责续跑和裁判,cap 负责停止失控 run。2026-07-04 再补一刀可观测性:`run_start` 现在记录 `trigger`、`timezone`、`has_goal`、去空白后的 `goal` 文本和 `goal_verifier`,自然调度器为 `scheduler`,手动 harness/bootstrap 为 `manual`;声明 `goal:` 的任务在 `run_end` 记录 `goal_status`。goal 扩展不可用记为 `status=goal_unavailable`;goal paused / budgetLimited 分别记为 `status=goal_paused` / `status=goal_budget_limited`;三者都作为断路器不触发 `max_retries`,避免 verifier 连续拒绝或配置错误后被 cron 当 transient error 重跑。task 级 `timezone:` 现在会转换为 robfig cron 的 `CRON_TZ=...`,本仓库实跑的 morning-triage 和 A 股任务都 pin 到 `Asia/Shanghai`,避免换机器后 weekday/早盘时间漂移。两工作日自然 cron 验收的硬标准(人工核对日志即可,不需要脚本):两条**不同日期**的 log,`run_start` 为 `trigger:"scheduler"`、`timezone:"Asia/Shanghai"`、`has_goal:true`、`goal_verifier:true`,`run_end` 为 `goal_status:"complete"`;每条 log 的 session_id 能连回名为 `cron:<task_id>` 的完整 session;每条 run 的日期出现在 `state/triage.md` 且同一 finding 不从 closed/done 回退到 open。

### 2.2 Worktrees / Handoff — ✅ 基本完成

已完成:

- `modu_code -worktree`:启动即进隔离 worktree
- `enter_worktree` / `exit_worktree` 工具(`pkg/coding_agent/tools/worktree`):agent 自己能开/退 worktree
- workflow 插件里子 agent 可传 `isolation: "worktree"`,并行 fan-out 物理隔离
- workflow 运行时并发默认 4、钳制上限 16,单次 workflow 最多 fork 1000 child——并行上限(MAX_PARALLEL)这一课已经装好

待做:无硬缺口。"每条 finding 一个 worktree" 是 skill/workflow 脚本层的写法约定,harness 能力已备齐(§3.4 的示例 skill 会演示)。

### 2.3 Skills / Discovery — ✅ 完成

已完成:

- `pkg/skills`:SKILL.md 发现(frontmatter 解析、多路径、ignore 规则),按 Agent Skills 规范以 XML 注入 system prompt
- slash 命令没有内置模板时回落到同名 skill——`modu_code -p "/morning-triage"` 就是 "automation 触发一个 skill,而不是一坨粘进 cron 的 prompt"(文章 L1 的核心要求),cron 任务的 `prompt` 字段写 `/skill-name` 即可

待做:无 harness 缺口。Discovery 的 Read/Judge/Write/Stop 段是每个 loop 自己的 SKILL.md 内容,属于用户侧;§3.4 提供参考实现。

### 2.4 Connectors — 🟡 自有渠道完成,MCP 缺失

已完成:

- `pkg/channels`(feishu / telegram 双实现 + bridge):入站消息、出站通知,modu_cron 的通知就走这套
- web_fetch / web_search 工具(可被 workflow child 显式请求)

待做(见 §3.6):没有 MCP client。文章里 connector 的角色是"决定 loop 的视野半径"(读 Jira、开 Linear ticket、Playwright 点页面)。目前等价能力靠 Bash + `gh` CLI 能覆盖 GitHub 一系(开 PR、读 issue、读 CI),所以 MCP 是 P2 增强而非阻塞项。

### 2.5 Sub-agents / Verification — ✅ 完成(verifier 门已装)

已完成:

- `pkg/coding_agent/plugins/subagent`:markdown 定义 sub-agent(frontmatter 支持 `tools` 白名单、`disallowed_tools`、`model` 覆盖、`isolation`、`max_turns`、`memory_scope`、`permission_mode`),body 即 system prompt——**换一个 model 当 evaluator** 的能力已经在了
- workflow 插件:`agent()` / `parallel()` / `pipeline()`,child 可传 `schema`(JSON Schema 校验返回值,失败重试一次)——结构化 VERDICT 输出的机制已备
- `/goal`:隐藏续跑(agent_end 后自动注入 continuation)、token budget(`StartWithBudget` / `StatusBudgetLimited`)、审计式完成 prompt(要求逐条对照 objective 找证据,禁止拿 proxy signal 当完成)

~~核心缺口~~ ✅ 已补齐(见 §3.1):goal 的完成判定原先是 generator 自判——正是文章批判的 Nodding Loop 结构。现在 `update_goal(status=complete)` 会先过一个独立 fresh-context verifier(maker-checker),REJECT 带理由驳回、连续驳回转 paused 交人。

### 2.6 Memory / Persistence — ✅ 完成

已完成:

- `pkg/coding_agent/services/memory` + `tools/memory`:持久 memory,`memory_scope` 支持 user/project 双域,sub-agent 和 workflow child 都能声明
- session 持久化 + `pkg/runtime` checkpoint journal(事件溯源,可回退/恢复/重入)
- 运行日志落盘(modu_cron NDJSON)

待做:无 harness 缺口。loop 专属的 `./state/triage.md`、`./inbox/` 是仓库文件,由 skill 读写,memory ≠ context 的界线 harness 已经画对(memory 在磁盘,context 每轮重建)。

## 3. 待做项设计(按优先级)

### 3.1 P0 · Goal 独立裁判(maker-checker)——补上"会 say no 的门" ✅ 已实现(2026-07-03)

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

### 3.2 P0 · modu_cron 三档 cap——Cap Before You Ship ✅ 已实现(2026-07-03)

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

### 3.3 P1 · Evaluator sub-agent 约定 + 内置模板 ✅ 已实现(2026-07-03)

**问题**:sub-agent 机制齐了,但仓库里没有一份"reviewer 长什么样"的参考,用户从零写容易漏掉 ASSUME BROKEN / execute-don't-read / VERDICT 三要素。

**已落地的实现**:

- `examples/agents/reviewer.md`:完整模板,对齐文章 §V-D——ROLE(adversarial maker-checker)/ ASSUME(broken until proven)/ CHECK(execute-don't-read,四步)/ USE(bash 跑测试、gh 查 CI)/ VERDICT(PASS 需全项通过,REJECT 必须逐条给理由,末行 JSON)。frontmatter:`tools: read, grep, ls, find, bash`、`max_turns: 12`、注释建议 `model` 覆盖("同 model 换 prompt 经常保留盲点")。装法:`cp examples/agents/reviewer.md ~/.modu/agents/`(或项目 `.coding_agent/agents/`)
- **与 §3.1 真打通**(不止文案层面):goal verifier 在 fork 前会查找名为 `reviewer` 的 subagent 定义——找到就用它的 body 当 system prompt(VERDICT JSON 契约会重新附加,保证解析不坏)、用它的 `model` 当默认模型(显式 verifier config 的 model 优先);工具沙箱**不**从定义取,verifier 永远保持自己的 read+bash 白名单。没有定义时回退内置 prompt,完全向后兼容。一份 `reviewer.md` 同时定制 workflow 里的 review agent 和 goal 裁判——一处维护
- 验收调整:modu 的 workflow `agent()` 没有按定义名解析的选项(那是设计时的假设,实际 API 是 prompt + tools/schema),所以 workflow 侧的 reviewer 用法是同一契约的内联 prompt + `schema: VERDICT`(见 §3.4 的 triage-fixes 脚本);"跑通一次真实 REJECT" 由 goal 侧单测覆盖(自定义 reviewer 定义被采用、契约被附加、model 优先级、沙箱不变,`verifier_test.go` 新增 2 例)

### 3.4 P1 · 示例 loop:morning-triage 参考实现 ✅ 已实现(2026-07-03)

**问题**:六部件都在,但没有一个把五动作串起来的端到端样例。文章的态度:第一个 loop 要"小到不像一个系统"。

**已落地的实现**(`examples/loops/morning-triage/`,一比一落文章附录 A 的完整 first loop):

- `skills/morning-triage/SKILL.md`:Read(gh run list 失败 CI / 24h 新 issue / git log --since / 昨天的 `./state/triage.md` + `./inbox/`)→ Judge(actionable vs noise,"只留今天值得开 worktree 的")→ Write(追加 `./state/triage.md` 五列表格(date/finding/source/priority/status),无 actionable finding 也写一条当日 `No actionable findings`/`closed` heartbeat,commit 回仓库)→ Hand off(每条 finding 输出 `worktree=fix/slug goal=<可验证停止条件>`,本 run 只 triage 不修)→ **Stop 段**(Never merge / Never delete / Never push main / 不确定的一事一文件写 `./inbox/`)
- `tasks.yaml`:工作日 06:00 触发 `/morning-triage`,三档 cap + `daily_budget_tokens` + feishu_bot channel 全配好(Cap Before You Ship)
- `triage-fixes.workflow.js`:Load(只读 `state/triage.md` 解析 open findings,schema 校验,不在 Load 阶段扫源码/git/CI)→ Fix(每条 finding `isolation:'worktree'` 起草,批量上限 3——review 带宽即天花板)→ Review(对抗式 reviewer,同 `examples/agents/reviewer.md` 契约,`schema: VERDICT`)→ Deliver(PASS 才 `gh pr create --draft` 永不 merge;REJECT 写 `./inbox/<slug>.md`)。用 modu 的真实 workflow 方言(`meta({...})` 调用、非 ES module、camelCase opts)——初稿照 Claude Code 语法写的版本在 modu 跑不起来,已按 `tool.go` 的 API 说明重写
- `github-actions-triage.yml`:云端变体,`modu_code -p "/morning-triage" -json --no-approve`,与本机内嵌调度器共享 repo 里的 `state/triage.md`,可并存
- `README.md`:五动作对照表、安装步骤、inbox 约定、Read-a-Sample 纪律、两天连跑验收清单 + Nodding Loop 验尸检查

**验证**:三份模板都过了真实解析器——SKILL.md 经 `pkg/skills` Manager 发现且内容完整、reviewer.md 经 `subagent.ParseDefinition`(tools/max_turns 正确)、workflow 脚本经 goja 按 runtime 同款包裹编译通过。文章 L4 那条"连跑两天"的验收只能实跑:第二天 state/inbox 延续、通知里只出现当天新增条目(检查方法已写进示例 README)。

### 3.5 P1 · 人门收口:inbox 约定 + 通知带上"待人审"清单 ✅ 已实现(2026-07-03)

**问题**:人门的三种形态里,通知已有、PR 不 merge 靠 Stop 段约定,但"不确定的进 inbox 等人"原先没有任何呈现——inbox 文件写了也没人知道。

**已落地的实现**(`pkg/cron/notify`):

- `Completion` 签名加 `cwd`(run 的工作目录,daemon 传入);payload 新增 `inbox_new` 和 `pr_links` 两个字段,通知文本追加 `inbox: N new item(s) waiting for you: a.md, b.md` 和 `pr: <url>` 行
- inbox 采集:`<cwd>/inbox/` 下 mtime 在本次 run 开始之后的文件(跳过 dotfile/子目录),按名排序,列最多 10 个、计数是真实总数——**只报当天新增**,昨天的存量不重复轰炸
- PR 链接:正则扫本次 run 的精简日志提取 GitHub PR URL,按首次出现去重,上限 5 条——agent 开了 draft PR,通知里直接可点
- `./inbox/` 目录语义(一事一文件、处理完即删)和 Read-a-Sample 纪律写进 §3.4 示例的 README
- 单测:新增/存量区分(Chtimes 造旧文件)、PR 去重与顺序、空 cwd 不采集(`notify_test.go` 新增 2 例)

### 3.6 P2 · MCP connector 支持

**问题**:视野半径受限于内置工具 + Bash/gh。接 Jira、Linear、Playwright 这类系统目前没有标准姿势。

**方案**:coding_agent 增加 MCP client(stdio 起步),配置声明 server,工具动态注册进 session 工具目录;workflow child 通过现有 `tools` 白名单机制显式转发。范围大,独立立项,不阻塞前面任何一项——GitHub 生态用 `gh` CLI 已经够第一个 loop 用。

## 4. 落地顺序与自检

实施顺序(每步都留在"能跑"状态,遵循文章"先证明它能停掉一个坏 agent,才赢得跑更多 agent 的权利"):

1. ✅ §3.2 cron caps —— 先装断路器,后面所有实验都有保险丝(2026-07-03 完成)
2. ✅ §3.1 goal verifier —— 补上 say no 的门,loop 从此有 Verification 这一动(2026-07-03 完成)
3. ✅ §3.3 reviewer 模板 —— `examples/agents/reviewer.md`,goal verifier 自动采用同名定义,一处维护(2026-07-03 完成)
4. ✅ §3.4 morning-triage 示例 + §3.5 inbox 通知 —— 五动作端到端串起来(2026-07-03 完成;连跑两天的验收只能实跑,清单在示例 README)
5. §3.6 MCP —— 独立排期

完成后对照文章 First-Loop Checklist 六问自检:

- Discovery source:skill 按 timer 读 CI/issues/commits/inbox —— ✅(`examples/loops/morning-triage`)
- State file:`./state/triage.md` 磁盘跨轮记忆 —— ✅(skill 的 Write 段,commit 回 repo)
- Evaluator:会 say no 的独立 check —— ✅(extensions.yaml 开 `verifier.enabled`;reviewer.md 可定制)
- Isolation:每个并行 agent 自己的 worktree —— ✅(triage-fixes 每 finding 一个 worktree)
- Token cap:跑歪谁叫停 —— ✅(任务三档 + `daily_budget_tokens`)
- Human review:哪一步停下来等人 —— ✅(draft PR 永不 merge;inbox 新增条目和 PR 链接直接进完成通知;verifier 连续驳回转 paused)

一句话总结:**P0 + P1 全部落地——goal 有 fresh-model 裁判(可用 reviewer.md 定制),cron 有三档断路器 + 日额度 + 人门通知,morning-triage 示例把五动作端到端串齐。唯一剩下的是 P2 的 MCP connector(独立排期),以及只能实跑的"连跑两天"验收。**

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
- 判定:说话建任务 + 热加载 ✅。(`cron_add` 支持 goal/timezone;cap 三件套 agent 会用 Edit 直接补进 task.yaml——也是合法路径,热加载同样接住。)

**Case 2 · goal loop 核心链路(等 2-3 轮,约 2 分钟)**

- 什么都不用说,等。每 ~30 秒一轮。
- 看(另一终端):`ls ~/.modu/cron/logs/loop-smoke/`;最新文件第一行应含 `"trigger":"scheduler"`、`"has_goal":true`、`"goal_verifier":true`;最后一行应是 `"status":"ok"` + `"goal_status":"complete"`;仓库里 `state/loop-smoke.md` 每轮多一行。
- 判定:Scheduling→Goal→Verifier→State 全链路 ✅,feeds itself ✅。(参考值:单轮 12-22 秒、约 7 万 token 含 verifier。)

**Case 3 · verifier 会说不(决定性,约 1 分钟)**

- 说:"测试一下 goal 裁判:用 create_goal 建一个 goal,objective 是'文件 /tmp/goal-reject-tui.txt 存在且内容为 hello'。然后**不要做任何工作**(故意的),直接调 update_goal 标记完成,把工具返回的原文给我看。"
- 看:工具返回 `update_goal rejected by the independent goal verifier (reject 1/3)... 文件不存在`;通知区出现 `goal: verifier REJECT (1/3)`。
- 加分:让它再谎报两次 → 第 3 次后 goal 转 paused + 通知(人门)。
- 收尾:说"清掉这个 goal"。
- 判定:maker-checker 门真的会驳回,且带具体理由 ✅。这同时满足文章的验尸检查(evaluator 至少真 REJECT 过一次)。

**Case 4 · 三档断路器(每档等一轮,测完即删)**

- timeout:说"加任务 cap-timeout:每 30 秒,prompt 是'前台运行 bash:python3 计一个 90 秒的忙等循环,等它输出',timeout 15 秒"。→ 下一轮日志 `"status":"timeout"`,duration ≈ 15000ms。**注意:不能用 sleep 造慢任务**——agent 会把 sleep 丢后台,bash 工具还自带"前台 sleep≥2s 拒绝"的防呆,必须用真计算。
- token cap:说"加任务 cap-token:每 30 秒,prompt 是'逐个读 pkg/ 下的 go 文件并极其详细地总结',单次上限 3000 token"。→ `"status":"token_cap"`,error 里有实际用量。
- 日额度:说"把 daily_budget_tokens 临时改成 1000"。→ 下一轮任何任务 `"status":"budget_exceeded"`,duration 0ms(连 session 都不建),挂了 channel 的任务飞书收到告警。**立刻说"改回 3000000"**。
- 判定:三档独立生效;三种断路器状态都不触发 max_retries 重试 ✅。

**Case 5 · 人门:通知 + inbox(等两轮)**

- 说:"给 loop-smoke 挂上 feishu-daily,prompt 里加一句:在 ./inbox/ 写一个 tui-test.md 说明有事需要人决定"。
- 看:下一轮飞书通知包含 task/status/耗时/摘要 + `inbox: 1 new item(s) waiting for you: tui-test.md`;**再下一轮**通知不再重复报 tui-test.md(只报当天新增是核心断言)。
- 判定:门被送到人眼前,且不重复轰炸 ✅。

**Case 6 · session 按 job id 关联(半分钟)**

- TUI 里 `/session`(或退出后重开时看列表)→ 出现名为 `cron:loop-smoke` 的会话;打开能看到完整对话,包括 update_goal 被 verifier 拦下/放行的完整现场。
- 判定:每次 cron run 都能按任务 id 找到完整 session ✅。

**Case 7 · 清理 + 收尾**

- 说:"删掉 loop-smoke、cap-timeout、cap-token 三个任务,把 state/loop-smoke.md 和 inbox/tui-test.md 也删掉"。
- 看:daemon.log `reloaded: 2 task(s)`;仓库干净。

**Case 8 · 自然两天验收(不用操作,周一之后看)**

- 前提:一直挂着**一个** modu_code(tmux/screen)。
- 周一 06:00 / 10:20 自然触发后看:日志第一行 `"trigger":"scheduler"`(不是 manual)、末行 `goal_status:"complete"`;`state/triage.md` 延续上周状态、已完成 finding 不回退;飞书通知只报当天新增 inbox。连续两个工作日满足即为最终验收(标准见 §2.1 补充)。

### 5.3 真机执行记录(2026-07-03,deepseek-v4-flash)

用无头 harness 直接驱动 `pkg/cron.RunScheduler` + `modu_code -p`,以上阶段已实跑一遍:

- **调度器**:3 任务并发 tick、fsnotify 热加载、75s 干净退出 ✅;运行日志/台账落 `~/.modu/cron/logs/` ✅
- **token cap**:`status:"token_cap"`,`10372 tokens >= cap 2000`,每轮精确切断 ✅
- **timeout**:`status:"timeout"`,15s 整切断 ✅。注:用 `sleep` 造慢任务测不出来——agent 会把 sleep 丢后台,bash 工具还自带"前台 sleep≥2s 直接拒绝"的防呆,要用真计算(python 忙等)才能撞线
- **日额度**:`status:"budget_exceeded"`,0ms 拒跑、不建 session、channel 收到告警 ✅
- **inbox 人门**:run 内写 `./inbox/test-item.md`,完成通知带 `inbox: 1 new item(s)` ✅(飞书实收)
- **goal verifier PASS**:真目标(写文件)完成,update_goal 通过 ✅
- **goal verifier REJECT(决定性)**:令 agent 谎报完成(不建文件直接 complete),update_goal 被驳回——`rejected by the independent goal verifier (reject 1/3)`,理由精确指出"文件不存在" ✅。maker-checker 门真机工作
- **cron `goal:` smoke(2026-07-03 本分支追加)**:用真实 `runner.Execute` + 当前 active model 跑临时任务,runner 先建 goal,agent 在临时 cwd 写 `smoke.txt`,goal verifier PASS 后 `run_end status=ok`;同一任务在 `max_tokens_per_run=30000` 时先被 `status=token_cap` 切断,把 cap 和 PASS 两条路径都撞了一遍 ✅。注意:token 订阅覆盖 hidden continuation,但 slim NDJSON 的后续 hidden turn 可观测性还需单独加强;验收以 `run_end status`、goal 状态和完整 session 为准。
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
- **`goal:` 字段快速功能验证(2026-07-05,deepseek-v4-flash)**:按 README「快速功能验证」的做法,隔离 scratch 配置跑 `loop-smoke`(`@every 20s`,goal="state/loop-smoke.md 末行是本轮时间戳"):100 秒内 4 轮,每轮 `run_start` 带 `has_goal:true`/`goal_verifier:true`,每轮 `run_end` 为 `status:"ok"` + `goal_status:"complete"`(runner 建 goal → agent 干活 → verifier 独立 PASS),state 文件逐轮各增一行(feeds itself),单轮 12-22 秒、约 7 万 token(含 verifier)。Scheduling→Goal→Verifier→State 全链路用短高频任务几分钟验完,不需要动任何真实任务 ✅
