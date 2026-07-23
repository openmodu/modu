package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveModuTUIPastedImagesLoadsDraggedPaths(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "screen shot.png")
	second := filepath.Join(dir, "diagram.jpg")
	if err := os.WriteFile(first, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, tinyJPEG, 0o600); err != nil {
		t.Fatal(err)
	}

	content := strings.ReplaceAll(first, " ", `\ `) + " " + second + " "
	images, handled, err := resolveModuTUIPastedImages(dir, content)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("supported image paths should be handled as attachments")
	}
	if len(images) != 2 {
		t.Fatalf("images = %#v", images)
	}
	if images[0].Name != "screen shot.png" || images[0].MimeType != "image/png" {
		t.Fatalf("first image = %#v", images[0])
	}
	if images[1].Name != "diagram.jpg" || images[1].MimeType != "image/jpeg" {
		t.Fatalf("second image = %#v", images[1])
	}
}

func TestResolveModuTUIPastedImagesLeavesOrdinaryTextAlone(t *testing.T) {
	for _, content := range []string{
		"please inspect /tmp/screenshot.png",
		"not-an-image.txt",
		"hello world",
	} {
		images, handled, err := resolveModuTUIPastedImages(t.TempDir(), content)
		if err != nil || handled || len(images) != 0 {
			t.Fatalf("ordinary paste %q resolved as images=%#v handled=%v err=%v", content, images, handled, err)
		}
	}
}

func TestLoadModuTUIImageRejectsUnsupportedOrOversizedData(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "fake.png")
	if err := os.WriteFile(textPath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadModuTUIImage(textPath); err == nil {
		t.Fatal("fake PNG should be rejected")
	}

	largePath := filepath.Join(dir, "large.png")
	large := append(append([]byte(nil), tinyPNG...), make([]byte, maxModuTUIImageBytes)...)
	if err := os.WriteFile(largePath, large, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadModuTUIImage(largePath); err == nil || !strings.Contains(err.Error(), "5MB") {
		t.Fatalf("oversized image error = %v", err)
	}
}

// Complete one-pixel image fixtures are unnecessary here: MIME detection only
// needs the canonical file signatures used by supported model APIs.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
}

var tinyJPEG = []byte{
	0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46,
	0x49, 0x46, 0x00, 0x01,
}
