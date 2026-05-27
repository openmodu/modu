package subagent

import (
	"context"
	"fmt"
	"strings"
)

func (e *Extension) cmdRun(args string) error {
	agentName, task, err := parseAgentTask(args)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	text, err := forkOne(context.Background(), e, agentName, task, callOptions{})
	if err != nil {
		e.tell("subagent: " + err.Error())
		return nil
	}
	e.tell(text)
	return nil
}

func (e *Extension) cmdParallel(args string) error {
	calls, err := parseCommandCalls(args)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	items := make([]any, 0, len(calls))
	for _, c := range calls {
		items = append(items, map[string]any{"agent": c.agent, "task": c.task})
	}
	text, err := runParallel(context.Background(), e, map[string]any{"parallel": items})
	if err != nil {
		e.tell("subagent: " + err.Error())
		return nil
	}
	e.tell(text)
	return nil
}

func (e *Extension) cmdChain(args string) error {
	calls, err := parseCommandCalls(args)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	items := make([]any, 0, len(calls))
	for _, c := range calls {
		items = append(items, map[string]any{"agent": c.agent, "task": c.task})
	}
	text, err := runChain(context.Background(), e, map[string]any{"chain": items})
	if err != nil {
		e.tell("subagent: " + err.Error())
		return nil
	}
	e.tell(text)
	return nil
}

func (e *Extension) cmdDoctor(args string) error {
	text, err := runAction(context.Background(), e, "doctor", nil)
	if err != nil {
		e.tell("subagent: " + err.Error())
		return nil
	}
	e.tell(text)
	return nil
}

func (e *Extension) tell(text string) {
	if e != nil && e.api != nil {
		e.api.Notify(e.Name(), text)
	}
}

func parseCommandCalls(raw string) ([]callSpec, error) {
	parts := strings.Split(raw, "->")
	calls := make([]callSpec, 0, len(parts))
	for i, part := range parts {
		agentName, task, err := parseAgentTask(part)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		calls = append(calls, callSpec{agent: agentName, task: task})
	}
	if len(calls) == 0 {
		return nil, fmt.Errorf("usage: <agent> <task> [-> <agent> <task> ...]")
	}
	return calls, nil
}

func parseAgentTask(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("usage: <agent> <task>")
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "", "", fmt.Errorf("usage: <agent> <task>")
	}
	agentName := fields[0]
	task := strings.TrimSpace(strings.TrimPrefix(raw, agentName))
	task = trimOptionalQuotes(task)
	if task == "" {
		return "", "", fmt.Errorf("usage: <agent> <task>")
	}
	return agentName, task, nil
}

func trimOptionalQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
}
