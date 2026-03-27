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

// ArticleResult contains all intermediate outputs and the final file path.
type ArticleResult struct {
	ProjectID string `json:"project_id"`
	Research  string `json:"research"`
	Brief     string `json:"brief"`
	Article   string `json:"article"`
	Review    string `json:"review"`
	Note      string `json:"note"`
	FilePath  string `json:"file_path"`
}

// runArticleWorkflow drives the full 6-phase 公众号 content pipeline:
//
//  1. Build topic context (optional trending topic discovery)
//  2. Create project
//  3. Research → wc-researcher
//  4. Topic selection + creative brief → wc-editor
//  5. Article writing → wc-copywriter
//  6. Review (up to 2 rounds) → wc-reviewer → wc-copywriter (if REVISE)
//  7. Editorial note → wc-editor
//  8. Save final file + complete project
func (s *AgentTeamsServer) runArticleWorkflow(ctx context.Context, req ArticleRequest, cfg ContentConfig) (*ArticleResult, error) {
	hub := s.hub
	ar := &ArticleResult{}

	topicCtx := buildTopicContext(req)
	log.Printf("[article] topic context: %s", topicCtx[:min(80, len(topicCtx))])

	// ── Phase 1: project ──────────────────────────────────────────────────────
	projName := req.Topic
	if projName == "" {
		projName = req.Niche + " 内容项目"
	}
	projID, err := hub.CreateProject("wc-editor", projName)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	ar.ProjectID = projID
	log.Printf("[article] project %s", projID)

	// ── Phase 2: research ────────────────────────────────────────────────────
	log.Println("[article] ── research ──")
	ar.Research, err = dispatchTask(ctx, hub, "wc-editor", "wc-researcher",
		"请针对以下创作方向进行选题调研，输出3个有爆款潜力的选题方向：\n\n"+topicCtx,
		projID, 6*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("research: %w", err)
	}

	// ── Phase 3: topic selection + creative brief (editor) ───────────────────
	log.Println("[article] ── topic selection ──")
	ar.Brief, err = dispatchTask(ctx, hub, "wc-editor", "wc-editor",
		"研究员提交了以下选题调研报告：\n\n"+ar.Research+
			"\n\n请完成两件事：\n1. 选出最优选题并说明理由\n2. 为主笔写出详细的创作简报",
		projID, 4*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("topic selection: %w", err)
	}

	// ── Phase 4: writing ─────────────────────────────────────────────────────
	log.Println("[article] ── writing ──")
	ar.Article, err = dispatchTask(ctx, hub, "wc-editor", "wc-copywriter",
		"请按照以下创作简报写出完整的公众号文章：\n\n"+ar.Brief,
		projID, 8*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("writing: %w", err)
	}

	// ── Phase 5: review (max 2 rounds) ───────────────────────────────────────
	log.Println("[article] ── review ──")
	current := ar.Article
	for round := 1; round <= 2; round++ {
		log.Printf("[article] review round %d", round)
		ar.Review, err = dispatchTask(ctx, hub, "wc-editor", "wc-reviewer",
			fmt.Sprintf("请评审以下公众号文章（第%d轮）：\n\n%s", round, current),
			projID, 5*time.Minute)
		if err != nil {
			return nil, fmt.Errorf("review r%d: %w", round, err)
		}

		if strings.Contains(ar.Review, "[ACCEPT]") {
			log.Printf("[article] ✓ accepted (round %d)", round)
			ar.Article = current
			break
		}
		if round == 2 {
			// Force accept after 2 rounds to avoid infinite loop
			log.Printf("[article] max rounds reached, accepting as-is")
			ar.Article = current
			break
		}

		log.Printf("[article] ✗ revision requested")
		current, err = dispatchTask(ctx, hub, "wc-editor", "wc-copywriter",
			"审稿编辑提出了修改意见，请修改文章：\n\n**审稿意见**：\n"+ar.Review+"\n\n**原文**：\n"+current,
			projID, 8*time.Minute)
		if err != nil {
			return nil, fmt.Errorf("revision: %w", err)
		}
	}

	// ── Phase 6: editorial note ──────────────────────────────────────────────
	log.Println("[article] ── editorial note ──")
	ar.Note, _ = dispatchTask(ctx, hub, "wc-editor", "wc-editor",
		"文章已通过审核，请写一段编者按（50-80字）：\n\n"+ar.Article,
		projID, 3*time.Minute)

	// ── Save + complete ───────────────────────────────────────────────────────
	ar.FilePath = saveArticleFile(req, ar, cfg.WorkDir)
	_ = hub.CompleteProject(projID)
	log.Printf("[article] ✓ saved → %s", ar.FilePath)
	return ar, nil
}

// dispatchTask creates a task, assigns it to the target agent, sends the task_assign
// message, and blocks until the task completes (or times out).
func dispatchTask(ctx context.Context, hub *mailbox.Hub, from, to, brief, projID string, timeout time.Duration) (string, error) {
	taskID, err := hub.CreateTask(from, brief, projID)
	if err != nil {
		return "", err
	}
	if err := hub.AssignTask(taskID, to); err != nil {
		return "", err
	}
	msg, err := mailbox.NewTaskAssignMessage(from, taskID, brief)
	if err != nil {
		return "", err
	}
	if err := hub.Send(to, msg); err != nil {
		return "", err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		task, err := hub.GetTask(taskID)
		if err != nil {
			return "", err
		}
		switch task.Status {
		case mailbox.TaskStatusCompleted:
			if r, ok := task.AgentResults[to]; ok && r != "" {
				return r, nil
			}
			return task.Result, nil
		case mailbox.TaskStatusFailed:
			return "", fmt.Errorf("task %s failed: %s", taskID, task.Error)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", fmt.Errorf("timeout (%v) waiting for %s", timeout, taskID)
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
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, item.Title))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// saveArticleFile writes the complete article + all phase outputs to disk as Markdown.
func saveArticleFile(req ArticleRequest, ar *ArticleResult, workDir string) string {
	dir := filepath.Join(workDir, "articles")
	_ = os.MkdirAll(dir, 0o755)

	topic := req.Topic
	if topic == "" {
		topic = req.Niche
	}
	ts := time.Now().Format("20060102-150405")
	safe := strings.NewReplacer(" ", "_", "/", "-", ":", "-").Replace(topic)
	path := filepath.Join(dir, ts+"-"+safe+".md")

	content := fmt.Sprintf(`# %s

**生成时间**：%s
**项目**：%s

---

## 调研报告

%s

---

## 创作简报

%s

---

## 文章正文

%s

---

## 审稿意见

%s

---

## 编者按

%s
`,
		topic,
		time.Now().Format("2006-01-02 15:04:05"),
		ar.ProjectID,
		ar.Research,
		ar.Brief,
		ar.Article,
		ar.Review,
		ar.Note,
	)
	_ = os.WriteFile(path, []byte(content), 0o644)
	return path
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
