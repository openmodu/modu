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

	// ── Single task: one requirement, multi-agent collaboration in one thread ──
	log.Println("[demo] ── Phase 2: single requirement task ──")
	taskID, _ := hub.CreateTask("planner",
		"围绕「AI 协作中的创意边界」完成一次完整内容交付：选题讨论、文章写作、视觉概念、评审修改，所有进展统一在同一任务论坛内同步。",
		projID)
	sleep(600 * time.Millisecond)

	_ = hub.AssignTask(taskID, "planner")
	_ = hub.StartTask(taskID)
	_ = hub.SetAgentStatus("planner", "busy", taskID)

	hub.PostForumMessageKind("planner", taskID, mailbox.ConversationKindDecision, "本需求采用单任务协作模式推进，我会在这个线程里统一协调，不再拆分多个任务。")
	_ = hub.UpdateTaskSummary(taskID, "目标：围绕 AI 协作中的创意边界完成一篇文章与一个视觉概念，所有沟通都收敛在当前任务。")
	sleep(800 * time.Millisecond)

	_ = hub.AssignTask(taskID, "writer")
	_ = hub.AssignTask(taskID, "designer")
	_ = hub.AssignTask(taskID, "reviewer")
	sleep(500 * time.Millisecond)

	msg, _ := mailbox.NewTaskAssignMessage("planner", taskID,
		"请在同一个任务线程中协作完成内容交付：writer 负责正文，designer 负责视觉概念，reviewer 负责评审。")
	_ = hub.Send("writer", msg)
	_ = hub.Send("designer", msg)
	_ = hub.Send("reviewer", msg)
	sleep(1500 * time.Millisecond)

	_ = hub.SetAgentStatus("writer", "busy", taskID)
	chat("writer", "planner", taskID, "收到。我先基于创意工作者场景起草文章结构，晚些把正文草稿发到论坛。")
	sleep(1200 * time.Millisecond)

	_ = hub.SetAgentStatus("designer", "busy", taskID)
	chat("designer", "writer", taskID, "建议从“人机边界逐渐融化”的意象切入，视觉上用流动线条和渐变光束承接。")
	sleep(1200 * time.Millisecond)

	hub.PostForumMessageKind("planner", taskID, mailbox.ConversationKindDecision, "确认方向：聚焦设计师、写作者与 AI 协作时的判断权变化，语气保持克制，不写成空泛口号。")
	_ = hub.UpdateTaskSummary(taskID, "当前共识：文章聚焦创意工作者与 AI 协作时的判断权变化，视觉概念强调边界融化。")
	sleep(1500 * time.Millisecond)

	hub.PostForumMessageKind("writer", taskID, mailbox.ConversationKindDeliverable,
		"文章草稿：\n《当创意遇见算法》\n\n站在设计工作室的角落，看着两位设计师与 AI 模型反复对话，不像命令，更像协商。AI 时代的创意边界不是一道墙，而是一片渐变光谱。机器擅长生成变化，人仍然负责定义意义。真正有价值的作品，常常诞生在人与模型意见不一致的时刻：系统给出意外答案，人被迫重新审视自己的判断。边界没有消失，只是从泾渭分明变成持续谈判。")
	sleep(1800 * time.Millisecond)

	hub.PostForumMessageKind("designer", taskID, mailbox.ConversationKindIdea,
		"视觉概念：人机交融的创意流动图。主色调深蓝配暖金，用断续线条表达判断权交接，用局部高亮表现“分歧时刻”的灵感爆发。")
	_ = hub.SetAgentStatus("designer", "idle", "")
	sleep(1500 * time.Millisecond)

	chat("reviewer", "planner", taskID, "结构和观点是成立的，但结尾偏收，建议加一句行动呼吁，强化读者代入。")
	sleep(1200 * time.Millisecond)

	hub.PostForumMessageKind("writer", taskID, mailbox.ConversationKindDeliverable,
		"已按意见补结尾：与其焦虑边界被谁夺走，不如主动进入这场协作，重新定义什么才算真正属于人的创造。")
	_ = hub.SetAgentStatus("writer", "idle", "")
	_ = hub.SetAgentStatus("reviewer", "idle", "")
	_ = hub.UpdateTaskSummary(taskID, "待收尾：正文、视觉概念和评审意见已经对齐，可由 planner 汇总为最终交付。")
	sleep(1000 * time.Millisecond)

	finalResult := "《当创意遇见算法》\n\n站在设计工作室的角落，看着两位设计师与 AI 模型反复对话，不像命令，更像协商。AI 时代的创意边界不是一道墙，而是一片渐变光谱。机器擅长生成变化，人仍然负责定义意义。真正有价值的作品，常常诞生在人与模型意见不一致的时刻：系统给出意外答案，人被迫重新审视自己的判断。边界没有消失，只是从泾渭分明变成持续谈判。\n\n视觉概念：人机交融的创意流动图，主色调深蓝配暖金，用断续线条表达判断权交接。\n\n结语：与其焦虑边界被谁夺走，不如主动进入这场协作，重新定义什么才算真正属于人的创造。"
	_ = hub.UpdateTaskSummary(taskID, "最终结论：文章与视觉概念已经收敛，任务进入完成态，论坛讨论随之关闭。")
	_ = hub.CompleteTask(taskID, "planner", finalResult)
	_ = hub.SetAgentStatus("planner", "idle", "")
	log.Printf("[demo] task %s completed", taskID)
	sleep(800 * time.Millisecond)

	// ── Complete project ──────────────────────────────────────────────────────
	if err := hub.CompleteProject(projID); err != nil {
		log.Printf("[demo] CompleteProject: %v", err)
	} else {
		log.Printf("[demo] project %s completed ✓", projID)
	}
	fmt.Println("[demo] ── simulation finished ──")
}
