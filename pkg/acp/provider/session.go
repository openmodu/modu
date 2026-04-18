package provider

import (
	"fmt"
)

// initialize performs the ACP protocol handshake. Must be called once,
// before any session/* methods. Caller holds Provider.mu.
func (p *Provider) initialize() error {
	_, err := p.client.Request("initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo": map[string]any{
			"name":    p.name,
			"version": p.version,
		},
		// Capabilities we — the "client" from the agent's perspective —
		// expose back to the agent. Supporting fs lets Claude Code etc.
		// read/write files via reverse-RPC; supporting permission lets
		// the agent ask us to approve tool calls.
		"capabilities": map[string]any{
			"fs": map[string]bool{
				"readTextFile":  true,
				"writeTextFile": true,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("acp/provider: initialize: %w", err)
	}
	return nil
}

// newSession creates a fresh ACP session anchored at p.cwd and returns
// its sessionId. Caller holds Provider.mu.
func (p *Provider) newSession() (string, error) {
	resp, err := p.client.Request("session/new", map[string]any{
		"cwd":        p.cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return "", fmt.Errorf("acp/provider: session/new: %w", err)
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := resp.ParseResult(&result); err != nil {
		return "", fmt.Errorf("acp/provider: parse session/new result: %w", err)
	}
	if result.SessionID == "" {
		return "", fmt.Errorf("acp/provider: session/new returned empty sessionId")
	}
	return result.SessionID, nil
}

// cancelSession best-effort sends session/cancel. Any error is swallowed
// because the caller is already in a shutdown path.
func (p *Provider) cancelSession() error {
	p.mu.Lock()
	sid := p.sessionID
	p.mu.Unlock()
	if sid == "" {
		return nil
	}
	return p.client.Notify("session/cancel", map[string]any{"sessionId": sid})
}
