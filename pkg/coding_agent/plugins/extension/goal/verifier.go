package goal

// Maker-checker verification for update_goal(status=complete).
//
// When enabled, the completion claim is judged by an independent fresh-
// context agent (a ForkSession child) instead of the generator itself. The
// verifier gets read-only discovery tools plus bash so it can EXECUTE tests
// rather than read code, and must answer with a JSON verdict. A REJECT keeps
// the goal active and returns the reasons to the generator; after
// maxRejects consecutive rejects the goal is paused and handed to the human.
//
// Configured via extensions.yaml:
//
//	extensions:
//	  - name: goal
//	    config:
//	      verifier:
//	        enabled: true
//	        model: ""        # optional model ID override for the verifier
//	        max_rejects: 3   # consecutive rejects before the goal pauses
//	        max_turns: 12    # verifier agent turn cap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
)

const (
	defaultVerifierMaxRejects = 3
	defaultVerifierMaxTurns   = 12
)

// verifierTools is the child's tool whitelist: discovery plus execution,
// no mutation (write/edit are absent on purpose).
var verifierTools = []string{"read", "grep", "ls", "find", "bash"}

type verifierConfig struct {
	Enabled    bool
	Model      string
	MaxRejects int
	MaxTurns   int
}

func (c verifierConfig) maxRejects() int {
	if c.MaxRejects > 0 {
		return c.MaxRejects
	}
	return defaultVerifierMaxRejects
}

func (c verifierConfig) maxTurns() int {
	if c.MaxTurns > 0 {
		return c.MaxTurns
	}
	return defaultVerifierMaxTurns
}

// ApplyConfig implements extension.ConfigurableExtension for the `config:`
// block in extensions.yaml. Only the `verifier` key is recognized.
func (e *Extension) ApplyConfig(cfg map[string]any) error {
	raw, ok := cfg["verifier"]
	if !ok || raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("goal: verifier config must be a map, got %T", raw)
	}
	out := verifierConfig{}
	for key, value := range m {
		switch key {
		case "enabled":
			b, ok := value.(bool)
			if !ok {
				return fmt.Errorf("goal: verifier.enabled must be a bool, got %T", value)
			}
			out.Enabled = b
		case "model":
			s, ok := value.(string)
			if !ok {
				return fmt.Errorf("goal: verifier.model must be a string, got %T", value)
			}
			out.Model = strings.TrimSpace(s)
		case "max_rejects":
			n, err := toPositiveInt(value)
			if err != nil {
				return fmt.Errorf("goal: verifier.max_rejects: %v", err)
			}
			out.MaxRejects = n
		case "max_turns":
			n, err := toPositiveInt(value)
			if err != nil {
				return fmt.Errorf("goal: verifier.max_turns: %v", err)
			}
			out.MaxTurns = n
		default:
			return fmt.Errorf("goal: unknown verifier config key %q", key)
		}
	}
	e.mu.Lock()
	e.verifier = out
	e.mu.Unlock()
	return nil
}

func toPositiveInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		if n > 0 {
			return n, nil
		}
	case int64:
		if n > 0 {
			return int(n), nil
		}
	case float64:
		if n > 0 && n == float64(int(n)) {
			return int(n), nil
		}
	default:
		return 0, fmt.Errorf("must be a positive integer, got %T", v)
	}
	return 0, fmt.Errorf("must be a positive integer, got %v", v)
}

func (e *Extension) verifierEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.verifier.Enabled
}

func (e *Extension) verifierSnapshot() verifierConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.verifier
}

// verifierVerdict is the JSON the verifier child must emit as its final
// answer.
type verifierVerdict struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons"`
}

// parseVerifierVerdict scans text for JSON objects carrying a "verdict"
// field and returns the last valid one. Tolerates surrounding prose and
// code fences: it tries a decode at every '{' and keeps whatever parses.
func parseVerifierVerdict(text string) (verifierVerdict, bool) {
	var out verifierVerdict
	found := false
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var candidate verifierVerdict
		if err := dec.Decode(&candidate); err != nil {
			continue
		}
		verdict := strings.ToUpper(strings.TrimSpace(candidate.Verdict))
		if verdict != "PASS" && verdict != "REJECT" {
			continue
		}
		candidate.Verdict = verdict
		out = candidate
		found = true
		// Skip past this object so nested '{' inside it aren't re-tried.
		i += int(dec.InputOffset()) - 1
	}
	return out, found
}

func verifierSystemPrompt() string {
	return `ROLE: Adversarial goal-completion verifier (maker-checker).
An agent claims it has completed an objective. You were not part of that
work and must judge the claim from scratch.

ASSUME the objective is NOT met until proven otherwise. Do not praise.
Find what is missing or broken.

CHECK, in order:
1. Build a checklist mapping every explicit requirement, named file,
   command, test, gate, and deliverable in the objective to concrete
   evidence in the repository.
2. Execute, don't read: run the relevant tests, builds, linters, or
   commands and judge their real output. "The code looks right" is not
   evidence.
3. Look for edge cases and requirements the author skipped.
4. Reject proxy signals: passing tests or substantial effort only count
   if they cover every requirement of the objective.

You have read-only discovery tools plus bash. Do not modify, commit, or
delete anything.

VERDICT: your final message MUST end with a single JSON object:
{"verdict":"PASS","reasons":[]}
or
{"verdict":"REJECT","reasons":["<one concrete, actionable reason>", "..."]}
PASS only if every requirement holds. A REJECT without concrete reasons is
useless — the author must know exactly what to fix.`
}

func verifierTaskPrompt(g Goal) string {
	return fmt.Sprintf(`The agent claims the following objective is complete:

<objective>
%s
</objective>

Audit the claim against the current state of the working directory and
report your verdict as specified.`, g.Objective)
}

// verifyCompletion runs the maker-checker gate for update_goal(complete).
// It returns (message, true) when completion must be REJECTED — the message
// goes back to the generator as the failed tool result — and ("", false)
// when completion may proceed (verifier disabled, PASS, or verifier
// infrastructure unavailable, which fails open so a broken verifier cannot
// brick an otherwise finished goal).
func (e *Extension) verifyCompletion(ctx context.Context) (string, bool) {
	if !e.verifierEnabled() || e.api == nil {
		return "", false
	}
	g, ok, err := e.store.CurrentErr()
	if err != nil || !ok {
		// Let MarkComplete surface store problems as before.
		return "", false
	}
	cfg := e.verifierSnapshot()
	out, err := e.api.ForkSession(ctx, extension.ForkOptions{
		Name:         "goal-verifier",
		SystemPrompt: verifierSystemPrompt(),
		Task:         verifierTaskPrompt(g),
		AllowedTools: verifierTools,
		Context:      "fresh",
		Model:        cfg.Model,
		MaxTurns:     cfg.maxTurns(),
	})
	if err != nil {
		e.tell(fmt.Sprintf("goal: verifier unavailable (%v) — accepting completion unverified", err))
		return "", false
	}

	verdict, parsed := parseVerifierVerdict(out)
	if parsed && verdict.Verdict == "PASS" {
		e.tell("goal: verifier PASS — completion confirmed by independent check")
		return "", false
	}
	reasons := verdict.Reasons
	if !parsed {
		reasons = []string{"verifier did not return a parseable verdict; treat the completion claim as unproven and re-verify your work"}
	}
	if len(reasons) == 0 {
		reasons = []string{"verifier rejected the claim without detail; re-verify every requirement of the objective against real evidence"}
	}

	updated, err := e.store.IncrementVerifierRejects()
	rejects := 0
	if err == nil {
		rejects = updated.VerifierRejects
	}
	maxRejects := cfg.maxRejects()

	var b strings.Builder
	fmt.Fprintf(&b, "update_goal rejected by the independent goal verifier (reject %d/%d). The goal is NOT complete.\n", rejects, maxRejects)
	b.WriteString("Address every reason below with real evidence, then claim completion again:\n")
	for _, r := range reasons {
		b.WriteString("- ")
		b.WriteString(r)
		b.WriteString("\n")
	}

	if rejects >= maxRejects {
		if paused, perr := e.store.Pause(); perr == nil {
			e.stopAgentGoalAccounting(paused.ID)
			e.tell(fmt.Sprintf("goal: paused after %d consecutive verifier rejects — needs human review\n%s", rejects, FormatGoalForUser(&paused)))
			fmt.Fprintf(&b, "\nThe goal has been PAUSED after %d consecutive rejects and now needs human review. Stop working on it and summarize the open gaps for the user.", rejects)
		}
	} else {
		e.tell(fmt.Sprintf("goal: verifier REJECT (%d/%d)\n- %s", rejects, maxRejects, strings.Join(reasons, "\n- ")))
	}
	return strings.TrimRight(b.String(), "\n"), true
}
