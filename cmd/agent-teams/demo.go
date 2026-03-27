package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
)

func (s *AgentTeamsServer) handleDemoRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	go s.runDemoSimulation()
	writeJSON(w, map[string]string{"ok": "true", "message": "Demo simulation started"})
}

// runDemoSimulation drives a fully-scripted creative-team workflow so the UI
// renders the whole agent collaboration story without needing real LLMs.
func (s *AgentTeamsServer) runDemoSimulation() {
	hub := s.hub
	sleep := func(d time.Duration) { time.Sleep(d) }
	chat := func(from, to, taskID, text string) {
		msg, err := mailbox.NewChatMessage(from, taskID, text)
		if err != nil {
			log.Printf("[demo] chat: %v", err)
			return
		}
		_ = hub.Send(to, msg)
	}

	log.Println("[demo] ── Phase 0: register agents ──")
	for _, id := range []string{"planner", "writer", "designer", "reviewer"} {
		hub.Register(id)
	}

	log.Println("[demo] ── Phase 1: create project ──")
	projID, err := hub.CreateProject("planner", "AI 时代的内容创作")
	if err != nil {
		log.Printf("[demo] CreateProject: %v", err)
		return
	}
	log.Printf("[demo] project %s created", projID)
	sleep(800 * time.Millisecond)

	// ── Task 1: Topic research ────────────────────────────────────────────────
	log.Println("[demo] ── Phase 2: topic research task ──")
	taskID1, _ := hub.CreateTask("planner",
		"调研 AI 时代人类创造力的核心议题，提炼 3 个有深度的选题方向（每个含标题+核心角度）", projID)
	sleep(600 * time.Millisecond)

	_ = hub.AssignTask(taskID1, "writer")
	msg1, _ := mailbox.NewTaskAssignMessage("planner", taskID1,
		"请为本次内容项目进行选题调研，输出 3 个有深度的选题方向，每个附带核心切入角度。")
	_ = hub.Send("writer", msg1)
	sleep(400 * time.Millisecond)

	_ = hub.StartTask(taskID1)
	_ = hub.SetAgentStatus("writer", "busy", taskID1)
	sleep(2 * time.Second)

	chat("writer", "planner", taskID1, "收到任务，正在调研。请问是否需要重点关注某个细分领域？")
	sleep(1500 * time.Millisecond)
	chat("planner", "writer", taskID1, "重点关注创意工作者（设计师、作家）对 AI 工具的实际使用体验。")
	sleep(3 * time.Second)

	_ = hub.CompleteTask(taskID1, "writer",
		"选题方向：\n1. AI 协作中的创意边界——设计师与 LLM 的对话实验\n2. 写作者的 AI 焦虑与新生产力范式\n3. 当算法成为缪斯：AI 时代的灵感经济学")
	_ = hub.SetAgentStatus("writer", "idle", "")
	log.Printf("[demo] task %s completed", taskID1)
	sleep(1 * time.Second)

	// ── Task 2: Article writing (multi-agent) ─────────────────────────────────
	log.Println("[demo] ── Phase 3: article writing task ──")
	taskID2, _ := hub.CreateTask("planner",
		"基于「AI 协作中的创意边界」，撰写一篇深度文章（约 500 字）", projID)
	sleep(500 * time.Millisecond)

	_ = hub.AssignTask(taskID2, "writer")
	_ = hub.AssignTask(taskID2, "designer")
	_ = hub.StartTask(taskID2)
	_ = hub.SetAgentStatus("writer", "busy", taskID2)
	_ = hub.SetAgentStatus("designer", "busy", taskID2)

	msg2, _ := mailbox.NewTaskAssignMessage("planner", taskID2,
		"请撰写文章正文，约 500 字，并提供配套视觉概念。")
	_ = hub.Send("writer", msg2)
	_ = hub.Send("designer", msg2)
	sleep(2500 * time.Millisecond)

	chat("designer", "writer", taskID2, "建议从「人机边界的模糊」视角切入，配合流动线条的视觉隐喻会更有力量。")
	sleep(2 * time.Second)
	chat("writer", "designer", taskID2, "好主意！我会在文章结构上呼应这个意象。")
	sleep(3 * time.Second)

	_ = hub.CompleteTask(taskID2, "designer",
		"视觉概念：人机交融的创意流动图——以渐变光束与断续线条表达边界消融，主色调深蓝+紫金。")
	_ = hub.SetAgentStatus("designer", "idle", "")
	sleep(2 * time.Second)

	_ = hub.CompleteTask(taskID2, "writer",
		"《当创意遇见算法》\n\n"+
			"站在设计工作室的角落，看着两位设计师与 AI 模型反复对话——不是命令，而是协商。\n\n"+
			"这一幕让我意识到，AI 时代的创意边界不是一道墙，而是一片渐变的光谱。\n\n"+
			"过去，我们把创意视为人类独有的领地；而今，算法正在学会「灵感」这门语言。但真正发生的不是替代，\n"+
			"而是分工的重新定义：机器处理形式的排列组合，人类守护意义的诞生与判断。\n\n"+
			"设计师 Lin 告诉我，她最好的作品往往诞生于与 AI 的「分歧时刻」——当系统给出意料之外的方案，\n"+
			"人的直觉被迫重新审视自己的偏见。这种张力，恰恰是当代创意的核心动力。\n\n"+
			"边界从未消失，只是变得更有弹性、更值得探索。")
	_ = hub.SetAgentStatus("writer", "idle", "")
	log.Printf("[demo] task %s completed", taskID2)
	sleep(1 * time.Second)

	// ── Task 3: Editorial review ──────────────────────────────────────────────
	log.Println("[demo] ── Phase 4: review task ──")
	taskID3, _ := hub.CreateTask("planner", "评审文章与视觉概念，给出具体修改建议或接受", projID)
	sleep(500 * time.Millisecond)

	_ = hub.AssignTask(taskID3, "reviewer")
	msg3, _ := mailbox.NewTaskAssignMessage("planner", taskID3,
		"请评审本次交付成果（文章+视觉），给出明确的接受/修改意见。")
	_ = hub.Send("reviewer", msg3)
	sleep(400 * time.Millisecond)

	_ = hub.StartTask(taskID3)
	_ = hub.SetAgentStatus("reviewer", "busy", taskID3)
	sleep(3 * time.Second)

	chat("reviewer", "planner", taskID3, "文章视角新颖，视觉概念契合度高。建议结尾增加一句行动呼吁，整体可以接受。")
	sleep(1500 * time.Millisecond)

	_ = hub.CompleteTask(taskID3, "reviewer",
		"[accept] 文章结构清晰，「分歧时刻」的例证有力，语言质量良好。\n建议：结尾可增加一句呼吁读者主动探索人机协作边界的号召语，以加强情感收尾。")
	_ = hub.SetAgentStatus("reviewer", "idle", "")
	log.Printf("[demo] task %s completed", taskID3)
	sleep(800 * time.Millisecond)

	// ── Complete project ──────────────────────────────────────────────────────
	if err := hub.CompleteProject(projID); err != nil {
		log.Printf("[demo] CompleteProject: %v", err)
	} else {
		log.Printf("[demo] project %s completed ✓", projID)
	}
	fmt.Println("[demo] ── simulation finished ──")
}
