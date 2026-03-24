# env

轻量级环境变量加载库，支持从 `.env` 文件读取配置。采用 Functional Options 模式，使用简洁灵活。

## 安装

```go
import "github.com/openmodu/modu/pkg/env"
```

## 快速开始

```go
// 加载当前目录的 .env 文件
env.Load()

// 获取环境变量
apiKey := env.Get("API_KEY")
port := env.GetDefault("PORT", "8080")
```

## 使用示例

### 基础用法

```go
// 默认加载 .env（不覆盖已有变量）
env.Load()

// 加载指定文件
env.Load(env.WithFile(".env.local"))

// 从指定目录加载
env.Load(env.WithDir("/etc/myapp"))

// 覆盖已有环境变量
env.Load(env.WithOverride())

// 文件必须存在
env.Load(env.WithRequired())
```

### 组合选项

```go
env.Load(
    env.WithFile(".env.production"),
    env.WithDir("/etc/myapp"),
    env.WithOverride(),
    env.WithRequired(),
)
```

### 获取变量

```go
// 获取，不存在返回空字符串
value := env.Get("KEY")

// 带默认值
port := env.GetDefault("PORT", "8080")

// 必须存在，否则返回 error
secret, err := env.GetRequired("SECRET")

// 必须存在，否则 panic
token := env.MustGet("TOKEN")
```

### Panic 版本

```go
// 加载失败则 panic
env.MustLoad(env.WithRequired())
```

## 选项列表

| 选项 | 说明 |
|------|------|
| `WithFile(name)` | 指定文件名（默认 `.env`）|
| `WithDir(dir)` | 指定目录 |
| `WithOverride()` | 覆盖已有环境变量 |
| `WithRequired()` | 文件必须存在，否则报错 |

## .env 文件格式

```env
# 注释
API_KEY=your-api-key
export DB_URL=postgres://localhost/db

# 支持引号
MESSAGE="Hello World"
NAME='Single Quotes'

# 支持转义
MULTILINE="Line1\nLine2"
```

## API

| 函数 | 说明 |
|------|------|
| `Load(opts...)` | 加载环境变量 |
| `MustLoad(opts...)` | 加载，失败 panic |
| `Get(key)` | 获取变量 |
| `GetDefault(key, def)` | 带默认值获取 |
| `GetRequired(key)` | 必须存在 |
| `MustGet(key)` | 必须存在，否则 panic |
| `Set(key, value)` | 设置变量 |

