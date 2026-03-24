# env

A lightweight environment variable loading library that supports reading configuration from `.env` files. It uses the Functional Options pattern for a concise and flexible API.

## Installation

```go
import "github.com/openmodu/modu/pkg/env"
```

## Quick Start

```go
// Load .env file from the current directory
env.Load()

// Get environment variables
apiKey := env.Get("API_KEY")
port := env.GetDefault("PORT", "8080")
```

## Usage Examples

### Basic Usage

```go
// Default: Load .env (does not override existing variables)
env.Load()

// Load a specific file
env.Load(env.WithFile(".env.local"))

// Load from a specific directory
env.Load(env.WithDir("/etc/myapp"))

// Override existing environment variables
env.Load(env.WithOverride())

// Require the file to exist
env.Load(env.WithRequired())
```

### Combined Options

```go
env.Load(
    env.WithFile(".env.production"),
    env.WithDir("/etc/myapp"),
    env.WithOverride(),
    env.WithRequired(),
)
```

### Retrieving Variables

```go
// Get, returns an empty string if it doesn't exist
value := env.Get("KEY")

// Get with a default value
port := env.GetDefault("PORT", "8080")

// Must exist, otherwise returns an error
secret, err := env.GetRequired("SECRET")

// Must exist, otherwise panic
token := env.MustGet("TOKEN")
```

### Panic Versions

```go
// Panic if loading fails (e.g., file not found with WithRequired)
env.MustLoad(env.WithRequired())
```

## Options List

| Option | Description |
|------|------|
| `WithFile(name)` | Specify the filename (default: `.env`) |
| `WithDir(dir)` | Specify the directory |
| `WithOverride()` | Override existing environment variables |
| `WithRequired()` | The file must exist, otherwise an error is returned |

## .env File Format

```env
# Comment
API_KEY=your-api-key
export DB_URL=postgres://localhost/db

# Quoted values
MESSAGE="Hello World"
NAME='Single Quotes'

# Escaped characters
MULTILINE="Line1\nLine2"
```

## API

| Function | Description |
|------|------|
| `Load(opts...)` | Load environment variables |
| `MustLoad(opts...)` | Load, panic on failure |
| `Get(key)` | Get variable |
| `GetDefault(key, def)` | Get with default value |
| `GetRequired(key)` | Must exist, returns error if not |
| `MustGet(key)` | Must exist, panic if not |
| `Set(key, value)` | Set variable |
