package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/acp/manager"
)

// ExternalQuota holds usage data fetched from a provider's API for one agent.
type ExternalQuota struct {
	WeekTokens   int    `json:"weekTokens"`
	WeekRequests int    `json:"weekRequests"`
	Source       string `json:"source"` // "openai" | "anthropic" | ""
	FetchedAt    string `json:"fetchedAt"`
	Err          string `json:"err,omitempty"`
}

// QuotaCache stores the most recent external quota result per agent.
// All methods are safe for concurrent use.
type QuotaCache struct {
	mu   sync.RWMutex
	data map[string]*ExternalQuota
}

func NewQuotaCache() *QuotaCache {
	return &QuotaCache{data: make(map[string]*ExternalQuota)}
}

func (c *QuotaCache) Get(agentID string) *ExternalQuota {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data[agentID]
}

func (c *QuotaCache) set(agentID string, q *ExternalQuota) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[agentID] = q
}

// RefreshAll fetches quota for all agents that have a detectable API key.
func (c *QuotaCache) RefreshAll(agents []manager.AgentConfig) {
	wk := weekStart(time.Now().UTC())
	for _, ac := range agents {
		ac := ac
		go func() {
			q := fetchQuota(ac, wk)
			if q != nil {
				c.set(ac.ID, q)
			}
		}()
	}
}

// StartPoller launches a background goroutine that refreshes quota every interval.
func (c *QuotaCache) StartPoller(ctx context.Context, mgr *manager.Manager, interval time.Duration) {
	go func() {
		c.RefreshAll(mgr.Config().Agents)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.RefreshAll(mgr.Config().Agents)
			}
		}
	}()
}

// fetchQuota dispatches to the right provider based on agent ID.
func fetchQuota(ac manager.AgentConfig, wk time.Time) *ExternalQuota {
	id := strings.ToLower(ac.ID)
	switch {
	case strings.Contains(id, "codex") || strings.Contains(id, "openai"):
		key := resolveKey(ac, "OPENAI_API_KEY")
		if key == "" {
			return nil
		}
		return fetchOpenAI(key, wk)
	case strings.Contains(id, "claude") || strings.Contains(id, "anthropic"):
		key := resolveKey(ac, "ANTHROPIC_API_KEY")
		if key == "" {
			return nil
		}
		return fetchAnthropic(key, wk)
	}
	return nil
}

// resolveKey returns the API key from agent env, or falls back to os env.
func resolveKey(ac manager.AgentConfig, envName string) string {
	if v, ok := ac.Env[envName]; ok && v != "" {
		return v
	}
	return os.Getenv(envName)
}

// ── OpenAI ────────────────────────────────────────────────────────────────

func fetchOpenAI(apiKey string, wk time.Time) *ExternalQuota {
	q := &ExternalQuota{Source: "openai", FetchedAt: time.Now().UTC().Format(time.RFC3339)}
	cl := &http.Client{Timeout: 10 * time.Second}
	now := time.Now().UTC()

	for d := wk; !d.After(now); d = d.AddDate(0, 0, 1) {
		tokens, reqs, err := openAIDayUsage(cl, apiKey, d.Format("2006-01-02"))
		if err != nil {
			q.Err = err.Error()
			log.Printf("[quota] openai %s: %v", d.Format("2006-01-02"), err)
			return q
		}
		q.WeekTokens += tokens
		q.WeekRequests += reqs
	}
	return q
}

func openAIDayUsage(cl *http.Client, apiKey, date string) (tokens, reqs int, err error) {
	req, err := http.NewRequest("GET", "https://api.openai.com/v1/usage?date="+date, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := cl.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Data []struct {
			NContextTokensTotal   int `json:"n_context_tokens_total"`
			NGeneratedTokensTotal int `json:"n_generated_tokens_total"`
			NRequests             int `json:"n_requests"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, fmt.Errorf("parse: %w", err)
	}
	for _, row := range result.Data {
		tokens += row.NContextTokensTotal + row.NGeneratedTokensTotal
		reqs += row.NRequests
	}
	return tokens, reqs, nil
}

// ── Anthropic ─────────────────────────────────────────────────────────────

func fetchAnthropic(apiKey string, wk time.Time) *ExternalQuota {
	q := &ExternalQuota{Source: "anthropic", FetchedAt: time.Now().UTC().Format(time.RFC3339)}
	cl := &http.Client{Timeout: 10 * time.Second}

	startDate := wk.Format("2006-01-02")
	endDate := time.Now().UTC().Format("2006-01-02")
	url := fmt.Sprintf(
		"https://api.anthropic.com/v1/organizations/usage?start_date=%s&end_date=%s",
		startDate, endDate,
	)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		q.Err = err.Error()
		return q
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := cl.Do(req)
	if err != nil {
		q.Err = err.Error()
		return q
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		q.Err = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return q
	}

	var result struct {
		Data []struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			Requests     int `json:"request_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		q.Err = fmt.Sprintf("parse: %v", err)
		return q
	}
	for _, row := range result.Data {
		q.WeekTokens += row.InputTokens + row.OutputTokens
		q.WeekRequests += row.Requests
	}
	return q
}
