[English](README.md) | [中文](README_zh.md)

# Environment variables

`pkg/env` loads `KEY=VALUE` pairs from a `.env` file into the current process and wraps common `os.Getenv` operations. Missing files are ignored by default, and existing environment variables are preserved unless `WithOverride` is set.

## Load a file

```go
import "github.com/openmodu/modu/pkg/env"

if err := env.Load(); err != nil {
	return err
}
```

`Load()` reads `.env` from the current directory. Options can change the path and failure behavior:

```go
err := env.Load(
	env.WithDir("/etc/myapp"),
	env.WithFile(".env.production"),
	env.WithOverride(),
	env.WithRequired(),
)
```

| Option | Effect |
|---|---|
| `WithFile(name)` | Use `name` instead of `.env` |
| `WithDir(dir)` | Resolve the file below `dir` |
| `WithOverride()` | Replace variables already present in the process |
| `WithRequired()` | Return an error when the file does not exist |

Use `MustLoad` only during startup when a configuration error should terminate the process:

```go
env.MustLoad(env.WithRequired())
```

## Read and set variables

```go
apiKey := env.Get("API_KEY")                    // Empty string when unset.
port := env.GetDefault("PORT", "8080")        // Default when unset or empty.
secret, err := env.GetRequired("SECRET")       // Error when unset or empty.
token := env.MustGet("TOKEN")                  // Panic when unset or empty.
err = env.Set("LOG_LEVEL", "debug")           // Delegates to os.Setenv.
```

## File format

```dotenv
# Comments and blank lines are ignored.
API_KEY=your-api-key
export DB_URL=postgres://localhost/db

MESSAGE="Hello World"
NAME='Single Quotes'
MULTILINE="Line1\nLine2"
```

The parser accepts an optional `export` prefix, single- or double-quoted values, and escaped characters in double-quoted values. It is a `.env` loader, not a shell: do not rely on command substitution or shell expansion.
