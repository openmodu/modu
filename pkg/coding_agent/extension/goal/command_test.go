package goal

import "testing"

func TestParseGoalCommand(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want parsedGoalCommand
	}{
		{name: "empty", raw: "", want: parsedGoalCommand{Kind: parsedGoalShow}},
		{name: "objective", raw: "ship the Codex style flow --token-budget 88", want: parsedGoalCommand{Kind: parsedGoalSetObjective, Objective: "ship the Codex style flow --token-budget 88"}},
		{name: "set is objective", raw: "set up the release", want: parsedGoalCommand{Kind: parsedGoalSetObjective, Objective: "set up the release"}},
		{name: "pause", raw: "pause", want: parsedGoalCommand{Kind: parsedGoalSetStatus, Status: StatusPaused}},
		{name: "resume", raw: "resume", want: parsedGoalCommand{Kind: parsedGoalSetStatus, Status: StatusActive}},
		{name: "clear", raw: "clear", want: parsedGoalCommand{Kind: parsedGoalClear}},
		{name: "status is objective", raw: "status", want: parsedGoalCommand{Kind: parsedGoalSetObjective, Objective: "status"}},
		{name: "complete is objective", raw: "complete", want: parsedGoalCommand{Kind: parsedGoalSetObjective, Objective: "complete"}},
		{name: "help is objective", raw: "help", want: parsedGoalCommand{Kind: parsedGoalSetObjective, Objective: "help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseGoalCommand(tt.raw); got != tt.want {
				t.Fatalf("parseGoalCommand(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}
