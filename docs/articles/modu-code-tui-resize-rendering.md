# modu_code TUI resize 重影：终端 reflow 与内联渲染

modu_code 的内联 TUI 曾在窗口缩放后重复绘制输入框、状态行和工具输出。根因不是单个光标计算错误，而是终端 reflow 改变了物理行数，渲染器仍按缩放前的逻辑行数移动光标。
>
> 涉及的核心提交：`3f96073`（迁移 bubbletea v2）、`063dfcb`（增量提交滚动区）、`173689c`（resize 时整屏重绘）、`2343478`（输入框边框与自适应分隔线）、`9f8bac2`（diff 渲染器转正并删除旧路径）。

---

## 0. 结论

只靠相对光标增量刷新，无法同时保证内联 TUI 保留原生 scrollback 且在 resize 后不重影。当前方案在尺寸稳定时用 diff 刷新活动区；收到 resize 后，在 `paint()` 层调用 `HardClear()` 清除屏幕和 scrollback，再从 `b.model.blocks` 重建 transcript。已完成内容通过增量提交进入原生 scrollback，活动区不会随会话持续增高。

---

## 1. 背景：两种终端渲染模型，以及 modu_code 为什么选了更难的那个

终端程序画 UI 有两条路：

**① Alt-screen（备用屏幕缓冲区）。** 程序进入 `ESC[?1049h`，终端切到一块独立的全屏缓冲区，程序对整屏拥有绝对坐标控制权，退出时 `ESC[?1049l` 还原，原始屏幕内容毫发无损。`vim`、`htop`、`less` 都是这种。modu_eval 也是这种——所以它**根本没有 resize 问题**：整屏由它说了算，窗口一变就按新尺寸重画整屏即可。

**② Inline（内联 / 内嵌）。** 程序不切备用屏，直接在当前 shell 的主缓冲区里、在命令提示符下方画一小块"活动区"。它的最大好处是：**输出会进入终端原生的 scrollback**——你可以像翻普通命令输出一样用鼠标滚轮往上翻历史。Claude Code 就是这种交互手感，modu_code 的产品目标也明确要求"**像 Claude Code 一样：保留 scrollback + resize 干净**"。

代价是：inline 模式下程序**不拥有整屏**。它头顶上方是终端自己管理的历史区，它能控制的只是底部那几行活动区，而且只能用**相对光标移动**（"往上 N 行、清掉、重写"）来刷新。这个"相对"二字，正是后面所有痛苦的根源。

---

## 2. 底层原理：终端是怎么刷新一块活动区的

要理解 bug，得先理解正常情况下增量刷新是怎么工作的。

一个内联渲染器维护"上一帧渲染了多少行"。下一帧到来时：

```
ESC[?2026h          ; 开始同步输出（原子提交，避免撕裂闪烁）
ESC[<N>A            ; 光标相对上移 N 行，回到本帧顶部（N = 上一帧逻辑行数 - 1）
\r ESC[2K           ; 回到行首、清除整行，逐行重写
... 重写每一行 ...
ESC[0J              ; 从光标处向下清除（擦掉比上一帧短出来的尾部）
ESC[?2026l          ; 结束同步输出
```

关键点：**这套机制完全建立在"我知道上一帧占了几行、且这几行没被人动过"这个假设上。** 光标上移用的是**相对**位移 `ESC[A`，它从"当前光标在哪"算起。只要这个假设成立，增量刷新又快又稳——每次按键只重写变化的行。

bubbletea v1 的标准渲染器就是这套模型。它工作得很好——**直到窗口大小改变。**

---

## 3. 问题的核心：终端会在 resize 时 reflow，而你的行模型不会

这是整件事最反直觉、也最致命的一点：

> **当终端变窄时，它会把屏幕上已有的、超过新宽度的行就地"重新折行"（reflow）——而且这件事发生在你的程序收到 resize 事件之前。**

举例：活动区里有一行运行中的工具预览 `❯ gh pr create --title "..."`，在 80 列时占 1 个物理行。用户把窗口拖到 40 列：

1. **终端先动手**：这行 60 个字符的逻辑行被折成 2 个物理行。屏幕上活动区的实际物理高度悄悄从 1 行变成了 2 行。
2. **然后你的程序才收到 SIGWINCH**，开始画下一帧。渲染器以为"上一帧是 1 个逻辑行"，于是 `ESC[1A` 上移 1 行——但此刻光标其实在被折出来的第 2 个物理行，上移 1 行只到了第 1 个物理行的位置。
3. `ESC[0J` 从这个**错位**的位置开始清除，于是上一帧顶部那些行**没被清掉**，新帧又叠在下面。

结果就是用户看到的经典症状:**每 resize 一次，旧的输入框 / 状态行 / 工具预览就多叠一层**——"`gh pr creat` / `pr` / `p` / `g` 层叠"、"数据重叠"、"多个 ❯"。逻辑行数和物理行数因为 reflow 而**对不上了**，相对光标模型彻底失准。

### 为什么 bubbletea v1 救不了，v2 也只救了一半

- **v1**：标准渲染器写死了相对光标策略，而且 v1 **没有任何注入自定义渲染器的口子**（只有 `WithoutRenderer()` 全关）。无解。
- **v2**（`charm.land/bubbletea/v2`，注意是 vanity 路径不是 github）：带了新的 cellbuf（`cursedRenderer`）渲染器，理论上更现代。我们一度以为它能修好。但读 v2 源码后发现:**它的 `cursedRenderer.resize()` 只调了 `scr.Erase()`，并不发绝对 home（`ESC[H`）、也不清 scrollback。** 在 inline（非 alt-screen）模式下，终端照样先 reflow，相对光标模型照样失准。**v2 修不了内联 resize 重影**——这是一条记入记忆的纠正结论。

> v2 迁移本身的工作量也不小（API 全变）：`View() string` → `View() tea.View`；`tea.KeyMsg` 从具体 struct 变成 interface，改用 `msg.String()` 判键；`msg.Runes` → `msg.Text`；括号粘贴变成独立的 `tea.PasteMsg`；AltScreen/Mouse 从 program option 变成 `tea.View` 字段。这些都做了，但 resize 问题依旧。

### 一个附带的坑：v2 的 cellbuf 会"裁剪"而不是"折行"

v2 渲染器对活动区里**超过终端宽度的行直接静默截断**（clip），不折行。所以 `View()` 必须对每一行用 `ansi.Wrap` 软折到当前宽度，否则窄屏下提示 / 状态行会丢尾巴。这催生了 `clampViewWidth`——后面整套自绘方案里，"每行按宽度 clamp"成了贯穿始终的一条纪律。

---

## 4. 解决方案：从"修补相对光标"到"全屏自绘"

我们参考了姊妹 TS 项目 pi（`../pi/packages/tui/src/tui.ts` 的 `TUI.doRender`）。pi 的 resize 之所以干净，是因为它**自己持有整屏的行模型**，resize 时直接 `ESC[2J ESC[H ESC[3J`（清屏 + 绝对 home + 清 scrollback）再从自己的模型把每一行重画一遍——锚定在绝对 home，**根本不在乎终端怎么 reflow 的**。正常更新时才用相对光标 diff（只重写首个到末个变化行）。

于是方案定为：**移植 pi 的差分渲染器到 Go。** 但要把它塞进 bubbletea，又踩了一连串地雷。

### 4.1 地雷：`WithoutRenderer()` 会连输入一起关掉

要替换渲染器，只能用 `tea.WithoutRenderer()` 关掉 bubbletea 自带的渲染器。但 bubbletea 把"无渲染器"当成**非 TUI / 守护进程模式**：此时 `initTerminal()` 提前返回，**不调 `initInput()`**——于是没有 raw mode、`ttyOutput` 为 nil → `checkResize()` 直接 no-op → **收不到 SIGWINCH 的 `WindowSizeMsg`**。键盘输入的 reader 虽然还跑，但在 cooked 模式下没用。

"渲染器关掉、输入留着"这个混合态不是白给的，缺的管线得自己补。我们在 `bubble_diff.go` 的 `startDiffMode()` 里补齐：

```go
oldState, _ := term.MakeRaw(fd)              // 自己进 raw 模式
os.Stdout.WriteString("\x1b[?25l\x1b[?2004h\x1b[>1u") // 隐藏光标 + 开括号粘贴 + kitty 键盘
// 自己起一个 goroutine 监听 SIGWINCH，把 term.GetSize 的真实尺寸喂回 WindowSizeMsg
// （启动时 bubbletea 给的尺寸是 {0,0}，得先 seed 一次）
```

清理时 `renderer.Finish()` + 还原终端状态。

### 4.2 渲染器三策略（diffRenderer 的活动区刷新）

`diff_renderer.go` 的 `diffRenderer` 实现三种刷新策略：

- **首帧**：直接画，不清屏。
- **正常更新**：相对光标 diff，`ESC[2K` 重写变化行，整体包在 `ESC[?2026h/l` 同步输出里——保留原生 scrollback，手感和翻普通命令输出一样。
- **renderer 自身的 resize 分支**：先按 reflow 后的物理行数擦除旧活动帧；如果旧活动帧已经填满 / 溢出可见屏，则只清**可见屏**（`ESC[2J ESC[H`，不发 `ESC[3J`）再重画活动区。

整套都用 `ESC[?2026h/l`（synchronized output）包裹，让终端原子地一次性提交整帧，消除撕裂和闪烁。

注意：真正的"pi 式清 screen + scrollback，然后重建整段 transcript"不是 `diffRenderer.Render()` 自己的 resize 分支完成的，而是后面 `bubble_diff.go` 的 `paint()` 通过 `lastPaintW/H` 检测 resize 后调用 `HardClear()` + `rerenderScrollback()` 完成。

### 4.3 关键修正：resize 时上移要用"物理行数"而不是"逻辑行数"

第一版 resize 分支仍栽在同一个坑上：它用 `CursorUp(len(previousLines)-1)`——**逻辑行数**——去回到帧顶。但窄缩时被 clamp 到旧宽度的行现在超过新宽度、又被折成 ≥2 个物理行，逻辑行数**少算了**，`ESC[0J` 从帧顶下方开始清，旧帧顶部继续叠。

修法很外科（只动 `diff_renderer.go`）：

```go
// frameRows: 按 ceil(StringWidth(line)/width) 累加每一逻辑行 reflow 后的物理行数
func frameRows(lines []string, width int) int { ... }
// resize 分支改用：上移 frameRows(previousLines, width) - 1
```

在真实 tmux 里验证：层叠副本 3→1，3/3 次干净。

### 4.4 当活动区比屏幕还高：overflow 滚进了 scrollback

还有一种重影 `ESC[0J` 够不着：当**流式回复长到超过一屏**，它的顶部会滚进终端**原生 scrollback**——相对清除指令根本触及不到那里。resize 时整帧（含多个 `❯`/状态/thinking）又被复制一份。

> 这个 case 只有用**已 attach 的 tmux 客户端**拖动窗口才能复现（detach 状态下 `resize-window` 不触发）。复现办法：pty-fork → 子进程 `tmux attach`、父进程对 pty 做 `TIOCSWINSZ` = 等价于拖动窗口。

阶段性修法：当 `frameRows(previousLines,width) >= height || previousViewportTop > 0`（帧已填满屏幕 ⇒ 上方没有已完成的内容 ⇒ 安全）时，用 `ESC[2J ESC[H` 清整个**可见屏**（**注意不发 `ESC[3J`**，保住原生历史）再从 home 重画。

### 4.5 架构级根治：增量提交滚动区（incremental scrollback streaming）

上面都是"出血了再止血"。真正的根因是**活动区允许长到超过一屏**。根治办法：**流式过程中主动把已经定稿的内容推进 scrollback，活动区永远只留正在生成的尾巴。**

`bubble_diff.go` 的 `commitStreamingPrefix()`（在 `paint()` 开头调用）：当助手 block 正在流式、且活动帧会溢出（`len(blockLines)+streamChromeRows > height`）时，把它**已定稿的 markdown 块**提交进 scrollback，只保留进行中的尾部留在活动区。

这里有两条用血换来的正确性规则：

- **按内容边界提交，不能按渲染行号提交。** glamour 每来一个 token 就**整块重渲染**（行号偏移、列表重新编号、折行变化），按行号提交必然**重复**。`lastStableBlockEnd(content)` 返回"最后一个**不在未闭合 ``` 围栏内**的空行块分隔符"之后的偏移；只渲染这段稳定前缀，校验它确实是完整渲染的 append-only 前缀（`prefLines[i]==fullLines[i]`），只提交新增的行。用 `streamCommittedContent`/`streamCommitN`/`streamBlockIdx` 跟踪。
- `●` 标记在第 0 行，随第一个 chunk 自然进 scrollback；活动尾部保留缩进。`MessageEnd` 只提交未提交的尾巴（`printAssistantTailCmd`）。能一屏放下的短回复**中途一行都不提交**——行为和老的"MessageEnd 时一把 glamour"完全一致。

### 4.6 最终形态：resize 时整屏重绘（pi 方案转正）

最后一块拼图：提交进原生 scrollback 的文本是**带悬挂缩进预折行**的。终端物理上没法 reflow 一个悬挂缩进——窄缩时每个已提交行独立重折，把尾巴 orphan 出来（`today?` / `forward.` 孤零零一行）。

用户明确选择"**重构成全屏自绘（pi 方案）**"，而不是保留缩进或容忍 orphan。实现（`diff_renderer.go`/`bubble_diff.go`/`bubble.go`，约 +103 行）：

```
paint() 里通过 lastPaintW/H 检测到 resize：
  1. above := rerenderScrollback()
                                // 从 b.model.blocks 这个 source of truth 重建整段记录：
                                //   renderInlineHeader + 每个 block 经 renderSingleBlock
                                //   + 重建的回合分隔线，全部按新宽度重新折行，
                                //   跳过 liveBlockIndex 避免重画活动块
  2. diffRenderer.HardClear()   // ESC[2J ESC[H ESC[3J，归零所有状态（含 previousWidth/Height）
  3. InsertAbove(above)         // 把重新折行后的 transcript 推回 scrollback
  4. 正常的 commitStreamingPrefix + Render(active) 继续跑
```

**结果：完美。** 真实 attach tmux、流式过程中温和与疯狂拖动窗口：可见区 10/10 干净、历史 10/10 干净（无层叠、无 `Working` 快照残留、无回合重复），已提交文本在任意宽度都**均匀重折且保留悬挂缩进**，glamour 排版完好。

被接受的权衡：每回合的 `✓ Completed (…)` 摘要在 resize 后会丢（不在 model 里）；每次 resize 整段记录重新 emit（可接受——同步输出原子完成、resize 本就低频）；原生 scrollback 的滚动位置在 resize 时重置。

---

## 5. 性能：节流与流式块复用

当前代码没有持久的 `blockRenderCache` / `cachedBlockLines` / `blockFingerprint` 这类每块渲染缓存；`renderSingleBlock` 仍会在需要时跑 glamour markdown 渲染。

实际已经落地的性能手段有两类：

- **渲染节流**：`Update()` 后不是无条件每个事件立即重画到底，而是通过 `paintInterval = 16ms`、`bubblePaintMsg`、`requestPaint()` 把突发事件合并到约 60fps。
- **流式块当帧复用**：`commitStreamingPrefix()` 会把当前 streaming assistant block 的 clamp 后行放到 `streamLines`，`renderInlineLive()` 同一轮 paint 直接复用尾部，避免同一帧重复渲染一次。

如果后续还出现长 transcript 卡顿，才需要另外做真正的 block 级缓存；这不是上述 5 个提交里已经实现的内容。

---

## 6. 测试方法论：一个让我赔上一整个 session 的教训

> **绝对不要用 pyte 验证 resize。** `pyte.Screen.resize()` 是**截断（TRUNCATE）**行，**不 reflow**。而真实终端**和 tmux**（用户就跑在 tmux 里，`TERM_PROGRAM=tmux`）resize 时是**reflow**。

pyte 同时干了两件坏事：(a) 把真 bug 藏起来；(b) 显示出真终端上根本不存在的"幽灵重影"。追这些幽灵导致两个臆想中的修复（`clampScrollbackLines`、帧高上限），对 pyte 有用、对 tmux **毫无作用**，全部回滚。

**正确做法——永远在真实 tmux 里复现 resize：**

```bash
# 用一个 scratch tmux server，跑旧/新二进制做 A/B
tmux -L test new-session -d -x 80 -y 24 "./modu_code"
tmux -L test send-keys ...                  # 驱动输入
tmux -L test resize-window -x 40 -y 24      # 改尺寸
tmux -L test capture-pane -p -S -N          # 含 scrollback 一起抓
```

- 验证**内容完整性**：在 `-S` scrollback 里数 `✓ Completed`（每回合必须 1 个）和 `● thinking` 检测回合重复；在可见区数它们检测重影层叠。
- 注意计数陷阱：数回答关键词会连用户 prompt + thinking 回显一起匹配，要用唯一的完成标记。
- 溢出 / 整屏类 bug 必须用**已 attach 的客户端**拖动复现（detach 的 `resize-window` 不触发 reflow 滚动）。
- 做 A/B 时用 `git stash` 暂存渲染文件即可编译出干净的旧二进制。

我们还写了一个**微型 VT 模拟器**（`diff_renderer_vt_test.go`）解析我们用到的那个转义子集 → 暴露可见网格 + scrollback，用来在单测里验证 Render / InsertAbove / resize 后的可见网格和 scrollback 行为。这里要注意：当前 `TestDiffRendererResizeShortFrameTopAnchored` 明确记录的是**短帧 top-anchored 的当前行为**，不是"已修成 bottom-aligned"；`fullScreenLines()` 也没有通过前置空行把短帧钉到底部。

---

## 7. 最终渲染路径

`MODU_TUI_DIFF` 灰度开关在 A/B 充分后移除（`9f8bac2`），diff 渲染器成为唯一路径。删掉了整条旧的非 diff 渲染链（`pkg/tui` 净 −181 行）：`useDiff`/`inline` 这两个永远为 true 的字段、非内联的 `RunBubbleTeaWithOptions`、整套 `renderInlineView`/`renderTranscript`/`renderHeader` 机器、`tea.Println` 分支（现在一切都入队 scrollback）、假光标逻辑（现在是真硬件光标 `PlaceCaret`）。`viewString()` 重新定义为活动帧快照（`fullScreenLines` join），`View()` 仅为满足 tea.Model 接口和测试保留——`WithoutRenderer` 下永不被调。

输入框现在上下各一条 `hRule(width)` 全宽 `─` 框起来；回合分隔线改为**全宽**、且画在**每个新用户回合之前**（回合之间），resize 时由 `rerenderScrollback` 按新宽度重建（修了"分割线 resize 后消失"）。

---

## 附：关键文件索引

- `pkg/tui/diff_renderer.go` — pi 移植的差分渲染器：三策略 + `frameRows` + `HardClear` + `InsertAbove` + `PlaceCaret`
- `pkg/tui/bubble_diff.go` — `startDiffMode`（自补 raw/SIGWINCH/粘贴）、`paint`、`commitStreamingPrefix`、`rerenderScrollback`、`lastStableBlockEnd`、流式块当帧复用
- `pkg/tui/bubble.go` — `runBubbleWithOptions` 接线 `WithoutRenderer` + diff 模式；`Update` 拆成 dispatch + paint
- `pkg/tui/diff_renderer_vt_test.go` — 微型 VT 模拟器，单测里复现可见网格 + scrollback

> **遗留 / 未做：** 没有持久的 block 级渲染缓存，长 transcript 下仍可能重复跑 `renderSingleBlock`；kitty keyboard 只启用了用于消歧的 flag 1（`ESC[>1u`），没有实现完整协议能力；pi 里的 kitty-image / overlay / clearOnShrink 等分支未移植。
