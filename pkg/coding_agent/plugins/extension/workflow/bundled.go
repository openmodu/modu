package workflow

import (
	"fmt"
	"strings"
)

const deepResearchWorkflowScript = `
meta({
  name = "deep_research",
  description = "Bundled workflow for multi-angle research and cross-checking",
  phases = {
    { title = "Scope", detail = "Break the question into research angles" },
    { title = "Research", detail = "Investigate several angles in parallel" },
    { title = "Cross-check", detail = "Challenge and filter claims" },
    { title = "Synthesis", detail = "Produce the final report" },
  },
})

local question = tostring(args.question or "")
local research_tools = {"web_search", "web_fetch", "read", "grep", "find", "ls"}

phase("Scope")
local scope = agent("Break this research question into 3 focused angles. Prefer concrete source-finding tasks when web tools are available. Question: " .. question, {
  label = "scope",
  tools = research_tools,
  permission_mode = "read-only",
})

phase("Research")
local findings = parallel({
  {
    label = "primary sources",
    prompt = "Research primary or authoritative sources for this question. Capture specific claims, source names, and uncertainty. Question: " .. question .. "\n\nScope:\n" .. tostring(scope),
    tools = research_tools,
    permission_mode = "read-only",
  },
  {
    label = "secondary analysis",
    prompt = "Research high-quality secondary analysis and summarize where it agrees or conflicts with primary evidence. Question: " .. question .. "\n\nScope:\n" .. tostring(scope),
    tools = research_tools,
    permission_mode = "read-only",
  },
  {
    label = "contradictions",
    prompt = "Look specifically for contradictions, stale claims, missing context, and reasons a claim might be wrong. Question: " .. question .. "\n\nScope:\n" .. tostring(scope),
    tools = research_tools,
    permission_mode = "read-only",
  },
}, { concurrency = 3 })

phase("Cross-check")
local checked = agent("Cross-check these findings. Keep only claims supported by the strongest available evidence. Mark claims without usable sources as uncertain.\n\nQuestion:\n" .. question .. "\n\nFindings:\n" .. json.encode(findings), {
  label = "cross-check",
  tools = research_tools,
  permission_mode = "read-only",
})

phase("Synthesis")
local report = agent("Write a concise research report answering the question. Include citations or source names where available. Clearly separate confirmed findings from uncertain or unsupported claims.\n\nQuestion:\n" .. question .. "\n\nCross-check:\n" .. tostring(checked), {
  label = "report",
  tools = research_tools,
  permission_mode = "read-only",
})

return {
  question = question,
  scope = scope,
  findings = findings,
  cross_check = checked,
  report = report,
}
`

func (e *Extension) cmdDeepResearch(argsText string) error {
	question := strings.TrimSpace(argsText)
	if question == "" {
		e.tell("Usage: /deep-research <question>")
		return nil
	}
	scriptPath, runDir, err := persistWorkflowScript(e.api.SessionDir(), deepResearchWorkflowScript)
	if err != nil {
		return err
	}
	exec := workflowExecution{
		Script:      deepResearchWorkflowScript,
		Args:        map[string]any{"question": question},
		Concurrency: e.cfg.Concurrency,
		MaxAgents:   e.cfg.MaxAgents,
		ScriptPath:  scriptPath,
		RunDir:      runDir,
	}
	if !e.approveWorkflowRun(exec, "/deep-research") {
		e.tell("Deep research workflow cancelled before start.")
		return nil
	}
	runID := e.startBackgroundWorkflow(exec)
	text := fmt.Sprintf("Deep research workflow started in background.\nRun: %s", runID)
	if scriptPath != "" {
		text += "\nScript: " + scriptPath
	}
	text += "\nUse /workflows to watch progress."
	e.tell(text)
	return nil
}
