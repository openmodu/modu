package workflow

import (
	"fmt"
	"strings"
)

const deepResearchWorkflowScript = `
meta({
  name: "deep_research",
  description: "Bundled workflow for multi-angle research and cross-checking",
  phases: [
    { title: "Scope", detail: "Break the question into research angles" },
    { title: "Research", detail: "Investigate several angles in parallel" },
    { title: "Cross-check", detail: "Challenge and filter claims" },
    { title: "Synthesis", detail: "Produce the final report" },
  ],
});

const question = String((args && args.question) || "");
const researchTools = ["web_search", "web_fetch", "read", "grep", "find", "ls"];

// Rules every research agent must follow. These guard against the two failure
// modes that make research worthless: (1) assuming the question is about the
// local code repo, and (2) fabricating data that was never actually retrieved.
const rules =
  "\n\nRULES:\n" +
  "1. web_search then web_fetch are your PRIMARY tools — use them to gather real sources before answering. " +
  "Treat the question as about the real world (news, markets, products, events, people), NOT about this code repository, " +
  "unless it explicitly names files/symbols in the current codebase.\n" +
  "2. NEVER invent specific facts (numbers, prices, dates, %, names, quotes). State only what a tool actually returned. " +
  "If you could not retrieve something, write 'NOT RETRIEVED' instead of guessing.\n" +
  "3. Cite the source URL or title for every concrete claim. A claim with no retrieved source must be labeled 'unverified'.";

phase("Scope");
const scope = await agent("First state the DOMAIN of this question in one line (e.g. finance/markets, tech news, science, this-codebase). Then break it into 3 focused, source-finding research angles. Use web_search to sanity-check what's findable. Question: " + question + rules, {
  label: "scope",
  tools: researchTools,
  permissionMode: "read-only",
});

phase("Research");
const findings = await parallel([
  () => agent("Use web_search + web_fetch to find primary or authoritative sources. Capture specific claims WITH their source URLs, plus your uncertainty. Question: " + question + "\n\nScope:\n" + scope + rules, {
    label: "primary sources",
    phase: "Research",
    tools: researchTools,
    permissionMode: "read-only",
  }),
  () => agent("Use web_search + web_fetch to find high-quality secondary analysis; summarize where it agrees or conflicts with primary evidence, with source URLs. Question: " + question + "\n\nScope:\n" + scope + rules, {
    label: "secondary analysis",
    phase: "Research",
    tools: researchTools,
    permissionMode: "read-only",
  }),
  () => agent("Use web_search + web_fetch to hunt for contradictions, stale claims, missing context, and reasons a claim might be wrong, with source URLs. Question: " + question + "\n\nScope:\n" + scope + rules, {
    label: "contradictions",
    phase: "Research",
    tools: researchTools,
    permissionMode: "read-only",
  }),
]);

phase("Cross-check");
const checked = await agent("Cross-check these findings. Keep only claims that cite a real retrieved source. REJECT any specific datum (number/date/price/quote) that the finding presents without a source URL — treat it as a likely fabrication, not evidence. Mark everything else as uncertain.\n\nQuestion:\n" + question + "\n\nFindings:\n" + JSON.stringify(findings) + rules, {
  label: "cross-check",
  tools: researchTools,
  permissionMode: "read-only",
});

phase("Synthesis");
const report = await agent("Write a concise research report answering the question. Lead with the answer. Include source URLs for every concrete claim. Clearly separate confirmed (sourced) findings from uncertain/unsourced ones. If the question could not be answered from retrieved sources, say so plainly instead of speculating.\n\nQuestion:\n" + question + "\n\nCross-check:\n" + checked + rules, {
  label: "report",
  tools: researchTools,
  permissionMode: "read-only",
});

return {
  question: question,
  scope: scope,
  findings: findings,
  cross_check: checked,
  report: report,
};
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
