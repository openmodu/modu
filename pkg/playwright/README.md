# Playwright

Playwright Go 封装库，简化浏览器自动化操作。

## 安装

```go
import "github.com/openmodu/modu/pkg/playwright"

go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium
```

## 功能

- 浏览器管理（Chromium/Firefox/WebKit）
- 反检测脚本自动注入
- Cookie 持久化
- 简化的 Page 操作 API

## 使用

### 基础用法

```go
// 创建浏览器
browser, err := playwright.New()
if err != nil {
    log.Fatal(err)
}
defer browser.Close()

// 创建页面
page, err := browser.NewPage()
if err != nil {
    log.Fatal(err)
}
defer page.Close()

// 导航
page.Goto("https://example.com")

// 获取内容
html, _ := page.Content()
title, _ := page.Title()
```

### 配置选项

```go
// 浏览器选项
browser, _ := playwright.New(
    playwright.WithHeadless(false),        // 显示浏览器窗口
    playwright.WithBrowserType("firefox"), // 使用 Firefox
    playwright.WithSlowMo(100),            // 慢动作模式
)

// Context 选项
page, _ := browser.NewPage(
    playwright.WithUserAgent("Custom UA"),
    playwright.WithViewport(1920, 1080),
    playwright.WithLocale("zh-CN"),
    playwright.WithTimezone("Asia/Shanghai"),
    playwright.WithAntiDetect(true),       // 反检测（默认开启）
)
```

### 页面操作

```go
// 导航
page.Goto("https://example.com", playwright.WithWaitUntil("networkidle"))

// 等待元素
page.WaitForSelector(".loaded", 10000)

// 交互
page.Click("#button")
page.Fill("#input", "value")
page.Type("#search", "query", 50) // 带延迟输入
page.Press("#input", "Enter")

// 滚动
page.Scroll(0, 500)
page.ScrollToBottom()
page.ScrollToTop()

// 获取内容
text, _ := page.InnerText(".content")
html, _ := page.InnerHTML(".content")
attr, _ := page.GetAttribute("a", "href")

// 截图
page.Screenshot("screenshot.png", true) // fullPage=true

// 执行 JavaScript
result, _ := page.Evaluate(`() => document.title`)
```

### Cookie 持久化

```go
// 创建 Cookie Store
store := playwright.NewCookieStore("~/.myapp/cookies.json")

// 保存 Cookie
ctx, _ := browser.NewContext()
// ... 登录操作 ...
store.Save(ctx)

// 加载 Cookie
ctx2, _ := browser.NewContext()
store.Load(ctx2)

// 检查是否存在
if store.Exists() {
    // ...
}
```

### 访问原始对象

```go
// 获取原始 playwright-go 对象
rawBrowser := browser.Raw()
rawContext := ctx.Raw()
rawPage := page.Raw()
```

## API

### Browser

| 方法 | 说明 |
|------|------|
| `New(opts...)` | 创建浏览器实例 |
| `Close()` | 关闭浏览器 |
| `NewContext(opts...)` | 创建新 Context |
| `NewPage(opts...)` | 创建新页面（使用默认 Context） |
| `Raw()` | 获取原始 playwright.Browser |

### Page

| 方法 | 说明 |
|------|------|
| `Goto(url, opts...)` | 导航到 URL |
| `WaitForSelector(selector, timeout)` | 等待元素出现 |
| `Wait(duration)` | 等待指定时间 |
| `Click(selector)` | 点击元素 |
| `Fill(selector, value)` | 填充输入框 |
| `Type(selector, text, delay)` | 输入文本 |
| `Content()` | 获取页面 HTML |
| `Evaluate(js, args)` | 执行 JavaScript |
| `Screenshot(path, fullPage)` | 截图 |
| `Scroll(x, y)` | 滚动页面 |
| `Close()` | 关闭页面 |

### CookieStore

| 方法 | 说明 |
|------|------|
| `NewCookieStore(path)` | 创建 Cookie Store |
| `Save(ctx)` | 保存 Cookie 到文件 |
| `Load(ctx)` | 从文件加载 Cookie |
| `Exists()` | 检查文件是否存在 |
| `Delete()` | 删除 Cookie 文件 |
