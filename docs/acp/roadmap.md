# ACP Integration — 开发规划

> 搭档文件：[`architecture.md`](./architecture.md)。
>
> 原则：**按单模块闭环（设计 → 实现 → 单测 → 绿灯）**，每完成一个模块 commit 一次；最后一步跑通端到端 E2E。

## 一、模块依赖图

```
jsonrpc (M1)
   ▲
   │
process (M2)
   ▲
   │
client (M3) ──────────► bridge (M4)
   ▲                       ▲
   │                       │
   └──────── provider (M5) ┘
                ▲
                │
           manager (M6)
                ▲
                │
         acp-gateway (M7)
                ▲
                │
            E2E (M8)
```

依赖是严格的 DAG — 上游没过测试，下游不动。

---

## 二、逐模块开发计划

### M1 — `pkg/acp/jsonrpc`

**目录**
```
pkg/acp/jsonrpc/
├── protocol.go
└── protocol_test.go
```

**对外 API**
```go
type Request struct { JSONRPC string; ID int; Method string; Params any }
type Response struct { JSONRPC string; ID int; Result json.RawMessage; Error *Error }
type Notification struct { JSONRPC string; Method string; Params any }
type Message struct { JSONRPC string; ID *int; Method string; Params json.RawMessage; Result json.RawMessage; Error *Error }

func NewRequest(id int, method string, params any) *Request
func NewNotification(method string, params any) *Notification
func NewResponse(id int, result any) *Response
func NewErrorResponse(id int, code int, message string) *Response

func (m *Message) IsRequest() bool
func (m *Message) IsResponse() bool
func (m *Message) IsNotification() bool
func (m *Message) ParseParams(target any) error
func (m *Message) ParseResult(target any) error

const (ParseError = -32700; InvalidRequest = -32600; MethodNotFound = -32601; InvalidParams = -32602; InternalError = -32603)
```

**测试用例（最少）**
- `TestRequestMarshal` — `NewRequest(1,"ping",{x:1})` JSON 字段序和字段名
- `TestNotificationMarshal` — 无 ID
- `TestMessageJudgement` — request/response/notification 三种 JSON 分别断言 `Is*` 正确
- `TestParseParams_Nil` — `Params == nil` 时返回 nil
- `TestError_Error` — `Error()` 带 `details` 和不带两种情况
- `TestNewResponse_NilResult` — `result == nil` 要序列化成 `"null"`

**完成标准**
- `go test ./pkg/acp/jsonrpc/ -v` 全绿
- 无外部依赖（纯 `encoding/json`）
- 覆盖率 ≥ 90%

**预估工时**：1 小时

---

### M2 — `pkg/acp/process`

**目录**
```
pkg/acp/process/
├── process.go
├── process_unix.go      // hideWindow 空实现 (兼容 Windows)
├── process_windows.go   // hideWindow 填 SysProcAttr.CreationFlags
└── process_test.go
```

**对外 API**
```go
type Status string
const (StatusIdle Status = "idle"; StatusStarting; StatusRunning; StatusError; StatusStopped)

type Config struct {
    ID      string
    Command string
    Args    []string
    Env     map[string]string
    Dir     string
}

type Process struct { /* ... */ }

func New(cfg Config) *Process
func (p *Process) Start() error
func (p *Process) Stop() error
func (p *Process) Status() Status
func (p *Process) Write(line []byte) error            // 发一行 LDJSON
func (p *Process) Lines() <-chan []byte                // 订阅 stdout 按行
func (p *Process) Stderr() <-chan []byte               // 订阅 stderr 按行
func (p *Process) Done() <-chan struct{}               // 进程退出信号
```

**设计要点**
- stdin 写：简单互斥锁保护 `io.WriteCloser`
- stdout 读循环：`bufio.Scanner` + `Buffer(make([]byte,0,64*1024), 10*1024*1024)`
- stderr 独立 goroutine，否则可能阻塞 stdout
- `Stop()` 三步：close stdin → `os.Interrupt` → 3s timeout → `Kill()`
- `Status` 状态变更全走 `setStatus()` 加锁

**测试用例**
- `TestStart_InvalidCmd` — command 不存在时返回 error，status=Error
- `TestEchoRoundtrip` — 用 `/bin/cat` 做回声进程：写一行 → 从 Lines() 读出来
- `TestStderrCapture` — 用 shell `>&2 echo oops`
- `TestGracefulStop` — 长命进程 `sleep 60`，Stop 后在 5s 内退出
- `TestStop_KillFallback` — 模拟 SIGINT 无响应（写个 trap 脚本），3s 后被 Kill
- `TestLinesBuffer10MB` — 写一行超长 JSON（1MB），能完整读回

**完成标准**
- 单测全绿，含 Windows 跳过标记（`//go:build !windows`）的部分
- 显式清理：测试结束所有子进程必须被回收（`t.Cleanup` 里 Stop）

**预估工时**：3 小时

---

### M3 — `pkg/acp/client`

**目录**
```
pkg/acp/client/
├── client.go
├── reverse.go          // 反向 RPC 派发
└── client_test.go
```

**对外 API**
```go
type Client struct { /* ... */ }

type PermissionRequest struct {
    SessionID string
    ToolCall  struct {
        ToolCallID string
        Title      string
        Kind       string
        RawInput   map[string]any
    }
    Options []struct { OptionID, Name, Kind string }
}

type PermissionHandler func(req *PermissionRequest) string  // 返回选中的 optionId
type FSHandler interface {
    ReadTextFile(path string) (string, error)
    WriteTextFile(path, content string) error
}

type Config struct {
    Proc           *process.Process
    OnPermission   PermissionHandler
    FS             FSHandler                 // nil 时反向 fs 请求返回 error
}

func New(cfg Config) *Client

func (c *Client) Start() error                                // 启动 process + 读循环
func (c *Client) Stop() error
func (c *Client) Request(method string, params any) (*jsonrpc.Message, error)
func (c *Client) Notify(method string, params any) error
func (c *Client) OnNotification(fn func(*jsonrpc.Message)) func()   // 返回 cleanup
```

**设计要点**
- `pending map[int]chan *jsonrpc.Message` — request ID 关联 response
- `requestID` 单调递增（加锁）
- 读循环：
  - `IsResponse` → 查 pending → 发送到对应 channel
  - `IsRequest` → 进 `handleReverse`
  - `IsNotification` → 广播给所有订阅者
- 反向 RPC 派发器（`reverse.go`）:
  - `session/request_permission` → 调 `OnPermission` 同步拿 optionId → 回 response
  - `fs/read_text_file` → 调 `FS.ReadTextFile`
  - `fs/write_text_file` → 调 `FS.WriteTextFile`
  - 其他方法 → 回 `MethodNotFound`
- `Stop()`：close pending channels → 停 process
- **订阅者回调在 goroutine 里跑**，避免一个慢 handler 卡住整个读循环

**测试用例**
- `TestRequest_Response` — mock agent goroutine 回 response，Request 能拿到
- `TestRequest_ConcurrentIDs` — 10 并发 Request 没有 ID 冲突，响应正确关联
- `TestNotification_Broadcast` — 两个 OnNotification 订阅者都收到
- `TestNotification_CleanupRemovesSubscriber` — cleanup 后不再收到
- `TestReversePermission_Selected` — mock agent 发 `session/request_permission`，client 通过 handler 选 option，agent 收到正确 response
- `TestReverseFS_ReadWrite` — mock agent 发 read/write，验证 client 转发到 FSHandler
- `TestReverseFS_Denied` — FS 为 nil 时返回 error response
- `TestRequest_AfterStop_ReturnsError`

**完成标准**
- 单测全绿
- 测试里必须用**真的 pipe**（`io.Pipe`）或**真的子进程**（重用 M2 process），不能 mock interface — 保证协议层真跑通

**预估工时**：4 小时

---

### M4 — `pkg/acp/bridge`

**目录**
```
pkg/acp/bridge/
├── bridge.go
├── bridge_test.go
└── testdata/
    ├── agent_message_chunk.json
    ├── agent_thought_chunk.json
    ├── tool_call.json
    ├── tool_call_update_completed.json
    └── available_commands_update.json
```

**对外 API**
```go
// Translate 把一个 ACP session/update notification 转成 0..N 个 modu AgentEvent
func Translate(msg *jsonrpc.Message) ([]agent.AgentEvent, error)
```

**设计要点**
- 纯函数，无 IO，无状态
- 对未知 `sessionUpdate` 返回 `nil, nil`（静默忽略）而不是 error — 兼容协议演进
- ClaudeCode 的 `_meta.claudeCode` 扩展字段在 bridge 里把它 flatten 到 AgentEvent 的 meta 里

**测试用例（每个 fixture 一个 case）**
- `agent_message_chunk` → `EventTypeMessageUpdate` + `text_delta`
- `agent_thought_chunk` → `EventTypeMessageUpdate` + thinking flag
- `tool_call` → `EventTypeToolExecutionStart`，ToolCallID / ToolName / Args 正确
- `tool_call_update(status=completed)` → `EventTypeToolExecutionEnd`
- `tool_call_update(error="...")` → `EventTypeToolExecutionEnd` + IsError=true
- `available_commands_update` → 空事件数组（或自定义 meta）
- `unknown_update` → `nil, nil`

**完成标准**
- 每个分支都有 fixture 覆盖
- 覆盖率 ≥ 90%

**预估工时**：2 小时

---

### M5 — `pkg/acp/provider`

**目录**
```
pkg/acp/provider/
├── provider.go
├── session.go           // sessions map + initialize 状态
├── provider_test.go
└── testdata/
    └── mock_server.go   // mock ACP agent server helper
```

**对外 API**
```go
type Options struct {
    ID     string                 // Provider ID，如 "acp:claude"
    Client *client.Client          // 已配置好的 client（未必 Start）
}

func New(opts Options) *Provider

// 实现 providers.Provider
func (p *Provider) ID() string
func (p *Provider) Stream(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (stream.Stream, error)
```

**设计要点**
- 第一次 Stream 触发 `initialize`（lazy），失败则每次 Stream 都重试
- `sessions map[string]string` — key 是 cwd（从 `llmCtx.Meta` 里取，约定 key `acp.cwd`）
- Stream 内部：
  1. 确保 session 已创建（若 map 未命中调 `session/new`）
  2. 订阅 client notifications，通过 `bridge.Translate` 转 SimpleStreamEvent 推到输出流
  3. 调 `session/prompt` 同步等 response
  4. response 到达后发 `stop_reason` event，close 流
- 抢占/取消：`ctx.Done()` 时发 `session/cancel`（ACP 标准方法），然后返回

**测试用例**
- `TestStream_HelloWorld` — mock agent 收到 prompt，回一个 agent_message_chunk，关掉 session；provider.Stream 能产出 text_delta 和 stop_reason
- `TestStream_ReusesSession` — 同一 cwd 两次 Stream，mock agent 只收到一次 `session/new`
- `TestStream_NewSessionPerCwd` — 不同 cwd 两次 Stream，mock agent 收到两次 `session/new`，sessionId 不同
- `TestStream_CtxCancel` — 调用方取消 ctx，provider 发 `session/cancel` 后退出
- `TestStream_InitializeError` — mock agent 让 initialize 返回 error，Stream 返回 error

**完成标准**
- 实现 `providers.Provider` 接口，`providers.Register` 后可通过 `providers.Get(id)` 拿到
- 单测全绿

**预估工时**：4 小时

---

### M6 — `pkg/acp/manager`

**目录**
```
pkg/acp/manager/
├── config.go
├── manager.go
└── manager_test.go
```

**对外 API**
```go
type AgentConfig struct {
    ID             string
    Name           string
    Command        string
    Args           []string
    Env            map[string]string
    PermissionMode string // "default" | "bypass"
}

type Config struct {
    Agents        []AgentConfig
    DefaultAgent  string
    Gateway       GatewayConfig
}

func LoadConfig(paths ...string) (*Config, error)         // 按优先级查找

type Manager struct { /* ... */ }

func New(cfg *Config, hooks Hooks) *Manager
func (m *Manager) Provider(agentID string) (*provider.Provider, error)   // lazy init
func (m *Manager) List() []string
func (m *Manager) Shutdown() error
```

其中 `Hooks` 里塞 `PermissionHandler` / `FSHandler`，由 gateway 注入。

**配置文件查找顺序**
1. `./acp.config.json`
2. `~/.modu/acp.config.json`
3. `~/.config/modu/acp.json`

**测试用例**
- `TestLoadConfig_Roundtrip` — 写/读配置文件，字段不丢
- `TestLoadConfig_FirstMatchWins`
- `TestLoadConfig_DefaultAgent_Missing` — defaultAgent 不在 Agents 里时报错
- `TestProvider_LazyInit` — 第一次调 Provider 才启动子进程
- `TestProvider_IDNotFound`
- `TestShutdown_StopsAll`

**完成标准**：配置 schema 稳定，可以被 gateway 直接用

**预估工时**：2 小时

---

### M7 — `cmd/acp-gateway`

**目录**
```
cmd/acp-gateway/
├── main.go
├── server.go
├── handlers.go
├── auth.go
├── task.go              // 把 modu mailbox task 桥接成 HTTP 资源
└── server_test.go
```

**HTTP 路由（v1）**
| Method | Path | 行为 |
|---|---|---|
| GET | `/healthz` | 存活检查 |
| GET | `/api/agents` | 列可用 agent |
| POST | `/api/tasks` | body: `{agent, prompt, cwd}` → 发布到 mailbox → 返回 `{taskId}` |
| GET | `/api/tasks/:id` | 查 task status/result |
| GET | `/api/tasks/:id/stream` | SSE：把 mailbox events + bridge events 推出去 |
| POST | `/api/tasks/:id/approve` | body: `{toolCallId, optionId}` → 回调 client permission |

**鉴权**：`Authorization: Bearer <token>`，token 从 env `MODU_ACP_TOKEN` 或 config 读；`/healthz` 免鉴权。

**Worker 启动**：main 里起 N 个 ACP worker goroutine（每个 agent 一个），claim task → 用 manager 拿到 provider → 调 provider.Stream → 事件转 mailbox event → CompleteTask。

**测试用例（httptest）**
- `TestHealthz_NoAuth`
- `TestAuthRequired` — 无 token → 401
- `TestPostTask_Publishes` — 调用后 mailbox 里能拿到 task
- `TestGetTask_Status` — publish → fake worker 标 completed → GET 返回正确状态
- `TestStreamSSE` — POST task → GET stream → 收到 SSE 事件
- `TestApprove_Forwards` — publish task → worker 触发 permission 请求 → approve 转发到 client

**完成标准**：手工 `curl` 能跑通（见下方"手工验证脚本"）

**预估工时**：4 小时

---

### M8 — E2E 测试

**目录**
```
examples/acp_e2e/
├── README.md
├── main.go               // 启动 gateway + 1 个 ACP worker
├── test_e2e.sh           // 端到端测试脚本
└── mock_acp_agent/       // 纯 Go 的 fake ACP agent（可执行）
    └── main.go
```

**Mock Agent**：自己写一个可执行的 fake ACP agent（纯 Go 编译成 binary），规避 `npx @zed-industries/claude-code-acp` 对网络/API key 的依赖。CI 可跑。

**E2E 流程**
1. 启动 mailbox hub + gateway（监听 127.0.0.1:7080）
2. 起 worker：`agent=mock-claude`，用 fake agent binary
3. `curl POST /api/tasks` 发 prompt
4. `curl GET /api/tasks/:id/stream` SSE 订阅
5. 断言：事件序列包含 start → text chunks → end；最终 status=completed
6. 另跑一个 case 测反向权限：fake agent 触发 permission → gateway SSE → `curl POST /approve` → 完成

**完成标准**
- `test_e2e.sh` 在开发机（macOS/Linux）上 exit 0
- 不依赖外部 LLM API key（mock agent 全自洽）
- 真实 claude-code-acp 的验证以"文档指引 + 手工跑通一次"为准（README 里记录）

**预估工时**：4 小时

---

## 三、Commit 策略

- 每完成一个模块 commit 一次，消息格式：
  - `feat(acp): M1 jsonrpc protocol + tests`
  - `feat(acp): M2 subprocess lifecycle + tests`
  - `feat(acp): M3 RPC client with reverse-RPC`
  - `feat(acp): M4 event bridge`
  - `feat(acp): M5 provider adapter`
  - `feat(acp): M6 manager + config`
  - `feat(acp): M7 gateway HTTP API`
  - `test(acp): M8 E2E with mock agent`
- 每次 commit 前跑 `go test ./pkg/acp/... ./cmd/acp-gateway/...`
- 文档提交单独一次：`docs(acp): architecture + roadmap`（这个 commit 先做）

---

## 四、手工验证脚本（Gateway 上线后）

```bash
# 启动 gateway (home machine)
export MODU_ACP_TOKEN=dev-token
go run ./cmd/acp-gateway &

# 发任务
curl -X POST http://localhost:7080/api/tasks \
  -H "Authorization: Bearer $MODU_ACP_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"agent":"claude","prompt":"list files in cwd","cwd":"/tmp"}'
# -> {"taskId":"abc123"}

# 订阅流
curl -N -H "Authorization: Bearer $MODU_ACP_TOKEN" \
  http://localhost:7080/api/tasks/abc123/stream
# 预期看到 SSE: status, update, tool_call, done
```

---

## 五、工时总估

| 阶段 | 预估 |
|---|---|
| 文档（架构 + roadmap） | 1 h |
| M1 jsonrpc | 1 h |
| M2 process | 3 h |
| M3 client | 4 h |
| M4 bridge | 2 h |
| M5 provider | 4 h |
| M6 manager | 2 h |
| M7 gateway | 4 h |
| M8 E2E | 4 h |
| **合计** | **25 h** |

按每天专注 4 小时算，~6-7 天。

---

## 六、里程碑与验收

| 里程碑 | 验收标准 |
|---|---|
| ✅ **MS0** 文档评审 | 架构 + roadmap 通过 review |
| **MS1** 协议层可用 | M1+M2+M3 全绿，能起 fake 子进程收发 JSON-RPC |
| **MS2** 单 agent 跑通 | M4+M5 完成，`examples/acp_spike` 能跑 mock agent，产出 modu AgentEvent 流 |
| **MS3** 可配置多 agent | M6 完成，配置文件驱动多 agent 池 |
| **MS4** 远端可用 | M7 完成，`curl` 能端到端 |
| **MS5** E2E 绿灯 | M8 完成，`test_e2e.sh` exit 0 |
| **MS6** 真 agent 验证 | 本地用真 `@zed-industries/claude-code-acp` 跑通一次（手工记录） |
| **MS7** iOS 接入（后续） | 独立 milestone，本 roadmap 不覆盖 |
