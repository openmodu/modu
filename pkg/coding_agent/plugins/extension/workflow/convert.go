package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

type jsonNullSentinel struct{}

type agentOptions struct {
	Label           string
	Phase           string
	Model           string
	Cwd             string
	Isolation       string
	Tools           []string
	DisallowedTools []string
	PermissionMode  string
	MaxTurns        int
	Thinking        string
	Skills          []string
	MemoryScope     string
}

type parallelTask struct {
	Prompt string
	agentOptions
}

func decodeMeta(t *lua.LTable) (metaInfo, error) {
	meta := metaInfo{
		Name:        strings.TrimSpace(stringValue(t, "name")),
		Description: strings.TrimSpace(stringValue(t, "description")),
		WhenToUse:   strings.TrimSpace(stringValue(t, "when_to_use")),
	}
	if meta.WhenToUse == "" {
		meta.WhenToUse = strings.TrimSpace(stringValue(t, "whenToUse"))
	}
	if meta.Name == "" {
		return meta, fmt.Errorf("meta.name must be a non-empty string")
	}
	if meta.Description == "" {
		return meta, fmt.Errorf("meta.description must be a non-empty string")
	}
	if phases, ok := t.RawGetString("phases").(*lua.LTable); ok {
		var decoded []phaseInfo
		var err error
		decoded, err = decodePhases(phases)
		if err != nil {
			return meta, err
		}
		meta.Phases = decoded
	}
	return meta, nil
}

func decodePhases(t *lua.LTable) ([]phaseInfo, error) {
	var out []phaseInfo
	for i := 1; i <= t.Len(); i++ {
		item, ok := t.RawGetInt(i).(*lua.LTable)
		if !ok {
			return nil, fmt.Errorf("each meta phase must be a table")
		}
		p := phaseInfo{
			Title:  strings.TrimSpace(stringValue(item, "title")),
			Detail: strings.TrimSpace(stringValue(item, "detail")),
			Model:  strings.TrimSpace(stringValue(item, "model")),
		}
		if p.Title == "" {
			return nil, fmt.Errorf("each meta phase must have a title string")
		}
		out = append(out, p)
	}
	return out, nil
}

func decodeAgentOptions(t *lua.LTable) (agentOptions, error) {
	opts := agentOptions{
		Label:          strings.TrimSpace(stringValue(t, "label")),
		Phase:          strings.TrimSpace(stringValue(t, "phase")),
		Model:          strings.TrimSpace(stringValue(t, "model")),
		Cwd:            strings.TrimSpace(stringValue(t, "cwd")),
		Isolation:      strings.TrimSpace(stringValue(t, "isolation")),
		PermissionMode: strings.TrimSpace(stringValue(t, "permission_mode")),
		MaxTurns:       intField(t, "max_turns"),
		Thinking:       strings.TrimSpace(stringValue(t, "thinking")),
		MemoryScope:    strings.TrimSpace(stringValue(t, "memory_scope")),
	}
	if opts.Isolation != "" && opts.Isolation != "worktree" {
		return opts, fmt.Errorf("isolation must be \"worktree\" when set")
	}
	var err error
	if opts.Tools, err = stringListField(t, "tools"); err != nil {
		return opts, err
	}
	if opts.DisallowedTools, err = stringListField(t, "disallowed_tools"); err != nil {
		return opts, err
	}
	if opts.Skills, err = stringListField(t, "skills"); err != nil {
		return opts, err
	}
	return opts, nil
}

func decodeParallelTasks(t *lua.LTable) ([]parallelTask, error) {
	var tasks []parallelTask
	for i := 1; i <= t.Len(); i++ {
		item, ok := t.RawGetInt(i).(*lua.LTable)
		if !ok {
			return nil, fmt.Errorf("parallel task %d must be a table", i)
		}
		opts, err := decodeAgentOptions(item)
		if err != nil {
			return nil, fmt.Errorf("parallel task %d: %w", i, err)
		}
		prompt := strings.TrimSpace(stringValue(item, "prompt"))
		if prompt == "" {
			prompt = strings.TrimSpace(stringValue(item, "task"))
		}
		if prompt == "" {
			return nil, fmt.Errorf("parallel task %d requires prompt", i)
		}
		tasks = append(tasks, parallelTask{Prompt: prompt, agentOptions: opts})
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("parallel() requires at least one task")
	}
	return tasks, nil
}

func stringValue(t *lua.LTable, key string) string {
	v := t.RawGetString(key)
	if s, ok := v.(lua.LString); ok {
		return string(s)
	}
	return ""
}

func intField(t *lua.LTable, key string) int {
	switch v := t.RawGetString(key).(type) {
	case lua.LNumber:
		f := float64(v)
		if f == math.Trunc(f) && f > 0 && f <= float64(math.MaxInt) {
			return int(f)
		}
	}
	return 0
}

func stringListField(t *lua.LTable, key string) ([]string, error) {
	raw := t.RawGetString(key)
	if raw == lua.LNil {
		return nil, nil
	}
	table, ok := raw.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, table.Len())
	for i := 1; i <= table.Len(); i++ {
		s, ok := table.RawGetInt(i).(lua.LString)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", key, i)
		}
		if trimmed := strings.TrimSpace(string(s)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

func goToLua(L *lua.LState, value any) lua.LValue {
	switch v := value.(type) {
	case nil:
		return jsonNullValue(L)
	case bool:
		return lua.LBool(v)
	case string:
		return lua.LString(v)
	case int:
		return lua.LNumber(v)
	case int64:
		return lua.LNumber(v)
	case float64:
		return lua.LNumber(v)
	case json.Number:
		f, _ := v.Float64()
		return lua.LNumber(f)
	case []any:
		t := L.NewTable()
		for i, item := range v {
			t.RawSetInt(i+1, goToLua(L, item))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.RawSetString(k, goToLua(L, v[k]))
		}
		return t
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return lua.LString(fmt.Sprint(value))
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return lua.LString(fmt.Sprint(value))
		}
		return goToLua(L, decoded)
	}
}

func jsonNullValue(L *lua.LState) lua.LValue {
	ud := L.NewUserData()
	ud.Value = jsonNullSentinel{}
	return ud
}

func luaToGo(value lua.LValue) (any, error) {
	return luaToGoSeen(value, map[*lua.LTable]bool{})
}

func luaToGoSeen(value lua.LValue, seen map[*lua.LTable]bool) (any, error) {
	if value == lua.LNil {
		return nil, nil
	}
	switch v := value.(type) {
	case lua.LBool:
		return bool(v), nil
	case lua.LNumber:
		f := float64(v)
		if math.Trunc(f) == f {
			return int64(f), nil
		}
		return f, nil
	case lua.LString:
		return string(v), nil
	case *lua.LTable:
		if seen[v] {
			return nil, fmt.Errorf("cyclic tables are not JSON-compatible")
		}
		seen[v] = true
		defer delete(seen, v)
		return luaTableToGo(v, seen)
	case *lua.LUserData:
		if _, ok := v.Value.(jsonNullSentinel); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("unsupported Lua userdata")
	default:
		return nil, fmt.Errorf("unsupported Lua value %s", value.Type().String())
	}
}

func luaTableToGo(t *lua.LTable, seen map[*lua.LTable]bool) (any, error) {
	maxIndex := 0
	count := 0
	isArray := true
	t.ForEach(func(k, v lua.LValue) {
		count++
		if n, ok := k.(lua.LNumber); ok && float64(n) == math.Trunc(float64(n)) && int(n) > 0 {
			if int(n) > maxIndex {
				maxIndex = int(n)
			}
			return
		}
		isArray = false
	})
	if isArray && count == maxIndex {
		out := make([]any, maxIndex)
		for i := 1; i <= maxIndex; i++ {
			item, err := luaToGoSeen(t.RawGetInt(i), seen)
			if err != nil {
				return nil, err
			}
			out[i-1] = item
		}
		return out, nil
	}
	out := map[string]any{}
	var firstErr error
	t.ForEach(func(k, v lua.LValue) {
		if firstErr != nil {
			return
		}
		var key string
		switch kk := k.(type) {
		case lua.LString:
			key = string(kk)
		case lua.LNumber:
			key = fmt.Sprint(float64(kk))
		default:
			firstErr = fmt.Errorf("table key %s is not JSON-compatible", k.Type().String())
			return
		}
		out[key], firstErr = luaToGoSeen(v, seen)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}
