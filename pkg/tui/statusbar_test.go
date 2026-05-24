package tui

import "testing"

// goalWatchIndicator is a pure consumer of the goal extension's
// RuntimeState map. The tests below assert the filter behaviour without
// spinning up a CodingSession — the contract is "show the indicator only
// when watching is on" so the host stays silent by default.

func TestGoalWatchIndicatorHiddenWhenWatchingOff(t *testing.T) {
	cases := []struct {
		name  string
		state map[string]any
	}{
		{name: "nil map", state: nil},
		{name: "no goal key", state: map[string]any{"other": "value"}},
		{name: "goal key not a map", state: map[string]any{"goal": "scalar"}},
		{name: "watching missing (default off)", state: map[string]any{
			"goal": map[string]any{"indicator": "goal active"},
		}},
		{name: "watching false", state: map[string]any{
			"goal": map[string]any{"watching": false, "indicator": "goal active"},
		}},
		{name: "watching not a bool", state: map[string]any{
			"goal": map[string]any{"watching": "on", "indicator": "goal active"},
		}},
		{name: "watching on but indicator empty", state: map[string]any{
			"goal": map[string]any{"watching": true, "indicator": "   "},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := goalWatchIndicator(c.state); got != "" {
				t.Errorf("expected silent (\"\"), got %q", got)
			}
		})
	}
}

func TestGoalWatchIndicatorShowsWhenWatchingOn(t *testing.T) {
	state := map[string]any{
		"goal": map[string]any{
			"watching":  true,
			"indicator": "goal 1.2K/5K",
		},
	}
	if got, want := goalWatchIndicator(state), "goal 1.2K/5K"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGoalWatchIndicatorTrimsIndicatorWhitespace(t *testing.T) {
	state := map[string]any{
		"goal": map[string]any{
			"watching":  true,
			"indicator": "  goal paused\n",
		},
	}
	if got, want := goalWatchIndicator(state), "goal paused"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
