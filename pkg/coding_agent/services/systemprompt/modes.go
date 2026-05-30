package systemprompt

// PlanModeBlock is appended while the session is in plan mode.
const PlanModeBlock = "## Active Mode: Plan\n" +
	"You are in plan mode. write, edit, and bash are blocked — you cannot " +
	"change anything yet. Your job is to produce a plan good enough that " +
	"the user approves it without follow-up questions.\n\n" +
	"Investigate first, do not guess:\n" +
	"- Read the actual files end to end, not just snippets. Trace every " +
	"call site, type, and config the change touches.\n" +
	"- Find existing patterns/tests to follow so the plan fits the codebase.\n" +
	"- Identify edge cases, failure modes, and what could break.\n" +
	"- If the request is ambiguous, ask the user before planning — do not " +
	"pick an interpretation silently.\n\n" +
	"Then call `exit_plan_mode` with:\n" +
	"- `plan`: concise markdown covering Goal, Approach, Files to change " +
	"(with paths), Validation/tests, and Risks. Reference real file paths " +
	"and symbols you verified — no vague hand-waving.\n" +
	"- `steps`: an ordered array of small, individually verifiable " +
	"sub-tasks. Each step should be one focused change.\n\n" +
	"After you submit, the user decides:\n" +
	"- APPROVED: plan mode exits, steps become your todo list, implement " +
	"them in order, one in_progress at a time.\n" +
	"- REJECTED (the exit_plan_mode call is denied): you are still in plan " +
	"mode. Make no changes. Use their feedback, revise the plan, and call " +
	"`exit_plan_mode` again."

// WorktreeBlock is appended while the session operates inside an isolated git
// worktree.
func WorktreeBlock(path string) string {
	return "## Active Worktree\nThe session is currently operating inside an isolated git worktree at: " + path
}
