// Package agents loads Markdown-based agent profiles inspired by the
// pi-subagents project: each profile is one `<name>.md` file with a YAML
// frontmatter block declaring metadata, followed by a Markdown body that
// becomes the agent's system prompt.
//
// This package only parses and validates profiles. It deliberately does NOT
// know about how profiles get bound to a CodingSession or which tools
// "actually exist" — that mapping belongs to whatever extension consumes
// the profiles (planned for phase 3: subagent extension).
package agents

// Profile is one parsed agent definition.
type Profile struct {
	// Name uniquely identifies the agent within a directory load. Required.
	Name string
	// Description is a one-line summary used by the model to pick which
	// agent to dispatch to. Required.
	Description string
	// Tools is the per-profile tool whitelist. Empty means "use whatever
	// the caller's default is". A single "*" entry means "every tool the
	// caller exposes" — call AllTools() to test for this case.
	Tools []string
	// Thinking matches pkg/types.ThinkingLevel values when set
	// ("off"/"low"/"medium"/"high"), but is stored as a raw string here so
	// new levels added upstream don't need a parser change.
	Thinking string
	// Model is an optional provider/model id that overrides the caller's
	// default for this profile. Empty means "inherit".
	Model string
	// SystemPrompt is the Markdown body, trimmed of surrounding whitespace.
	SystemPrompt string
	// Extra holds any frontmatter keys this package did not recognize.
	// Consumer extensions can read their own fields out of this map
	// without forcing a parser change here.
	Extra map[string]any
	// SourcePath is the on-disk path the profile was loaded from.
	// Empty when ParseProfile is called on an in-memory reader.
	SourcePath string
}

// AllTools reports whether the profile asks for every tool the caller
// offers, expressed in frontmatter as `tools: ["*"]` (or `tools: "*"`).
// Specific tool names alongside "*" do not count — that combination is
// treated as a regular whitelist.
func (p *Profile) AllTools() bool {
	return len(p.Tools) == 1 && p.Tools[0] == "*"
}
