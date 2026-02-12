package utils

import "encoding/json"

func ParseStreamingJSON(partial string) any {
	if partial == "" {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal([]byte(partial), &out); err == nil {
		return out
	}
	return map[string]any{}
}
