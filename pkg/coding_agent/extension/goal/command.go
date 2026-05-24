package goal

import "strings"

type parsedGoalCommandKind string

const (
	parsedGoalShow         parsedGoalCommandKind = "show"
	parsedGoalClear        parsedGoalCommandKind = "clear"
	parsedGoalSetStatus    parsedGoalCommandKind = "setStatus"
	parsedGoalSetObjective parsedGoalCommandKind = "setObjective"
)

type parsedGoalCommand struct {
	Kind      parsedGoalCommandKind
	Status    Status
	Objective string
}

func parseGoalCommand(rawArgs string) parsedGoalCommand {
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" {
		return parsedGoalCommand{Kind: parsedGoalShow}
	}

	switch strings.ToLower(trimmed) {
	case "pause":
		return parsedGoalCommand{Kind: parsedGoalSetStatus, Status: StatusPaused}
	case "resume":
		return parsedGoalCommand{Kind: parsedGoalSetStatus, Status: StatusActive}
	case "clear":
		return parsedGoalCommand{Kind: parsedGoalClear}
	default:
		return parsedGoalCommand{Kind: parsedGoalSetObjective, Objective: trimmed}
	}
}
