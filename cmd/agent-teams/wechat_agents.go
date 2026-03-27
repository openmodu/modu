package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
)

// ContentConfig holds LLM connection settings for the wechat content team.
type ContentConfig struct {
	APIURL  string // OpenAI-compatible base URL (e.g. http://localhost:1234/v1)
	APIKey  string // API key; empty is fine for LM Studio
	ModelID string // model identifier as known by the server
	WorkDir string // workspace root for saving articles
}

func defaultContentConfig() ContentConfig {
	url := os.Getenv("CONTENT_API_URL")
	if url == "" {
		url = "http://localhost:1234/v1"
	}
	model := os.Getenv("CONTENT_MODEL")
	if model == "" {
		model = "local-model"
	}
	return ContentConfig{
		APIURL:  url,
		APIKey:  os.Getenv("CONTENT_API_KEY"),
		ModelID: model,
		WorkDir: "./workspace",
	}
}

var wechatAgentDefs = []struct {
	id     string
	role   string
	prompt string
}{
	{"wc-researcher", "热点研究员", WechatResearcherPrompt},
	{"wc-editor", "主编", WechatEditorPrompt},
	{"wc-copywriter", "主笔", WechatCopywriterPrompt},
	{"wc-reviewer", "审稿编辑", WechatReviewerPrompt},
}

// startWechatTeam registers all content agents with the hub and spawns their goroutines.
func startWechatTeam(ctx context.Context, hub *mailbox.Hub, cfg ContentConfig) {
	for _, a := range wechatAgentDefs {
		hub.Register(a.id)
		_ = hub.SetAgentRole(a.id, a.role)
	}
	for _, a := range wechatAgentDefs {
		go runContentAgent(ctx, hub, a.id, a.role, a.prompt, cfg)
	}
	log.Printf("[wechat] team started (api=%s model=%s)", cfg.APIURL, cfg.ModelID)
}

// runContentAgent is the main loop for one content agent.
// It polls hub.Recv(), calls the LLM for task_assign messages, and reports results.
func runContentAgent(ctx context.Context, hub *mailbox.Hub, agentID, role, systemPrompt string, cfg ContentConfig) {
	_ = os.MkdirAll(filepath.Join(cfg.WorkDir, "agents", agentID), 0o755)
	log.Printf("[%s] ready", agentID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		raw, ok := hub.Recv(agentID)
		if !ok || raw == "" {
			time.Sleep(120 * time.Millisecond)
			continue
		}

		parsed, err := mailbox.ParseMessage(raw)
		if err != nil {
			continue
		}
		if parsed.Type != mailbox.MessageTypeTaskAssign {
			continue
		}

		payload, _ := mailbox.ParseTaskAssignPayload(parsed)
		taskID := parsed.TaskID
		from := parsed.From

		log.Printf("[%s] ← task %s", agentID, taskID)
		_ = hub.StartTask(taskID)
		_ = hub.SetAgentStatus(agentID, "busy", taskID)

		userPrompt := fmt.Sprintf(
			"任务ID：%s\n\n%s\n\n请直接输出完整成果，不要以「好的」「以下是」等废话开头。",
			taskID, payload.Description)

		result, llmErr := llmGenerate(ctx, cfg, systemPrompt, userPrompt)
		if llmErr != nil {
			log.Printf("[%s] llm error: %v", agentID, llmErr)
			_ = hub.FailTask(taskID, llmErr.Error())
		} else {
			_ = hub.CompleteTask(taskID, agentID, result)
			notify, _ := mailbox.NewChatMessage(agentID, taskID, "✓ 任务完成")
			_ = hub.Send(from, notify)
			log.Printf("[%s] task %s done (%d chars)", agentID, taskID, len(result))
		}
		_ = hub.SetAgentStatus(agentID, "idle", "")
	}
}

// llmGenerate calls an OpenAI-compatible chat completions endpoint and returns the response text.
func llmGenerate(ctx context.Context, cfg ContentConfig, systemPrompt, userPrompt string) (string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type reqBody struct {
		Model       string  `json:"model"`
		Messages    []msg   `json:"messages"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
	}

	body, _ := json.Marshal(reqBody{
		Model: cfg.ModelID,
		Messages: []msg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.8,
		MaxTokens:   3000,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.APIURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api returned %d", resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("api error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return out.Choices[0].Message.Content, nil
}
