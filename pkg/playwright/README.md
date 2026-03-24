# Playwright

A Playwright Go wrapper library to simplify browser automation operations.

## Installation

```go
import "github.com/openmodu/modu/pkg/playwright"

go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium
```

## Features

- Browser management (Chromium/Firefox/WebKit)
- Automatic anti-detection script injection
- Cookie persistence
- Simplified Page operation API

## Usage

### Basic Usage

```go
// Create a browser
browser, err := playwright.New()
if err != nil {
    log.Fatal(err)
}
defer browser.Close()

// Create a page
page, err := browser.NewPage()
if err != nil {
    log.Fatal(err)
}
defer page.Close()

// Navigation
page.Goto("https://example.com")

// Get content
html, _ := page.Content()
title, _ := page.Title()
```

### Configuration Options

```go
// Browser options
browser, _ := playwright.New(
    playwright.WithHeadless(false),        // Show browser window
    playwright.WithBrowserType("firefox"), // Use Firefox
    playwright.WithSlowMo(100),            // Slow-motion mode
)

// Context options
page, _ := browser.NewPage(
    playwright.WithUserAgent("Custom UA"),
    playwright.WithViewport(1920, 1080),
    playwright.WithLocale("en-US"),
    playwright.WithTimezone("UTC"),
    playwright.WithAntiDetect(true),       // Anti-detection (enabled by default)
)
```

### Page Operations

```go
// Navigation
page.Goto("https://example.com", playwright.WithWaitUntil("networkidle"))

// Wait for selector
page.WaitForSelector(".loaded", 10000)

// Interaction
page.Click("#button")
page.Fill("#input", "value")
page.Type("#search", "query", 50) // Type with delay
page.Press("#input", "Enter")

// Scrolling
page.Scroll(0, 500)
page.ScrollToBottom()
page.ScrollToTop()

// Get content
text, _ := page.InnerText(".content")
html, _ := page.InnerHTML(".content")
attr, _ := page.GetAttribute("a", "href")

// Screenshot
page.Screenshot("screenshot.png", true) // fullPage=true

// Execute JavaScript
result, _ := page.Evaluate(`() => document.title`)
```

### Cookie Persistence

```go
// Create a Cookie Store
store := playwright.NewCookieStore("~/.myapp/cookies.json")

// Save Cookies
ctx, _ := browser.NewContext()
// ... login operations ...
store.Save(ctx)

// Load Cookies
ctx2, _ := browser.NewContext()
store.Load(ctx2)

// Check if exists
if store.Exists() {
    // ...
}
```

### Access Raw Objects

```go
// Get the raw playwright-go objects
rawBrowser := browser.Raw()
rawContext := ctx.Raw()
rawPage := page.Raw()
```

## API

### Browser

| Method | Description |
|------|------|
| `New(opts...)` | Create a browser instance |
| `Close()` | Close the browser |
| `NewContext(opts...)` | Create a new Context |
| `NewPage(opts...)` | Create a new page (using default Context) |
| `Raw()` | Get the raw `playwright.Browser` |

### Page

| Method | Description |
|------|------|
| `Goto(url, opts...)` | Navigate to a URL |
| `WaitForSelector(selector, timeout)` | Wait for an element to appear |
| `Wait(duration)` | Wait for a specific duration |
| `Click(selector)` | Click an element |
| `Fill(selector, value)` | Fill an input field |
| `Type(selector, text, delay)` | Type text |
| `Content()` | Get page HTML content |
| `Evaluate(js, args)` | Execute JavaScript |
| `Screenshot(path, fullPage)` | Take a screenshot |
| `Scroll(x, y)` | Scroll the page |
| `Close()` | Close the page |

### CookieStore

| Method | Description |
|------|------|
| `NewCookieStore(path)` | Create a Cookie Store |
| `Save(ctx)` | Save cookies from a context to a file |
| `Load(ctx)` | Load cookies from a file into a context |
| `Exists()` | Check if the cookie file exists |
| `Delete()` | Delete the cookie file |
