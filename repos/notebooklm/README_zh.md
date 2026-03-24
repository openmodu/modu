# notebooklm

Google NotebookLM 非官方 Go SDK，支持 Notebook、Source、Artifact 管理以及 Chat 功能。

## 安装

```go
import "github.com/openmodu/modu/repos/notebooklm"
```

## 快速开始

```go
// 登录（会打开浏览器）
notebooklm.Login()

// 从存储的认证信息创建客户端
client, err := notebooklm.NewClientFromStorage("")
if err != nil {
    log.Fatal(err)
}

// 列出所有 Notebook
notebooks, err := client.ListNotebooks(ctx)

// 创建新 Notebook
notebook, err := client.CreateNotebook(ctx, "My Notebook")

// 添加 URL 源
source, err := client.AddSourceURL(ctx, notebook.ID, "https://example.com/article")

// 生成音频播客
status, err := client.GenerateAudio(ctx, notebook.ID, vo.AudioFormatDeepDive, vo.AudioLengthDefault)

// 提问
result, err := client.Ask(ctx, notebook.ID, "总结这些内容", nil)
fmt.Println(result.Answer)
```

## 认证

首次使用需要通过浏览器登录：

```go
// 登录并自动保存认证信息
if err := notebooklm.Login(); err != nil {
    log.Fatal(err)
}

// 后续使用：从存储加载
client, err := notebooklm.NewClientFromStorage("")

// 检查存储是否存在
if notebooklm.StorageExists() {
    // 已登录
}

// 获取存储路径
path := notebooklm.GetStoragePath()
```

## API 概览

### Notebook 操作

```go
// 列出所有 Notebook
notebooks, _ := client.ListNotebooks(ctx)

// 创建 Notebook
notebook, _ := client.CreateNotebook(ctx, "标题")

// 获取 Notebook
notebook, _ := client.GetNotebook(ctx, notebookID)

// 重命名 Notebook
client.RenameNotebook(ctx, notebookID, "新标题")

// 删除 Notebook
client.DeleteNotebook(ctx, notebookID)
```

### Source 操作

```go
// 列出所有 Source
sources, _ := client.ListSources(ctx, notebookID)

// 添加 URL（自动识别 YouTube）
source, _ := client.AddSourceURL(ctx, notebookID, "https://...")

// 添加本地文件
source, _ := client.AddSourceFile(ctx, notebookID, "/path/to/file.pdf")

// 添加文本
source, _ := client.AddSourceText(ctx, notebookID, "标题", "内容...")

// 删除 Source
client.DeleteSource(ctx, notebookID, sourceID)
```

### Artifact 操作

```go
// 生成音频播客
status, _ := client.GenerateAudio(ctx, notebookID, vo.AudioFormatDeepDive, vo.AudioLengthDefault)

// 生成视频
status, _ := client.GenerateVideo(ctx, notebookID, vo.VideoFormatBriefing, vo.VideoStyleClassroom)

// 轮询生成状态
status, _ := client.PollGeneration(ctx, notebookID, status.TaskID)

// 列出所有 Artifact
artifacts, _ := client.ListArtifacts(ctx, notebookID)

// 下载音频
client.DownloadAudio(ctx, notebookID, "./output.m4a", "")

// 下载视频
client.DownloadVideo(ctx, notebookID, "./output.mp4", "")
```

### Chat 操作

```go
// 向 Notebook 提问（使用所有 Source）
result, _ := client.Ask(ctx, notebookID, "问题内容", nil)
fmt.Println(result.Answer)

// 指定特定 Source 提问
result, _ := client.Ask(ctx, notebookID, "问题", []string{sourceID1, sourceID2})
```

## 音频/视频格式选项

```go
// 音频格式
vo.AudioFormatDeepDive      // 深度对话
vo.AudioFormatConversation  // 对话形式

// 音频长度
vo.AudioLengthDefault       // 默认
vo.AudioLengthShort         // 短
vo.AudioLengthMedium        // 中
vo.AudioLengthLong          // 长

// 视频格式
vo.VideoFormatBriefing      // 简报

// 视频风格
vo.VideoStyleClassroom      // 课堂风格
```

## 文件结构

```
notebooklm/
├── client.go      # 核心 Client 和 RPC 调用
├── notebook.go    # Notebook CRUD
├── source.go      # Source 管理
├── artifact.go    # Artifact 生成/下载
├── chat.go        # Chat 功能
├── utils.go       # 工具函数
├── auth.go        # 认证管理
├── login.go       # 浏览器登录
├── parser.go      # 响应解析
└── rpc/           # RPC 编解码
```

