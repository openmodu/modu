package read

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

var imageExtensions = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

var binaryExtensions = map[string]struct{}{
	".7z": {}, ".a": {}, ".aac": {}, ".aiff": {}, ".app": {}, ".avi": {},
	".bin": {}, ".bmp": {}, ".bz2": {}, ".deb": {}, ".dll": {}, ".doc": {},
	".docx": {}, ".dylib": {}, ".eot": {}, ".exe": {}, ".flac": {}, ".gif": {},
	".gz": {}, ".ico": {}, ".iso": {}, ".jpeg": {}, ".jpg": {}, ".lib": {},
	".m4a": {}, ".m4v": {}, ".mkv": {}, ".mov": {}, ".mp3": {}, ".mp4": {},
	".mpeg": {}, ".mpg": {}, ".msi": {}, ".o": {}, ".obj": {}, ".odp": {},
	".ods": {}, ".odt": {}, ".ogg": {}, ".opus": {}, ".otf": {}, ".pdf": {},
	".png": {}, ".ppt": {}, ".pptx": {}, ".pyc": {}, ".rar": {}, ".rpm": {},
	".so": {}, ".tar": {}, ".tgz": {}, ".tif": {}, ".tiff": {}, ".ttf": {},
	".wav": {}, ".webm": {}, ".webp": {}, ".wma": {}, ".wmv": {}, ".woff": {},
	".woff2": {}, ".xls": {}, ".xlsx": {}, ".xz": {}, ".z": {}, ".zip": {},
}

const binaryCheckBytes = 8192
const largeNotebookOutputThreshold = 10000
const thinScreenshotSpace = "\u202f"

const cyberRiskMitigationReminder = "\n\n<system-reminder>\nWhenever you read a file, you should consider whether it would be considered malware. You CAN and SHOULD provide analysis of malware, what it is doing. But you MUST refuse to improve or augment the code. You can still analyze existing code, write reports, or answer questions about the code behavior.\n</system-reminder>"

var blockedDevicePaths = map[string]struct{}{
	"/dev/zero":    {},
	"/dev/random":  {},
	"/dev/urandom": {},
	"/dev/full":    {},
	"/dev/stdin":   {},
	"/dev/tty":     {},
	"/dev/console": {},
	"/dev/stdout":  {},
	"/dev/stderr":  {},
	"/dev/fd/0":    {},
	"/dev/fd/1":    {},
	"/dev/fd/2":    {},
}

var procFDPathPattern = regexp.MustCompile(`^/proc/(?:self|\d+)/fd/[0-2]$`)

type notebookFile struct {
	Metadata struct {
		LanguageInfo struct {
			Name string `json:"name"`
		} `json:"language_info"`
	} `json:"metadata"`
	Cells []notebookCell `json:"cells"`
}

type notebookCell struct {
	CellType       string           `json:"cell_type"`
	Source         any              `json:"source"`
	ID             string           `json:"id"`
	ExecutionCount *int             `json:"execution_count"`
	Outputs        []notebookOutput `json:"outputs"`
}

type notebookOutput struct {
	OutputType string         `json:"output_type"`
	Text       any            `json:"text"`
	Data       map[string]any `json:"data"`
	EName      string         `json:"ename"`
	EValue     string         `json:"evalue"`
	Traceback  []string       `json:"traceback"`
}

// ReadTool implements the file reading tool.
type ReadTool struct {
	cwd       string
	readState *common.FileReadState
}

func NewTool(cwd string) types.Tool {
	return &ReadTool{cwd: cwd}
}

func NewTrackedTool(cwd string, readState *common.FileReadState) types.Tool {
	return &ReadTool{cwd: cwd, readState: readState}
}

func (t *ReadTool) Name() string  { return "read" }
func (t *ReadTool) Label() string { return "Read File" }
func (t *ReadTool) Description() string {
	return `Read a file from the local filesystem.

Usage:
- Use this tool to inspect known files; prefer it over bash commands such as cat, head, tail, or sed.
- The path may be absolute or relative to the working directory.
- By default it reads up to 2000 lines from the beginning. Use offset and limit when you only need a specific section of a large file.
- Numeric strings such as "10" are accepted for offset and limit.
- Results are returned with 1-based line numbers in "line<TAB>content" format. Do not include the line-number prefix when later using edit old_text.
- This tool reads text files, Jupyter notebooks, and images, not directories or other binary files. Use ls to inspect a directory.
- Large text files without an explicit limit return an error; use offset and limit to read a targeted range.
- Empty files return a system-reminder warning instead of a numbered blank line.
- If offset is beyond the end of the file, the tool returns a system-reminder warning with the file's total line count.
- Files ending in a newline do not produce an extra numbered blank line.
- Text reads include a system-reminder to analyze potentially malicious code without improving it.
- Device files that would block or produce infinite output are rejected before reading.
- PNG, JPG, JPEG, GIF, and WEBP images are returned as base64-encoded image content.
- macOS screenshot names with regular or narrow no-break spaces before AM/PM are resolved interchangeably.
- Jupyter notebooks (.ipynb) are parsed into cell text plus supported text/image outputs. Large notebook cell outputs are replaced with jq guidance.`
}

func (t *ReadTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to read (absolute or relative to cwd)",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Alias for path, accepted for compatibility.",
			},
			"offset": map[string]any{
				"anyOf":       semanticReadIntegerSchema(0),
				"description": "Line number to start reading from (1-based). Optional.",
			},
			"limit": map[string]any{
				"anyOf":       semanticReadIntegerSchema(1),
				"description": "Maximum number of lines to read. Optional, defaults to 2000.",
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"path"}},
			{"required": []string{"file_path"}},
		},
	}
}

func semanticReadIntegerSchema(minimum int) []map[string]any {
	pattern := `^\d+$`
	if minimum > 0 {
		pattern = `^[1-9]\d*$`
	}

	return []map[string]any{
		{"type": "integer", "minimum": minimum},
		{"type": "string", "pattern": pattern},
	}
}

func (t *ReadTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pathArg, _ := args["path"].(string)
	if pathArg == "" {
		pathArg, _ = args["file_path"].(string)
	}
	if pathArg == "" {
		return common.ErrorResult("path is required"), nil
	}

	resolved, err := common.ResolveReadPath(pathArg, t.cwd)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to resolve path: %v", err)), nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			if altPath, ok := alternateScreenshotPath(resolved); ok {
				altInfo, altErr := os.Stat(altPath)
				if altErr == nil {
					resolved = altPath
					info = altInfo
				} else if !os.IsNotExist(altErr) {
					return common.ErrorResult(fmt.Sprintf("failed to stat file: %v", altErr)), nil
				}
			}
			if info == nil {
				return common.ErrorResult(fmt.Sprintf("file not found: %s", pathArg)), nil
			}
		} else {
			return common.ErrorResult(fmt.Sprintf("failed to stat file: %v", err)), nil
		}
	}

	if info.IsDir() {
		return common.ErrorResult(fmt.Sprintf("%s is a directory, not a file. Use ls to list directory contents.", pathArg)), nil
	}
	if isBlockedDevicePath(resolved) {
		return common.ErrorResult(fmt.Sprintf("Cannot read '%s': this device file would block or produce infinite output.", pathArg)), nil
	}

	ext := strings.ToLower(filepath.Ext(resolved))
	if ext == ".ipynb" {
		return t.readNotebook(resolved, pathArg, info)
	}
	if mimeType, isImage := imageExtensions[ext]; isImage {
		return t.readImage(resolved, mimeType)
	}
	if _, isBinary := binaryExtensions[ext]; isBinary {
		return common.ErrorResult(fmt.Sprintf("This tool cannot read binary files. The file appears to be a binary %s file. Please use appropriate tools for binary file analysis.", ext)), nil
	}

	return t.readText(resolved, pathArg, info, args)
}

func alternateScreenshotPath(path string) (string, bool) {
	for _, suffix := range []struct {
		from string
		to   string
	}{
		{from: " AM.png", to: thinScreenshotSpace + "AM.png"},
		{from: " PM.png", to: thinScreenshotSpace + "PM.png"},
		{from: thinScreenshotSpace + "AM.png", to: " AM.png"},
		{from: thinScreenshotSpace + "PM.png", to: " PM.png"},
	} {
		if strings.HasSuffix(path, suffix.from) {
			return strings.TrimSuffix(path, suffix.from) + suffix.to, true
		}
	}
	return "", false
}

func (t *ReadTool) readImage(path, mimeType string) (types.ToolResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to read image: %v", err)), nil
	}

	// Detect MIME type from content if needed
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(path))
		if mimeType == "" {
			mimeType = "image/png"
		}
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.ImageContent{
				Type:     "image",
				Data:     encoded,
				MimeType: mimeType,
			},
		},
	}, nil
}

func (t *ReadTool) readNotebook(path, displayPath string, info os.FileInfo) (types.ToolResult, error) {
	if info.Size() > common.ReadMaxBytes {
		return common.ErrorResult(fmt.Sprintf("notebook is too large to read at once: %s is %s, maximum is %s. Use bash with jq to read specific cells or outputs.", displayPath, common.FormatSize(info.Size()), common.FormatSize(common.ReadMaxBytes))), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to read notebook: %v", err)), nil
	}

	var notebook notebookFile
	if err := json.Unmarshal(data, &notebook); err != nil {
		return common.ErrorResult(fmt.Sprintf("notebook is not valid JSON: %v", err)), nil
	}

	language := notebook.Metadata.LanguageInfo.Name
	if language == "" {
		language = "python"
	}

	var content []types.ContentBlock
	imageCount := 0
	for i, cell := range notebook.Cells {
		cellText := formatNotebookCell(cell, i, language)
		appendNotebookText(&content, cellText)
		if cell.CellType == "code" && notebookOutputsAreLarge(cell.Outputs) {
			appendNotebookText(&content, fmt.Sprintf("\nOutputs are too large to include. Use bash with: cat %q | jq '.cells[%d].outputs'", displayPath, i))
			continue
		}
		for _, output := range cell.Outputs {
			outputBlocks := formatNotebookOutput(output)
			for _, block := range outputBlocks {
				if _, ok := block.(*types.ImageContent); ok {
					imageCount++
				}
				if textBlock, ok := block.(*types.TextContent); ok {
					appendNotebookText(&content, textBlock.Text)
					continue
				}
				content = append(content, block)
			}
		}
	}

	if len(content) == 0 {
		content = []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: "<system-reminder>Warning: the notebook exists but contains no cells.</system-reminder>",
			},
		}
	}

	return types.ToolResult{
		Content: content,
		Details: map[string]any{
			"path":       path,
			"size":       info.Size(),
			"type":       "notebook",
			"cells":      len(notebook.Cells),
			"images":     imageCount,
			"truncated":  false,
			"language":   language,
			"lineFormat": "notebook_cells",
		},
	}, nil
}

func (t *ReadTool) readText(path, displayPath string, info os.FileInfo, args map[string]any) (types.ToolResult, error) {
	// Parse offset and limit
	offset := 0
	_, hasExplicitOffset := args["offset"]
	if v, ok := args["offset"]; ok {
		offset, _ = common.ToSemanticInt(v)
		if offset < 0 {
			return common.ErrorResult("offset must be greater than or equal to 0"), nil
		}
		if offset > 0 {
			offset-- // Convert to 0-based
		}
	}
	limit := common.ReadMaxLines
	if v, ok := args["limit"]; ok {
		limit, _ = common.ToSemanticInt(v)
		if limit <= 0 {
			return common.ErrorResult("limit must be greater than 0"), nil
		}
	}
	_, hasExplicitLimit := args["limit"]

	isBinary, err := isLikelyBinaryFile(path)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to inspect file: %v", err)), nil
	}
	if isBinary {
		return common.ErrorResult("This tool cannot read binary files. The file appears to contain binary data. Please use appropriate tools for binary file analysis."), nil
	}

	if !hasExplicitLimit && info.Size() > common.ReadMaxBytes {
		return common.ErrorResult(fmt.Sprintf("file is too large to read at once: %s is %s, maximum is %s. Use offset and limit to read a specific portion.", displayPath, common.FormatSize(info.Size()), common.FormatSize(common.ReadMaxBytes))), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	rawContent := string(data)
	content := rawContent

	// Handle BOM
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	if content == "" {
		t.recordReadState(path, rawContent, info, hasExplicitOffset || hasExplicitLimit)
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{
					Type: "text",
					Text: "<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>",
				},
			},
			Details: map[string]any{
				"path":      path,
				"size":      info.Size(),
				"lines":     0,
				"truncated": false,
			},
		}, nil
	}

	lines := splitFileLines(content)

	// Apply offset
	if offset > 0 && offset < len(lines) {
		lines = lines[offset:]
	} else if offset >= len(lines) {
		t.recordReadState(path, rawContent, info, true)
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{
					Type: "text",
					Text: fmt.Sprintf("<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>", offset+1, len(lines)),
				},
			},
			Details: map[string]any{
				"path":      path,
				"size":      info.Size(),
				"lines":     len(lines),
				"offset":    offset + 1,
				"truncated": false,
			},
		}, nil
	}

	totalLines := len(lines)
	truncated := false

	// Apply limit
	if len(lines) > limit {
		lines = lines[:limit]
		truncated = true
	}
	t.recordReadState(path, rawContent, info, hasExplicitOffset || hasExplicitLimit || truncated)

	// Format with line numbers
	var sb strings.Builder
	startLine := offset + 1
	for i, line := range lines {
		fmt.Fprintf(&sb, "%d\t%s\n", startLine+i, line)
	}

	result := sb.String()

	if truncated {
		result += fmt.Sprintf("\n... (%d lines truncated, showing lines %d-%d of %d total)",
			totalLines-len(lines), startLine, startLine+len(lines)-1, totalLines+offset)
	}
	result += cyberRiskMitigationReminder

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: result,
			},
		},
		Details: map[string]any{
			"path":      path,
			"size":      info.Size(),
			"lines":     totalLines + offset,
			"truncated": truncated,
		},
	}, nil
}

func (t *ReadTool) recordReadState(path, content string, info os.FileInfo, partial bool) {
	if t.readState == nil {
		return
	}
	t.readState.Record(path, content, info.ModTime().UnixNano(), partial)
}

func splitFileLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func formatNotebookCell(cell notebookCell, index int, defaultLanguage string) string {
	cellID := cell.ID
	if cellID == "" {
		cellID = fmt.Sprintf("cell-%d", index)
	}

	var metadata strings.Builder
	if cell.CellType != "code" {
		fmt.Fprintf(&metadata, "<cell_type>%s</cell_type>", cell.CellType)
	} else if defaultLanguage != "python" {
		fmt.Fprintf(&metadata, "<language>%s</language>", defaultLanguage)
	}

	return fmt.Sprintf("<cell id=\"%s\">%s%s</cell id=\"%s\">", cellID, metadata.String(), notebookString(cell.Source), cellID)
}

func formatNotebookOutput(output notebookOutput) []types.ContentBlock {
	switch output.OutputType {
	case "stream":
		return notebookTextBlocks(processNotebookOutputText(notebookString(output.Text)))
	case "execute_result", "display_data":
		var blocks []types.ContentBlock
		if output.Data != nil {
			if text := processNotebookOutputText(notebookString(output.Data["text/plain"])); text != "" {
				blocks = append(blocks, &types.TextContent{Type: "text", Text: "\n" + text})
			}
			if data, mimeType := notebookImageData(output.Data); data != "" {
				blocks = append(blocks, &types.ImageContent{Type: "image", Data: data, MimeType: mimeType})
			}
		}
		return blocks
	case "error":
		message := output.EName
		if output.EValue != "" {
			if message != "" {
				message += ": "
			}
			message += output.EValue
		}
		if len(output.Traceback) > 0 {
			if message != "" {
				message += "\n"
			}
			message += strings.Join(output.Traceback, "\n")
		}
		return notebookTextBlocks(processNotebookOutputText(message))
	default:
		return nil
	}
}

func notebookOutputsAreLarge(outputs []notebookOutput) bool {
	size := 0
	for _, output := range outputs {
		size += len(notebookOutputText(output))
		if output.Data != nil {
			if image, _ := notebookImageData(output.Data); image != "" {
				size += len(image)
			}
		}
		if size > largeNotebookOutputThreshold {
			return true
		}
	}
	return false
}

func notebookOutputText(output notebookOutput) string {
	switch output.OutputType {
	case "stream":
		return notebookString(output.Text)
	case "execute_result", "display_data":
		if output.Data == nil {
			return ""
		}
		return notebookString(output.Data["text/plain"])
	case "error":
		message := output.EName
		if output.EValue != "" {
			if message != "" {
				message += ": "
			}
			message += output.EValue
		}
		if len(output.Traceback) > 0 {
			if message != "" {
				message += "\n"
			}
			message += strings.Join(output.Traceback, "\n")
		}
		return message
	default:
		return ""
	}
}

func notebookTextBlocks(text string) []types.ContentBlock {
	if text == "" {
		return nil
	}
	return []types.ContentBlock{&types.TextContent{Type: "text", Text: "\n" + text}}
}

func processNotebookOutputText(text string) string {
	if text == "" {
		return ""
	}
	truncated := common.TruncateTail(text, common.TruncateOptions{
		MaxLines: common.BashMaxLines,
		MaxBytes: common.DefaultMaxBytes,
	})
	if truncated.WasTruncated {
		return truncated.Message + truncated.Content
	}
	return truncated.Content
}

func notebookString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []string:
		return strings.Join(v, "")
	case []any:
		var sb strings.Builder
		for _, part := range v {
			if s, ok := part.(string); ok {
				sb.WriteString(s)
			}
		}
		return sb.String()
	default:
		return fmt.Sprint(v)
	}
}

func notebookImageData(data map[string]any) (string, string) {
	if value, ok := data["image/png"]; ok {
		return stripNotebookImageWhitespace(notebookString(value)), "image/png"
	}
	if value, ok := data["image/jpeg"]; ok {
		return stripNotebookImageWhitespace(notebookString(value)), "image/jpeg"
	}
	return "", ""
}

func stripNotebookImageWhitespace(data string) string {
	return strings.Join(strings.Fields(data), "")
}

func appendNotebookText(content *[]types.ContentBlock, text string) {
	if text == "" {
		return
	}
	if len(*content) > 0 {
		if previous, ok := (*content)[len(*content)-1].(*types.TextContent); ok {
			previous.Text += "\n" + text
			return
		}
	}
	*content = append(*content, &types.TextContent{Type: "text", Text: text})
}

func isBlockedDevicePath(filePath string) bool {
	if _, ok := blockedDevicePaths[filePath]; ok {
		return true
	}
	return procFDPathPattern.MatchString(filePath)
}

func isLikelyBinaryFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	buf := make([]byte, binaryCheckBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	return isLikelyBinary(buf[:n]), nil
}

func isLikelyBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	nonPrintable := 0
	for _, b := range data {
		if b == 0 {
			return true
		}
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(len(data)) > 0.1
}
