package tokenkit

import (
	"encoding/json"
	"strconv"
)

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := strconv.Atoi(n.String())
		return i
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

func parseInt64(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}
