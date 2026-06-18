package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// apply merges a yaml-decoded map into c. Unknown keys produce errors so a
// typo'd workflow setting does not silently keep the default behavior.
func (c *Config) apply(cfg map[string]any) error {
	for k, v := range cfg {
		switch k {
		case "disabled", "disable", "disable_workflows", "disable-workflows":
			b, err := workflowConfigBool(k, v)
			if err != nil {
				return err
			}
			c.Disabled = b
		case "concurrency":
			n, err := workflowConfigPositiveInt(k, v)
			if err != nil {
				return err
			}
			c.Concurrency = n
		case "max_agents", "max-agents":
			n, err := workflowConfigPositiveInt(k, v)
			if err != nil {
				return err
			}
			c.MaxAgents = n
		default:
			return fmt.Errorf("unknown config key: %s", k)
		}
	}
	return nil
}

func workflowConfigBool(k string, v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be bool, got %T", k, v)
	}
	return b, nil
}

func workflowConfigPositiveInt(k string, v any) (int, error) {
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		if x != float64(int(x)) {
			return 0, fmt.Errorf("%s must be positive int, got %T", k, v)
		}
		n = int(x)
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be positive int, got %T", k, v)
		}
		n = int(i)
	default:
		return 0, fmt.Errorf("%s must be positive int, got %T", k, v)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s must be positive int, got %d", k, n)
	}
	return n, nil
}

func workflowDisabledByEnv() bool {
	for _, name := range []string{
		"MODU_CODE_DISABLE_WORKFLOWS",
		"CLAUDE_CODE_DISABLE_WORKFLOWS",
	} {
		if workflowEnvTruthy(os.Getenv(name)) {
			return true
		}
	}
	return false
}

func workflowEnvTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
