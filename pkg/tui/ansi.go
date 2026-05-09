package tui

import (
	"strconv"
	"strings"

	gotui "github.com/grindlemire/go-tui"
)

// stripANSIForGoTUI removes SGR escape sequences from s. go-tui's text layout
// is column-aware, and embedded escape codes confuse its width math, so any
// pre-rendered ANSI string must be stripped before being passed to a go-tui
// element's text content.
func stripANSIForGoTUI(s string) string {
	return uiANSIPattern.ReplaceAllString(s, "")
}

// ansiSegment is a stretch of text plus the go-tui style derived from any
// preceding SGR escape codes.
type ansiSegment struct {
	Text  string
	Style gotui.Style
}

// parseGoTUIANSIText splits s into styled segments, applying SGR sequences
// (CSI ... m) into a running gotui.Style. Used by tests and any future
// embedder that wants to feed pre-styled text into a go-tui layout.
func parseGoTUIANSIText(text string) []ansiSegment {
	var segments []ansiSegment
	style := gotui.NewStyle()
	for len(text) > 0 {
		idx := strings.Index(text, "\x1b[")
		if idx < 0 {
			if text != "" {
				segments = append(segments, ansiSegment{Text: text, Style: style})
			}
			break
		}
		if idx > 0 {
			segments = append(segments, ansiSegment{Text: text[:idx], Style: style})
			text = text[idx:]
			continue
		}
		end := strings.IndexByte(text, 'm')
		if end < 0 {
			segments = append(segments, ansiSegment{Text: text, Style: style})
			break
		}
		applyGoTUISGR(&style, text[2:end])
		text = text[end+1:]
	}
	return segments
}

func applyGoTUISGR(style *gotui.Style, params string) {
	if params == "" {
		*style = gotui.NewStyle()
		return
	}
	codes := parseSGRCodes(params)
	for i := 0; i < len(codes); i++ {
		switch code := codes[i]; code {
		case 0:
			*style = gotui.NewStyle()
		case 1:
			*style = style.Bold()
		case 2:
			*style = style.Dim()
		case 3:
			*style = style.Italic()
		case 4:
			*style = style.Underline()
		case 22:
			style.Attrs &^= gotui.AttrBold | gotui.AttrDim
		case 23:
			style.Attrs &^= gotui.AttrItalic
		case 24:
			style.Attrs &^= gotui.AttrUnderline
		case 39:
			style.Fg = gotui.DefaultColor()
		case 38:
			if i+1 >= len(codes) {
				continue
			}
			switch codes[i+1] {
			case 2:
				if i+4 < len(codes) {
					*style = style.Foreground(gotui.RGBColor(uint8(clampSGRByte(codes[i+2])), uint8(clampSGRByte(codes[i+3])), uint8(clampSGRByte(codes[i+4]))))
					i += 4
				}
			case 5:
				if i+2 < len(codes) {
					*style = style.Foreground(gotui.ANSIColor(uint8(clampSGRByte(codes[i+2]))))
					i += 2
				}
			}
		default:
			if color, ok := goTUIANSIColor(code); ok {
				*style = style.Foreground(color)
			}
		}
	}
}

func parseSGRCodes(params string) []int {
	parts := strings.Split(params, ";")
	codes := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			codes = append(codes, 0)
			continue
		}
		code, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		codes = append(codes, code)
	}
	return codes
}

func goTUIANSIColor(code int) (gotui.Color, bool) {
	switch code {
	case 30:
		return gotui.ANSIColor(0), true
	case 31:
		return gotui.Red, true
	case 32:
		return gotui.Green, true
	case 33:
		return gotui.Yellow, true
	case 34:
		return gotui.Blue, true
	case 35:
		return gotui.Magenta, true
	case 36:
		return gotui.Cyan, true
	case 37:
		return gotui.White, true
	case 90:
		return gotui.BrightBlack, true
	case 91:
		return gotui.BrightRed, true
	case 92:
		return gotui.BrightGreen, true
	case 93:
		return gotui.BrightYellow, true
	case 94:
		return gotui.BrightBlue, true
	case 95:
		return gotui.BrightMagenta, true
	case 96:
		return gotui.BrightCyan, true
	case 97:
		return gotui.BrightWhite, true
	default:
		return gotui.DefaultColor(), false
	}
}

func clampSGRByte(value int) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return value
}
