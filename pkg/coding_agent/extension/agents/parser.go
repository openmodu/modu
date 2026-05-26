package agents

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatterDelim is the line that opens and closes the YAML frontmatter
// block. Matches the convention used by pi-subagents and most Markdown
// processors (Jekyll, Hugo, etc.).
const frontmatterDelim = "---"

// ParseProfile reads one agent profile from r. The input must begin with a
// `---` line, contain a YAML frontmatter terminated by another `---`, and
// then a Markdown body that becomes the system prompt.
//
// Errors include missing delimiters, malformed YAML, wrong field types, and
// missing required fields (name, description).
func ParseProfile(r io.Reader) (*Profile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	return parseBytes(data)
}

func parseBytes(data []byte) (*Profile, error) {
	text := normalizeLineEndings(string(data))

	// Opening delimiter: the very first line must be `---`.
	if !strings.HasPrefix(text, frontmatterDelim+"\n") {
		return nil, fmt.Errorf("missing opening %q delimiter at start of file", frontmatterDelim)
	}
	rest := text[len(frontmatterDelim)+1:]

	// Closing delimiter: a `---` on its own line ends the frontmatter.
	closeIdx := strings.Index(rest, "\n"+frontmatterDelim+"\n")
	// Also accept a trailing `---` with no following newline (file ends
	// exactly at the delimiter line) — uncommon but legal.
	endTrailing := false
	if closeIdx < 0 {
		if strings.HasSuffix(rest, "\n"+frontmatterDelim) {
			closeIdx = len(rest) - len(frontmatterDelim) - 1
			endTrailing = true
		} else {
			return nil, fmt.Errorf("missing closing %q delimiter", frontmatterDelim)
		}
	}

	fmRaw := rest[:closeIdx]
	var body string
	if endTrailing {
		body = ""
	} else {
		body = rest[closeIdx+len("\n"+frontmatterDelim+"\n"):]
	}

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(fmRaw), &raw); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}

	p := &Profile{Extra: map[string]any{}}
	if err := extractFields(raw, p); err != nil {
		return nil, err
	}
	p.SystemPrompt = strings.TrimSpace(body)

	if p.Name == "" {
		return nil, fmt.Errorf("missing required frontmatter field: name")
	}
	if p.Description == "" {
		return nil, fmt.Errorf("missing required frontmatter field: description")
	}
	return p, nil
}

// extractFields walks the raw frontmatter map. Known keys land in typed
// fields on p; everything else is preserved verbatim in p.Extra so
// consumer extensions can read their own conventions out of it later.
func extractFields(raw map[string]any, p *Profile) error {
	for k, v := range raw {
		switch k {
		case "name":
			s, err := asString(k, v)
			if err != nil {
				return err
			}
			p.Name = s
		case "description":
			s, err := asString(k, v)
			if err != nil {
				return err
			}
			p.Description = s
		case "tools":
			tools, err := parseTools(v)
			if err != nil {
				return err
			}
			p.Tools = tools
		case "thinking":
			s, err := asString(k, v)
			if err != nil {
				return err
			}
			p.Thinking = s
		case "model":
			s, err := asString(k, v)
			if err != nil {
				return err
			}
			p.Model = s
		default:
			p.Extra[k] = v
		}
	}
	return nil
}

func asString(field string, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %T", field, v)
	}
	return s, nil
}

// parseTools accepts either a comma-separated string ("read, grep, bash")
// or a YAML list (`[read, grep]` / explicit `- read` block style).
// Both forms trim whitespace and drop empty entries.
func parseTools(v any) ([]string, error) {
	switch t := v.(type) {
	case string:
		parts := strings.Split(t, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("tools[%d]: expected string, got %T", i, item)
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tools must be a string or list, got %T", v)
	}
}

// normalizeLineEndings rewrites CRLF and bare CR to LF so the delimiter
// scanning logic doesn't need to special-case Windows / classic-Mac files.
func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}
