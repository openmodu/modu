# gen_image_repo

图像生成服务抽象层，提供统一的图像生成接口，支持多个 Provider。

## 安装

```go
import genimagerepo "github.com/openmodu/modu/repos/gen_image_repo"
```

## 快速开始

```go
// 创建 Gemini 图像生成器
generator := genimagerepo.NewGeminiImageImpl(
    "https://generativelanguage.googleapis.com",
    "your-api-key",
)

// 生成图像
resp, err := generator.Generate(ctx, &genimagevo.GenImageRequest{
    UserPrompt:   "一只可爱的猫咪在阳光下睡觉",
    SystemPrompt: "生成高质量的图片",  // 可选
})
if err != nil {
    log.Fatal(err)
}

// 处理结果
for _, img := range resp.Images {
    // img.Data    - 图像二进制数据
    // img.MimeType - 图像类型 (如 "image/png")
}

// 保存到文件
genimagerepo.SaveImageToFile(resp.Images[0], "./output.png")
```

## 接口定义

```go
type ImageGenRepo interface {
    Generate(ctx context.Context, req *genimagevo.GenImageRequest) (*genimagevo.GenImageResponse, error)
    Name() string
}
```

## 支持的 Provider

| Provider | 构造函数 |
|----------|----------|
| Gemini | `NewGeminiImageImpl(baseURL, apiKey)` |

## 请求参数

```go
type GenImageRequest struct {
    UserPrompt   string  // 用户提示词（必填）
    SystemPrompt string  // 系统提示词（可选）
}
```

## 响应结构

```go
type GenImageResponse struct {
    Images       []*Image      // 生成的图像列表
    Model        string        // 使用的模型
    ProviderName string        // Provider 名称
    Usage        *UsageInfo    // Token 使用量
    RawResponse  any           // 原始响应
}

type Image struct {
    Data     []byte  // 图像二进制数据
    MimeType string  // MIME 类型
}
```

## 工具函数

```go
// 保存图像到文件
genimagerepo.SaveImageToFile(image *genimagevo.Image, path string) error
```

## 文件结构

```
gen_image_repo/
├── image_repo.go           # 接口定义
├── nano_banana_provider.go # Gemini Provider 实现
└── utils.go                # 工具函数
```

