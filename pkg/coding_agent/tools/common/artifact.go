package common

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type PreviewStrategy string

const (
	PreviewHead PreviewStrategy = "head"
	PreviewTail PreviewStrategy = "tail"
)

type ArtifactStore struct {
	Dir string
}

type ArtifactRef struct {
	ID    string
	Path  string
	Bytes int
}

func NewArtifactStore(dir string) *ArtifactStore {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return &ArtifactStore{Dir: dir}
}

func (s *ArtifactStore) Put(toolCallID, name string, data []byte) (ArtifactRef, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return ArtifactRef{}, fmt.Errorf("artifact store is not configured")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return ArtifactRef{}, err
	}
	id := sanitizeArtifactPart(toolCallID)
	if id == "" {
		id = "tool-call"
	}
	suffix := sanitizeArtifactPart(name)
	if suffix == "" {
		suffix = "output"
	}
	path := filepath.Join(s.Dir, id+"."+suffix)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return ArtifactRef{}, err
	}
	return ArtifactRef{ID: id, Path: path, Bytes: len(data)}, nil
}

func (s *ArtifactStore) Find(toolCallID string) (ArtifactRef, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return ArtifactRef{}, fmt.Errorf("artifact store is not configured")
	}
	id := sanitizeArtifactPart(toolCallID)
	if id == "" {
		return ArtifactRef{}, fmt.Errorf("call_id is required")
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return ArtifactRef{}, err
	}
	prefix := id + "."
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return ArtifactRef{}, fmt.Errorf("artifact not found for call_id %q", toolCallID)
	}
	sort.Strings(names)
	path := filepath.Join(s.Dir, names[0])
	info, err := os.Stat(path)
	if err != nil {
		return ArtifactRef{}, err
	}
	return ArtifactRef{ID: id, Path: path, Bytes: int(info.Size())}, nil
}

type TextPreviewOptions struct {
	ToolCallID    string
	ArtifactName  string
	ArtifactStore *ArtifactStore
	Strategy      PreviewStrategy
	MaxLines      int
	MaxBytes      int
}

type TextPreview struct {
	Text    string
	Details map[string]any
}

func PreviewText(raw string, opts TextPreviewOptions) TextPreview {
	return PreviewTextFrom(raw, raw, opts)
}

func PreviewTextFrom(raw, visible string, opts TextPreviewOptions) TextPreview {
	strategy := opts.Strategy
	if strategy == "" {
		strategy = PreviewTail
	}
	if opts.ArtifactName == "" {
		opts.ArtifactName = "output"
	}

	truncateOpts := TruncateOptions{MaxLines: opts.MaxLines, MaxBytes: opts.MaxBytes}
	var truncated TruncationResult
	switch strategy {
	case PreviewHead:
		truncated = TruncateHead(visible, truncateOpts)
	default:
		strategy = PreviewTail
		truncated = TruncateTail(visible, truncateOpts)
	}

	text := truncated.Content
	if truncated.WasTruncated {
		if strategy == PreviewHead {
			text = strings.TrimRight(truncated.Content, "\n") + "\n" + truncated.Message
		} else {
			text = truncated.Message + truncated.Content
		}
	}

	partial := raw != visible
	output := map[string]any{
		"truncated":  truncated.WasTruncated || partial,
		"rawBytes":   len([]byte(raw)),
		"rawLines":   countPreviewLines(raw),
		"shownBytes": len([]byte(truncated.Content)),
		"shownLines": truncated.KeptLines,
		"strategy":   string(strategy),
	}
	if (truncated.WasTruncated || partial) && opts.ArtifactStore != nil {
		ref, err := opts.ArtifactStore.Put(opts.ToolCallID, opts.ArtifactName, []byte(raw))
		if err != nil {
			output["artifactError"] = err.Error()
		} else {
			output["artifactId"] = ref.ID
			output["artifactPath"] = ref.Path
			output["artifactBytes"] = ref.Bytes
		}
	}

	return TextPreview{
		Text:    text,
		Details: map[string]any{"output": output},
	}
}

func countPreviewLines(s string) int {
	return len(strings.Split(s, "\n"))
}

func sanitizeArtifactPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-.")
	}
	return out
}
