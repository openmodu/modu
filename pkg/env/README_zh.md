[English](README.md) | [中文](README_zh.md)

# 环境变量

`pkg/env` 把 `.env` 文件中的 `KEY=VALUE` 写入当前进程，并封装常用的 `os.Getenv` 操作。默认忽略不存在的文件，也不覆盖进程中已有的环境变量；只有设置 `WithOverride` 才会覆盖。

## 加载文件

```go
import "github.com/openmodu/modu/pkg/env"

if err := env.Load(); err != nil {
	return err
}
```

`Load()` 默认读取当前目录的 `.env`。下面的选项用于修改路径和失败行为：

```go
err := env.Load(
	env.WithDir("/etc/myapp"),
	env.WithFile(".env.production"),
	env.WithOverride(),
	env.WithRequired(),
)
```

| 选项 | 行为 |
|---|---|
| `WithFile(name)` | 使用 `name`，不再读取 `.env` |
| `WithDir(dir)` | 从 `dir` 下查找文件 |
| `WithOverride()` | 覆盖进程中已有的变量 |
| `WithRequired()` | 文件不存在时返回错误 |

只有当配置错误必须终止进程时，才在启动阶段使用 `MustLoad`：

```go
env.MustLoad(env.WithRequired())
```

## 读取和设置变量

```go
apiKey := env.Get("API_KEY")                    // 未设置时返回空字符串。
port := env.GetDefault("PORT", "8080")        // 未设置或为空时返回默认值。
secret, err := env.GetRequired("SECRET")       // 未设置或为空时返回错误。
token := env.MustGet("TOKEN")                  // 未设置或为空时 panic。
err = env.Set("LOG_LEVEL", "debug")           // 调用 os.Setenv。
```

## 文件格式

```dotenv
# 忽略注释和空行。
API_KEY=your-api-key
export DB_URL=postgres://localhost/db

MESSAGE="Hello World"
NAME='Single Quotes'
MULTILINE="Line1\nLine2"
```

解析器支持可选的 `export` 前缀、单引号或双引号值，以及双引号内的转义字符。它不是 Shell，不要依赖命令替换或 Shell 变量展开。
