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
- daily budget:`~/.modu/cron/logs/usage.json` 按本地时区天聚合(保留 31 天),tick 前检查、超额拒跑(`status=budget_exceeded`,run_start/run_end 照记,channel 照发通知),run 后累加
- max retries:`runner.ExecuteWithRetries`,**只重试 status=error**——timeout/token_cap/budget_exceeded 是断路器,重试它们等于拆保险丝
- `run_end` 新增 `tokens` 字段;`status` 取值 `ok/error/timeout/token_cap/budget_exceeded`;完成通知的 status 同步区分
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

- `skills/morning-triage/SKILL.md`:Read(gh run list 失败 CI / 24h 新 issue / git log --since / 昨天的 `./state/triage.md` + `./inbox/`)→ Judge(actionable vs noise,"只留今天值得开 worktree 的")→ Write(追加 `./state/triage.md` 四列表格,commit 回仓库)→ Hand off(每条 finding 输出 `worktree=fix/slug goal=<可验证停止条件>`,本 run 只 triage 不修)→ **Stop 段**(Never merge / Never delete / Never push main / 不确定的一事一文件写 `./inbox/`)
- `tasks.yaml`:工作日 06:00 触发 `/morning-triage`,三档 cap + `daily_budget_tokens` + feishu_bot channel 全配好(Cap Before You Ship)
- `triage-fixes.workflow.js`:Load(读 open findings,schema 校验)→ Fix(每条 finding `isolation:'worktree'` 起草,批量上限 3——review 带宽即天花板)→ Review(对抗式 reviewer,同 `examples/agents/reviewer.md` 契约,`schema: VERDICT`)→ Deliver(PASS 才 `gh pr create --draft` 永不 merge;REJECT 写 `./inbox/<slug>.md`)。用 modu 的真实 workflow 方言(`meta({...})` 调用、非 ES module、camelCase opts)——初稿照 Claude Code 语法写的版本在 modu 跑不起来,已按 `tool.go` 的 API 说明重写
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
- §3.2 三档 cap:`cron/config/config_test.go`(EffectiveTimeout 回退、ValidateCaps、字段 round-trip)、`cron/runlog/usage_test.go`(日台账累加/隔天独立/31 天裁剪)、`cron/runner/runner_test.go`(超日额度拒跑且 run_end 可区分、重试只发生在 status=error、断路器状态不重试、ctx 取消停止重试)
- §3.3 模板可解析:三份模板都过真实解析器——SKILL.md 过 `pkg/skills` Manager、reviewer.md 过 `subagent.ParseDefinition`、workflow 脚本过 goja(runtime 同款包裹)。这一步在交付时验证过;若改模板,用同样方式回归
- §3.5 inbox 人门:`cron/notify/notify_test.go`——本次 run 新增/历史存量按 mtime 区分、PR 链接去重保序、空 cwd 不采集
- 调度骨架:`cron/daemon_test.go`(热加载换新调度器、坏 config/坏 cron 表达式回滚保留旧调度器)、`cron/scheduler/scheduler_test.go`(skip/queue/kill 并发策略)

### 5.2 真机 E2E(分阶段,每阶段独立可跑)

前置(一次性):① `go install ./cmd/modu_code` 重装二进制(调度器嵌在 TUI 进程里,旧二进制没有这些功能);② 建 `~/.modu/extensions.yaml` 开 verifier(文件是增量覆盖,只写 goal 一项即可):

```yaml
extensions:
  - name: goal
    config:
      verifier:
        enabled: true
```

**阶段 1 · 内嵌调度器冒烟(§2.1)**:启动 `modu_code` 进 TUI;另开终端 `tail -f ~/.modu/cron/daemon.log`,应见 `loaded N task(s)` + `cron scheduler started`。TUI 里说"加一个每分钟的测试任务 smoke-test,prompt 是 say hello in one word"——daemon.log 应出现 `config file changed, reloading...`(聊天建任务 + 热加载一起验了)。等 1-2 分钟:`~/.modu/cron/logs/smoke-test/` 出现 NDJSON,最后一行 `run_end` 为 `status:"ok"` 且带 `tokens`;session 列表里出现名为 `cron:smoke-test` 的完整会话(按 job id 关联)。通过标准:任务无人触发自己跑了,TUI 界面没被日志糊花。

**阶段 2 · 三档 cap(§3.2)**:没有手动触发命令,用 `@every 1m` 的专用任务让断路器自己撞线,测完即删:

- timeout:任务 prompt "run bash: sleep 120"、`timeout: 20s` → 下一轮 `run_end` = `status:"timeout"`
- token cap:prompt "逐个读 pkg/ 下所有 go 文件并总结"、`max_tokens_per_run: 3000` → `status:"token_cap"`
- 日额度:config.yaml 临时加 `daily_budget_tokens: 1000`(当天台账已有消耗)→ 任何任务下一轮 `status:"budget_exceeded"`,日志只有 run_start/run_end 两行,channel 收到告警。**测完立刻删掉这行**,否则真实任务会被拒跑

**阶段 3 · 通知 + inbox 人门(§3.5)**:给 smoke-test 挂 `channels: [feishu-daily]`,等一轮,飞书收到 task/status/duration/summary。再把 prompt 改成"在 ./inbox/ 写一个 test-item.md 然后说 done"→ 下一轮通知出现 `inbox: 1 new item(s) waiting for you: test-item.md`;**再下一轮不应重复报该存量条目**(只报当天新增是这条的核心断言)。

**阶段 4 · goal verifier + reviewer(§3.1/§3.3)**:`cp examples/agents/reviewer.md ~/.modu/agents/`;TUI 里 `/goal 在 /tmp/goal-test.txt 写入 hello 并确认文件存在`。完成时应先出现 goal-verifier 的 fork 会话,然后通知区出现 `goal: verifier PASS — completion confirmed by independent check`。验尸式检查(文章 rubric):跑若干真实 goal 后,最近几轮里 verifier 至少真 REJECT 过一次——从未说过 no 的 evaluator 统计上不可能在工作。

**阶段 5 · morning-triage 连跑两天(§3.4)**:按 `examples/loops/morning-triage/README.md` 安装;先把 cron 改成 `@every 5m` 快速跑通一轮(`state/triage.md` 生成并 commit、通知带 inbox),确认后改回 `0 0 6 * * 1-5`,连挂两个早上。第二天检查:未完成 finding 状态延续、已完成的不重做、昨天的 inbox 条目还在、通知只报当天新增。

清理:测试任务(smoke-test / cap-*)在 TUI 里说一句"删掉"即可;临时的 `daily_budget_tokens` 记得移除。

### 5.3 真机执行记录(2026-07-03,deepseek-v4-flash)

用无头 harness 直接驱动 `pkg/cron.RunScheduler` + `modu_code -p`,以上阶段已实跑一遍:

- **调度器**:3 任务并发 tick、fsnotify 热加载、75s 干净退出 ✅;运行日志/台账落 `~/.modu/cron/logs/` ✅
- **token cap**:`status:"token_cap"`,`10372 tokens >= cap 2000`,每轮精确切断 ✅
- **timeout**:`status:"timeout"`,15s 整切断 ✅。注:用 `sleep` 造慢任务测不出来——agent 会把 sleep 丢后台,bash 工具还自带"前台 sleep≥2s 直接拒绝"的防呆,要用真计算(python 忙等)才能撞线
- **日额度**:`status:"budget_exceeded"`,0ms 拒跑、不建 session、channel 收到告警 ✅
- **inbox 人门**:run 内写 `./inbox/test-item.md`,完成通知带 `inbox: 1 new item(s)` ✅(飞书实收)
- **goal verifier PASS**:真目标(写文件)完成,update_goal 通过 ✅
- **goal verifier REJECT(决定性)**:令 agent 谎报完成(不建文件直接 complete),update_goal 被驳回——`rejected by the independent goal verifier (reject 1/3)`,理由精确指出"文件不存在" ✅。maker-checker 门真机工作
- **顺手抓到并修掉一个真 bug**:并发 tick 各自调 `provider.Resolve()` 写全局 `providers.Models` map → fatal concurrent map writes(commit 5910069 加锁修复)。另发现一个**预存在**的流式管道 data race(openai `readSSE` 写 in-flight message vs `pkg/agent.collectAssistantMessage` 读,任何流式会话都有,与 loop 改动无关)——待独立修复
