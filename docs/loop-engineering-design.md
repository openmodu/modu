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

补充(2026-07-03 追加):cron 与 coding_agent 已打通双向——① `config`/`crontools`/`scheduler` 提升到 `pkg/cron/`,新增 builtin `cron` 扩展,modu_code 会话内可直接用 `cron_add`/`cron_list`/`cron_remove` 管理任务表(flock + 原子写防跨进程写坏,daemon fsnotify 热加载接住改动);② modu_cron 每个 tick 的 session 现在加载 builtin 扩展(goal/subagent/workflow),cron 任务可以 `create_goal` 长跑并由 §3.1 的 verifier 裁判完成,extensions.yaml 坏了降级为无扩展继续跑。执行面(常驻调度)仍需要独立 daemon 进程——TUI session 是短命进程,"running while you sleep" 需要常驻;但入口已收敛:cron 的全部实现(cli/runner/runlog/notify)都在 `pkg/cron/`,入口统一为 `modu_code cron <sub>`(daemon/init/list/logs/rm/run),独立的 modu_cron 二进制已移除。cron 自己那份重复的 env-only provider 也删了——`cmd/modu_code/internal/provider` 提升为共享的 `pkg/provider`,cron 任务现在直接用 `modu_code` 当前激活的 model(`~/.modu/config.toml`),不再有第二套模型配置源。CLI 的 `add` 子命令和 cron 专属的 Telegram 入站轮询(`telegram_inbound.go`)也整块删了——两者都是重复实现,`cron` 扩展已经把 cron_add/cron_list/cron_remove 挂进任何 modu_code session,交互式或 modu_code 自带的 Telegram bot(`MOMS_TG_TOKEN`)天然就有自然语言管理能力,不需要 cron 自己再起一套受限 session + 轮询。通知渠道另支持 `feishu_bot` 类型(飞书应用机器人,凭据自动复用 `~/.modu/channels/feishu/config.toml`)。

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
    prompt: "/morning-triage"
    timeout: 45m               # per-run 时长上限,默认 30m;run_end 记 status=timeout
    max_tokens_per_run: 500000 # per-run token 上限(input+output),越线立即 cancel,status=token_cap
    max_retries: 2             # 仅 status=error 时原地重试(退避 30s 起翻倍,上限 5m)

# config.yaml 全局日额度
daily_budget_tokens: 3000000
```

- per-run timeout:`runner.Execute` 内 `context.WithTimeout(task.EffectiveTimeout())`,daemon tick 和手动 `run <id>` 走同一道闸;调度器原来写死的 30m 改为 runner 负责
- per-run token cap:runner `session.Subscribe` 累计 assistant usage(input+output,与 goal budget 同口径),越线 cancel
- daily budget:`~/.modu_cron/logs/usage.json` 按本地时区天聚合(保留 31 天),tick 前检查、超额拒跑(`status=budget_exceeded`,run_start/run_end 照记,channel 照发通知),run 后累加
- max retries:`runner.ExecuteWithRetries`,**只重试 status=error**——timeout/token_cap/budget_exceeded 是断路器,重试它们等于拆保险丝
- `run_end` 新增 `tokens` 字段;`status` 取值 `ok/error/timeout/token_cap/budget_exceeded`;完成通知的 status 同步区分
- cap 配置非法时 `scheduler.Add` 报错 → daemon 热加载失败回滚(与非法 cron 同一套保护)

**验收(已过)**:单测覆盖字段解析/round-trip、EffectiveTimeout 回退、ValidateCaps、台账累加与 31 天裁剪、超日额度拒跑且 run_end 状态可区分、重试只发生在 plain error、ctx 取消停止重试(config/runlog/runner 三包全绿)。留一条实测项:配好真实 model 后,用一个死循环 prompt 验证三档至少一档能打断(見 README「三档 cap」节)。

### 3.3 P1 · Evaluator sub-agent 约定 + 内置模板

**问题**:sub-agent 机制齐了,但仓库里没有一份"reviewer 长什么样"的参考,用户从零写容易漏掉 ASSUME BROKEN / execute-don't-read / VERDICT 三要素。

**方案**:零核心代码,交付约定和模板。

- 在 agents 目录约定下提供内置 `reviewer` 模板(随 `modu_code config` 初始化或文档提供),内容对齐文章 §V-D:ROLE(adversarial)/ ASSUME(broken until proven)/ CHECK(先跑再读,按项目失败模式排序)/ USE(Bash 跑测试、gh 查 CI)/ VERDICT(PASS 需全项通过,REJECT 必须逐条给理由)
- frontmatter 默认:只读工具 + Bash,建议配 `model` 覆盖(与 generator 不同的模型,"同 model 换 prompt 经常保留盲点")
- 与 §3.1 打通:goal verifier 的默认 system prompt 就用这份模板,一处维护

**验收**:workflow 脚本里 `agent("review ...", {agentType/定义名: "reviewer", schema: VERDICT})` 跑通一次真实 REJECT。

### 3.4 P1 · 示例 loop:morning-triage 参考实现

**问题**:六部件都在,但没有一个把五动作串起来的端到端样例。文章的态度:第一个 loop 要"小到不像一个系统"。

**方案**:仓库交付 `examples/loops/morning-triage/`,一比一落文章附录 A 的完整 first loop:

- `skills/morning-triage/SKILL.md`:Read(gh run list 失败 CI / 24h 新 issue / git log --since=yesterday / 昨天的 state 文件)→ Judge(actionable vs noise,"只留今天值得开 worktree 的")→ Write(追加 `./state/triage.md`,四列:finding/source/priority/status,commit 回仓库)→ Handoff(每条 finding 输出 worktree=fix/slug + goal 停止条件)→ **Stop 段**(Never merge / Never delete / 不确定的写 `./inbox/`)
- `tasks.yaml` 片段:cron 06:00 触发 `/morning-triage`,带 §3.2 三档 cap,通知 channel
- 可选的 workflow 脚本:读 triage.md,每条 finding `agent(..., {isolation:"worktree"})` 起草修复,`reviewer` sub-agent 挑刺,PASS 才 `gh pr create --draft`(永不 merge)
- 云端变体:一份 `.github/workflows/triage.yml`,用 `modu_code -p "/morning-triage" -json` 跑同一个 skill——本机 daemon 和云端 Actions 二选一或并用

**验收**:文章 L4 那条"跑出来才知道"的检查——连跑两天,第二天的 run 能读回第一天的 state 与 inbox,未完成 finding 状态延续,不重做已完成的活。

### 3.5 P1 · 人门收口:inbox 约定 + 通知带上"待人审"清单

**问题**:人门的三种形态里,通知已有、PR 不 merge 靠 Stop 段约定,但"不确定的进 inbox 等人"目前没有任何呈现——inbox 文件写了也没人知道。

**方案**:

- 约定 `./inbox/` 目录语义(每文件一事,markdown),写进 §3.4 的示例与文档
- modu_cron 完成通知追加一段:本次 run 后 `./inbox/` 新增条目数与标题列表、新开 PR 链接——把"门"送到人眼前(telegram/feishu 里直接看到今天有几件事等你)
- 文档写明 Read-a-Sample 纪律:不读全部,每天读一个,解释不出来 = 你的地图掉队了

**改动点**:`pkg/cron/notify` 加一个 run 后置采集钩子;其余是文档。

### 3.6 P2 · MCP connector 支持

**问题**:视野半径受限于内置工具 + Bash/gh。接 Jira、Linear、Playwright 这类系统目前没有标准姿势。

**方案**:coding_agent 增加 MCP client(stdio 起步),配置声明 server,工具动态注册进 session 工具目录;workflow child 通过现有 `tools` 白名单机制显式转发。范围大,独立立项,不阻塞前面任何一项——GitHub 生态用 `gh` CLI 已经够第一个 loop 用。

## 4. 落地顺序与自检

实施顺序(每步都留在"能跑"状态,遵循文章"先证明它能停掉一个坏 agent,才赢得跑更多 agent 的权利"):

1. ✅ §3.2 cron caps —— 先装断路器,后面所有实验都有保险丝(2026-07-03 完成)
2. ✅ §3.1 goal verifier —— 补上 say no 的门,loop 从此有 Verification 这一动(2026-07-03 完成)
3. §3.3 reviewer 模板(可直接复用 verifier.go 里的 adversarial prompt)
4. §3.4 morning-triage 示例 + §3.5 inbox 通知 —— 五动作端到端串起来,连跑两天验收
5. §3.6 MCP —— 独立排期

完成后对照文章 First-Loop Checklist 六问自检:

- Discovery source:skill 按 timer 读 CI/issues/commits/inbox —— §3.4 后 ✅
- State file:`./state/triage.md` 磁盘跨轮记忆 —— §3.4 后 ✅
- Evaluator:会 say no 的独立 check —— ✅ 已装(extensions.yaml 开 `verifier.enabled` 后生效)
- Isolation:每个并行 agent 自己的 worktree —— ✅
- Token cap:跑歪谁叫停 —— ✅ 已装(任务三档 + `daily_budget_tokens`)
- Human review:哪一步停下来等人 —— §3.5 后 ✅(通知已有 + verifier 连续驳回转 paused 已是一道人门;inbox 呈现待补)

一句话总结:**两件 P0 已落地——goal 有了 fresh-model 裁判(maker-checker),cron 有了三档断路器 + 日额度。剩下的是 P1 的模板、示例与 inbox 约定,以及 P2 的 MCP。**
