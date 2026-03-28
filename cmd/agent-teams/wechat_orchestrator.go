package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/repos/scraper"
)

// ArticleRequest is the user-facing input for starting an article workflow.
type ArticleRequest struct {
	Topic        string `json:"topic"`         // user-specified topic; empty = auto-discover
	Niche        string `json:"niche"`         // content direction (e.g. "AI创业")
	AutoDiscover bool   `json:"auto_discover"` // scrape trending topics first
}

// ArticleResult contains the task ID, file path, and final content.
type ArticleResult struct {
	TaskID   string `json:"task_id"`
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// runArticleWorkflow creates a single task for the full article requirement and assigns
// it to the editor (coordinator). The editor's CodingSession drives the entire workflow
// by delegating to researcher, copywriter, and reviewer via mailbox_delegate.
//
// This function blocks until the task completes or times out (30 minutes).
func (s *AgentTeamsServer) runArticleWorkflow(ctx context.Context, req ArticleRequest, cfg ContentConfig) (*ArticleResult, error) {
	hub := s.hub

	topicCtx := buildTopicContext(req)
	log.Printf("[article] brief: %s", topicCtx[:min(80, len(topicCtx))])

	// One task for the full article requirement.
	taskDesc := buildArticleTaskDesc(req, topicCtx)
	taskID, err := hub.CreateTask("wc-editor", taskDesc)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	if err := hub.AssignTask(taskID, "wc-editor"); err != nil {
		return nil, fmt.Errorf("assign task: %w", err)
	}

	// Kick off the editor with a task_assign message.
	msg, err := mailbox.NewTaskAssignMessage("system", taskID, taskDesc)
	if err != nil {
		return nil, fmt.Errorf("build msg: %w", err)
	}
	if err := hub.Send("wc-editor", msg); err != nil {
		return nil, fmt.Errorf("send to editor: %w", err)
	}

	log.Printf("[article] task %s started → wc-editor", taskID)

	// Wait for the editor to complete the task (up to 30 minutes).
	const timeout = 30 * time.Minute
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		task, err := hub.GetTask(taskID)
		if err != nil {
			return nil, err
		}
		switch task.Status {
		case mailbox.TaskStatusCompleted:
			content := task.Result
			filePath := saveArticleFile(req, taskID, content, cfg.WorkDir)
			log.Printf("[article] ✓ task %s done → %s", taskID, filePath)
			return &ArticleResult{TaskID: taskID, FilePath: filePath, Content: content}, nil
		case mailbox.TaskStatusFailed:
			return nil, fmt.Errorf("task %s failed: %s", taskID, task.Error)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("timeout (%v) waiting for task %s", timeout, taskID)
}

// buildArticleTaskDesc constructs the full task description for the editor.
func buildArticleTaskDesc(req ArticleRequest, topicCtx string) string {
	var sb strings.Builder
	sb.WriteString("请为以下方向创作一篇高质量微信公众号文章，完整流程包括：\n")
	sb.WriteString("1. 委托研究员调研选题方向（wc-researcher）\n")
	sb.WriteString("2. 基于调研选出最优选题，制定创作简报\n")
	sb.WriteString("3. 委托主笔按简报写作（wc-copywriter）\n")
	sb.WriteString("4. 委托审稿编辑审核（wc-reviewer），如需修改则让主笔修改（最多2轮）\n")
	sb.WriteString("5. 写编者按，将完整文章（含编者按）写入文件，调用 mailbox_complete 提交\n\n")
	sb.WriteString("---\n\n")
	sb.WriteString("**创作方向：**\n")
	sb.WriteString(topicCtx)
	return sb.String()
}

// buildTopicContext returns a context string describing what to write about.
func buildTopicContext(req ArticleRequest) string {
	if req.Topic != "" {
		return req.Topic
	}
	niche := req.Niche
	if niche == "" {
		niche = "科技与生活"
	}
	if !req.AutoDiscover {
		return "创作方向：" + niche
	}
	trending := fetchTrendingTopics()
	return "创作方向：" + niche + "\n\n今日热点（供选题参考）：\n" + trending
}

// fetchTrendingTopics scrapes TopHub for the current Chinese trending topics.
func fetchTrendingTopics() string {
	items, err := scraper.ScrapeTopHub(10)
	if err != nil {
		log.Printf("[article] fetchTrending: %v", err)
		return "（热点获取失败，请手动指定主题）"
	}
	var sb strings.Builder
	for i, item := range items {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, item.Title)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// saveArticleFile writes the article content to workspace/articles/{ts}-{topic}.md.
func saveArticleFile(req ArticleRequest, taskID, content, workDir string) string {
	dir := filepath.Join(workDir, "articles")
	_ = os.MkdirAll(dir, 0o755)

	topic := req.Topic
	if topic == "" {
		topic = req.Niche
	}
	if topic == "" {
		topic = taskID
	}
	_ = taskID // used in fallback above
	ts := time.Now().Format("20060102-150405")
	safe := strings.NewReplacer(" ", "_", "/", "-", ":", "-").Replace(topic)
	path := filepath.Join(dir, ts+"-"+safe+".md")

	_ = os.WriteFile(path, []byte(content), 0o644)
	return path
}

