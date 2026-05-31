package approval

import (
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
	"github.com/openmodu/modu/pkg/types"
)

// exit_plan_mode through unconditionally — the real interactive approval is
// driven inside the tool (so rejection feedback can reach the model).
func TestPlanGateAutoAllowed(t *testing.T) {
	m := New()
	m.DenyAlways("exit_plan_mode")
	m.SetRules(config.PermissionConfig{DenyTools: []string{"exit_plan_mode"}})
	called := false
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		called = true
		return types.ToolApprovalDeny, nil
	})
	if d, _ := m.Approve("exit_plan_mode", "c0", nil); d != types.ToolApprovalAllow {
		t.Fatalf("agent gate must auto-allow exit_plan_mode, got %v", d)
	}
	if called {
		t.Fatal("exit_plan_mode must not hit the generic approval callback")
	}
}

func TestApprovalManagerAutoAllowsReadOnlyTools(t *testing.T) {
	m := New()
	called := false
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		called = true
		return types.ToolApprovalDeny, nil
	})
	for _, toolName := range []string{"read", "grep", "find", "ls"} {
		if d, _ := m.Approve(toolName, "call-"+toolName, nil); d != types.ToolApprovalAllow {
			t.Fatalf("expected %s to auto-allow, got %v", toolName, d)
		}
	}
	if called {
		t.Fatal("read-only tools should not hit the interactive approval callback")
	}
}

func TestApprovalManagerAutoAllowsGoalStateTools(t *testing.T) {
	m := New()
	m.SetRules(config.PermissionConfig{DenyTools: []string{"update_goal"}})
	called := false
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		called = true
		return types.ToolApprovalDeny, nil
	})
	for _, toolName := range []string{"create_goal", "get_goal", "update_goal"} {
		if d, _ := m.Approve(toolName, "call-"+toolName, nil); d != types.ToolApprovalAllow {
			t.Fatalf("expected %s to auto-allow, got %v", toolName, d)
		}
	}
	if called {
		t.Fatal("goal state tools should not hit the interactive approval callback")
	}
}

func TestApprovalManagerDenyRulesOverrideReadOnlyAutoAllow(t *testing.T) {
	m := New()
	m.SetRules(config.PermissionConfig{DenyTools: []string{"read"}})
	if d, _ := m.Approve("read", "call-read", nil); d != types.ToolApprovalDeny {
		t.Fatalf("expected denyTools to override read auto-allow, got %v", d)
	}
}

func TestApprovalManagerDenyRulesOverrideCachedBashAllow(t *testing.T) {
	m := New()
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		return types.ToolApprovalAllowAlways, nil
	})
	args := map[string]any{"command": "rm -rf /tmp/nope"}
	if d, _ := m.Approve("bash", "call-allow", args); d != types.ToolApprovalAllowAlways {
		t.Fatalf("expected initial bash approval to cache, got %v", d)
	}

	m.SetRules(config.PermissionConfig{DenyTools: []string{"bash"}})
	if d, _ := m.Approve("bash", "call-deny", args); d != types.ToolApprovalDeny {
		t.Fatalf("expected denyTools to override cached bash allow, got %v", d)
	}
}

func TestApprovalManagerAutoAllowsSafeBashCommands(t *testing.T) {
	m := New()
	called := false
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		called = true
		return types.ToolApprovalDeny, nil
	})
	for _, command := range []string{
		"pwd",
		"git status --short",
		"go test ./pkg/tui",
		"curl -sS http://127.0.0.1:7081/",
		"rg -n ApprovalManager pkg/coding_agent 2>/dev/null",
		"bash -lc 'git status --short'",
		"curl -sS -o /dev/null http://127.0.0.1:7081/",
		"python <<'PY'\nprint('inspect')\nPY",
	} {
		if d, _ := m.Approve("bash", "call-safe", map[string]any{"command": command}); d != types.ToolApprovalAllow {
			t.Fatalf("expected safe bash command %q to auto-allow, got %v", command, d)
		}
	}
	if called {
		t.Fatal("safe bash commands should not hit the interactive approval callback")
	}
}

func TestApprovalManagerAllowBashPrefixesRestrictsSafeBashAutoAllow(t *testing.T) {
	m := New()
	m.SetRules(config.PermissionConfig{AllowBashPrefixes: []string{"git status"}})
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		t.Fatalf("allowBashPrefixes should decide safe bash command before callback for %s %#v", name, args)
		return types.ToolApprovalDeny, nil
	})
	if d, _ := m.Approve("bash", "call-allowed", map[string]any{"command": "git status --short"}); d != types.ToolApprovalAllow {
		t.Fatalf("expected matching safe bash command to be allowed, got %v", d)
	}
	if d, _ := m.Approve("bash", "call-denied", map[string]any{"command": "pwd"}); d != types.ToolApprovalDeny {
		t.Fatalf("expected non-matching safe bash command to be denied by allowBashPrefixes, got %v", d)
	}
}

func TestApprovalManagerScopesInteractiveBashAllowAlwaysToCommand(t *testing.T) {
	m := New()
	calls := 0
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		calls++
		switch calls {
		case 1:
			return types.ToolApprovalAllowAlways, nil
		case 2:
			return types.ToolApprovalDeny, nil
		default:
			t.Fatalf("unexpected approval callback call %d for %s %#v", calls, name, args)
			return types.ToolApprovalDeny, nil
		}
	})

	dangerArgs := map[string]any{"command": "rm -rf /tmp/nope"}
	if d, _ := m.Approve("bash", "call-danger-1", dangerArgs); d != types.ToolApprovalAllowAlways {
		t.Fatalf("expected first bash command to return allow_always, got %v", d)
	}
	if d, _ := m.Approve("bash", "call-danger-2", dangerArgs); d != types.ToolApprovalAllow {
		t.Fatalf("expected identical bash command to be cached as allow, got %v", d)
	}
	if calls != 1 {
		t.Fatalf("expected identical bash command not to prompt again, got %d callbacks", calls)
	}
	if d, _ := m.Approve("bash", "call-danger-other", map[string]any{"command": "rm -rf /tmp/other"}); d != types.ToolApprovalDeny {
		t.Fatalf("expected different bash command to ask callback and deny, got %v", d)
	}
	if calls != 2 {
		t.Fatalf("expected different bash command to prompt, got %d callbacks", calls)
	}
}

func TestApprovalManagerScopesInteractiveBashDenyAlwaysToCommand(t *testing.T) {
	m := New()
	calls := 0
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		calls++
		switch calls {
		case 1:
			return types.ToolApprovalDenyAlways, nil
		case 2:
			return types.ToolApprovalAllow, nil
		default:
			t.Fatalf("unexpected approval callback call %d for %s %#v", calls, name, args)
			return types.ToolApprovalDeny, nil
		}
	})

	dangerArgs := map[string]any{"command": "rm -rf /tmp/nope"}
	if d, _ := m.Approve("bash", "call-danger-1", dangerArgs); d != types.ToolApprovalDenyAlways {
		t.Fatalf("expected first bash command to return deny_always, got %v", d)
	}
	if d, _ := m.Approve("bash", "call-danger-2", dangerArgs); d != types.ToolApprovalDeny {
		t.Fatalf("expected identical bash command to be cached as deny, got %v", d)
	}
	if calls != 1 {
		t.Fatalf("expected identical denied bash command not to prompt again, got %d callbacks", calls)
	}
	if d, _ := m.Approve("bash", "call-other-danger", map[string]any{"command": "rm -rf /tmp/other"}); d != types.ToolApprovalAllow {
		t.Fatalf("expected different bash command to ask callback and allow, got %v", d)
	}
	if calls != 2 {
		t.Fatalf("expected different bash command to prompt, got %d callbacks", calls)
	}
}

func TestApprovalManagerProgrammaticBashAllowAlwaysRemainsToolWideForNonDangerousCommands(t *testing.T) {
	m := New()
	m.AllowAlways("bash")
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		t.Fatalf("programmatic bash allow should not prompt for %s %#v", name, args)
		return types.ToolApprovalDeny, nil
	})
	if d, _ := m.Approve("bash", "call-1", map[string]any{"command": "go test ./pkg/tui"}); d != types.ToolApprovalAllow {
		t.Fatalf("expected first bash command to be allowed, got %v", d)
	}
	if d, _ := m.Approve("bash", "call-2", map[string]any{"command": "git status --short"}); d != types.ToolApprovalAllow {
		t.Fatalf("expected second bash command to be allowed by tool-wide rule, got %v", d)
	}
}

func TestApprovalManagerDangerousBashBypassesToolWideAllow(t *testing.T) {
	m := New()
	m.AllowAlways("bash")
	calls := 0
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		calls++
		return types.ToolApprovalDeny, nil
	})
	if d, _ := m.Approve("bash", "call-danger", map[string]any{"command": "rm -rf /tmp/nope"}); d != types.ToolApprovalDeny {
		t.Fatalf("expected dangerous bash command to require approval and deny, got %v", d)
	}
	if calls != 1 {
		t.Fatalf("expected dangerous bash command to prompt despite tool-wide allow, got %d callbacks", calls)
	}
}

func TestApprovalManagerDangerousBashWrapperBypassesToolWideAllow(t *testing.T) {
	m := New()
	m.AllowAlways("bash")
	calls := 0
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		calls++
		return types.ToolApprovalDeny, nil
	})
	if d, _ := m.Approve("bash", "call-danger", map[string]any{"command": "bash -lc 'rm -rf /tmp/nope'"}); d != types.ToolApprovalDeny {
		t.Fatalf("expected dangerous wrapped bash command to require approval and deny, got %v", d)
	}
	if calls != 1 {
		t.Fatalf("expected dangerous wrapped bash command to prompt despite tool-wide allow, got %d callbacks", calls)
	}
}

func TestApprovalManagerDangerousBashBypassesAllowPrefix(t *testing.T) {
	m := New()
	m.SetRules(config.PermissionConfig{AllowBashPrefixes: []string{"rm "}})
	calls := 0
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		calls++
		return types.ToolApprovalDeny, nil
	})
	if d, _ := m.Approve("bash", "call-danger", map[string]any{"command": "rm -rf /tmp/nope"}); d != types.ToolApprovalDeny {
		t.Fatalf("expected dangerous bash command to bypass allow prefix and deny, got %v", d)
	}
	if calls != 1 {
		t.Fatalf("expected dangerous bash command to prompt despite allow prefix, got %d callbacks", calls)
	}
}

func TestApprovalManagerDangerousBashStillCachesExactCommand(t *testing.T) {
	m := New()
	calls := 0
	m.SetCallback(func(name, id string, args map[string]any) (types.ToolApprovalDecision, error) {
		calls++
		switch calls {
		case 1:
			return types.ToolApprovalAllowAlways, nil
		case 2:
			return types.ToolApprovalDeny, nil
		default:
			t.Fatalf("unexpected approval callback call %d for %s %#v", calls, name, args)
			return types.ToolApprovalDeny, nil
		}
	})

	dangerArgs := map[string]any{"command": "rm -rf /tmp/nope"}
	if d, _ := m.Approve("bash", "call-danger-1", dangerArgs); d != types.ToolApprovalAllowAlways {
		t.Fatalf("expected first dangerous bash command to return allow_always, got %v", d)
	}
	if d, _ := m.Approve("bash", "call-danger-2", dangerArgs); d != types.ToolApprovalAllow {
		t.Fatalf("expected identical dangerous bash command to be cached as allow, got %v", d)
	}
	if d, _ := m.Approve("bash", "call-danger-3", map[string]any{"command": "rm -rf /tmp/other"}); d != types.ToolApprovalDeny {
		t.Fatalf("expected different dangerous bash command to ask callback and deny, got %v", d)
	}
	if calls != 2 {
		t.Fatalf("expected only exact dangerous command to be cached, got %d callbacks", calls)
	}
}
