package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

const maxModuTUIImageBytes = 5 << 20

var moduTUIImageExtensions = map[string]struct{}{
	".png":  {},
	".jpg":  {},
	".jpeg": {},
	".gif":  {},
	".webp": {},
}

var moduTUIImageMIMETypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

func resolveModuTUIPastedImages(cwd, content string) ([]modutui.ImageAttachment, bool, error) {
	paths, ok := splitModuTUIImagePaths(content)
	if !ok || len(paths) == 0 {
		return nil, false, nil
	}
	for _, path := range paths {
		if _, supported := moduTUIImageExtensions[strings.ToLower(filepath.Ext(path))]; !supported {
			return nil, false, nil
		}
	}

	images := make([]modutui.ImageAttachment, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		image, err := loadModuTUIImage(path)
		if err != nil {
			return nil, true, err
		}
		images = append(images, image)
	}
	return images, true, nil
}

func splitModuTUIImagePaths(content string) ([]string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, false
	}

	var (
		paths   []string
		current strings.Builder
		quote   rune
		escaped bool
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		path := current.String()
		current.Reset()
		if strings.HasPrefix(path, "file://") {
			if parsed, err := url.Parse(path); err == nil && parsed.Path != "" {
				path = parsed.Path
			}
		}
		paths = append(paths, path)
	}

	for _, r := range content {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' {
				escaped = true
			} else {
				current.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '\\':
			escaped = true
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped || quote != 0 {
		return nil, false
	}
	flush()
	return paths, len(paths) > 0
}

func loadModuTUIImage(path string) (modutui.ImageAttachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return modutui.ImageAttachment{}, fmt.Errorf("read image %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return modutui.ImageAttachment{}, fmt.Errorf("image path %q is not a regular file", path)
	}
	if info.Size() > maxModuTUIImageBytes {
		return modutui.ImageAttachment{}, fmt.Errorf("image %q exceeds the 5MB limit", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return modutui.ImageAttachment{}, fmt.Errorf("read image %q: %w", path, err)
	}
	return moduTUIImageAttachment(filepath.Base(path), data)
}

func moduTUIImageAttachment(name string, data []byte) (modutui.ImageAttachment, error) {
	if len(data) == 0 {
		return modutui.ImageAttachment{}, fmt.Errorf("image %q is empty", name)
	}
	if len(data) > maxModuTUIImageBytes {
		return modutui.ImageAttachment{}, fmt.Errorf("image %q exceeds the 5MB limit", name)
	}
	mimeType := http.DetectContentType(data)
	ext, ok := moduTUIImageMIMETypes[mimeType]
	if !ok {
		return modutui.ImageAttachment{}, fmt.Errorf("image %q has unsupported content type %q", name, mimeType)
	}
	if strings.TrimSpace(name) == "" {
		name = "clipboard" + ext
	}
	return modutui.ImageAttachment{
		Name:     name,
		MimeType: mimeType,
		Data:     append([]byte(nil), data...),
	}, nil
}

func readModuTUIClipboardImages() ([]modutui.ImageAttachment, error) {
	data, err := readModuTUIClipboardImageBytes()
	if err != nil {
		return nil, err
	}
	image, err := moduTUIImageAttachment("", data)
	if err != nil {
		return nil, err
	}
	return []modutui.ImageAttachment{image}, nil
}

func readModuTUIClipboardImageBytes() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return readModuTUIMacClipboardImage()
	case "linux":
		return readModuTUILinuxClipboardImage()
	case "windows":
		return readModuTUIWindowsClipboardImage()
	default:
		return nil, fmt.Errorf("clipboard image paste is not supported on %s", runtime.GOOS)
	}
}

func readModuTUIMacClipboardImage() ([]byte, error) {
	tmp, err := os.CreateTemp("", "modu-code-clipboard-*.png")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	defer os.Remove(path)

	const script = `on run argv
set outputPath to item 1 of argv
try
	set imageData to the clipboard as «class PNGf»
	set outputFile to open for access POSIX file outputPath with write permission
	set eof outputFile to 0
	write imageData to outputFile
	close access outputFile
on error errMsg
	try
		close access POSIX file outputPath
	end try
	error errMsg
end try
end run`
	if output, err := exec.Command("osascript", "-e", script, path).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("clipboard has no PNG image: %s", strings.TrimSpace(string(output)))
	}
	return os.ReadFile(path)
}

func readModuTUILinuxClipboardImage() ([]byte, error) {
	type attempt struct {
		name string
		args []string
	}
	var attempts []attempt
	for mimeType := range moduTUIImageMIMETypes {
		attempts = append(attempts,
			attempt{name: "wl-paste", args: []string{"--no-newline", "--type", mimeType}},
			attempt{name: "xclip", args: []string{"-selection", "clipboard", "-t", mimeType, "-o"}},
		)
	}
	for _, candidate := range attempts {
		data, err := exec.Command(candidate.name, candidate.args...).Output()
		if err == nil && len(data) > 0 {
			return data, nil
		}
	}
	return nil, fmt.Errorf("clipboard has no supported image (install wl-clipboard or xclip if needed)")
}

func readModuTUIWindowsClipboardImage() ([]byte, error) {
	const script = `Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$image = [Windows.Forms.Clipboard]::GetImage()
if ($null -eq $image) { exit 2 }
$stream = New-Object IO.MemoryStream
$image.Save($stream, [Drawing.Imaging.ImageFormat]::Png)
$bytes = $stream.ToArray()
[Console]::OpenStandardOutput().Write($bytes, 0, $bytes.Length)`
	data, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("clipboard has no supported image")
	}
	return data, nil
}
