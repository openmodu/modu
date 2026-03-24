# gen_image_repo

An abstraction layer for image generation services, providing a unified interface that supports multiple providers.

## Installation

```go
import genimagerepo "github.com/openmodu/modu/repos/gen_image_repo"
```

## Quick Start

```go
// Create a Gemini image generator
generator := genimagerepo.NewGeminiImageImpl(
    "https://generativelanguage.googleapis.com",
    "your-api-key",
)

// Generate an image
resp, err := generator.Generate(ctx, &genimagevo.GenImageRequest{
    UserPrompt:   "A cute cat sleeping in the sun",
    SystemPrompt: "Generate a high-quality image",  // Optional
})
if err != nil {
    log.Fatal(err)
}

// Process the result
for _, img := range resp.Images {
    // img.Data    - Binary image data
    // img.MimeType - MIME type (e.g., "image/png")
}

// Save to a file
genimagerepo.SaveImageToFile(resp.Images[0], "./output.png")
```

## Interface Definition

```go
type ImageGenRepo interface {
    Generate(ctx context.Context, req *genimagevo.GenImageRequest) (*genimagevo.GenImageResponse, error)
    Name() string
}
```

## Supported Providers

| Provider | Constructor |
|----------|----------|
| Gemini | `NewGeminiImageImpl(baseURL, apiKey)` |

## Request Parameters

```go
type GenImageRequest struct {
    UserPrompt   string  // User prompt (required)
    SystemPrompt string  // System prompt (optional)
}
```

## Response Structure

```go
type GenImageResponse struct {
    Images       []*Image      // List of generated images
    Model        string        // Model used
    ProviderName string        // Provider name
    Usage        *UsageInfo    // Token usage info
    RawResponse  any           // Raw response object
}

type Image struct {
    Data     []byte  // Binary image data
    MimeType string  // MIME type
}
```

## Utility Functions

```go
// Save an image to a file
genimagerepo.SaveImageToFile(image *genimagevo.Image, path string) error
```

## File Structure

```
gen_image_repo/
├── image_repo.go           # Interface definition
├── nano_banana_provider.go # Gemini (Nano Banana) Provider implementation
└── utils.go                # Utility functions
```
