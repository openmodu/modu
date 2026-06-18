package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

func schemaContractPrompt(schema map[string]any) string {
	if len(schema) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		data, _ = json.Marshal(schema)
	}
	return "Final output contract:\n" +
		"- Return only one JSON value and no prose.\n" +
		"- The JSON value must satisfy this JSON Schema subset:\n" +
		string(data)
}

func structuredOutputRetryPrompt(original, previous string, cause error) string {
	return strings.Join([]string{
		"Retry the same workflow subtask.",
		"The previous response did not satisfy the required structured output contract.",
		"Validation error: " + cause.Error(),
		"Previous response:",
		previous,
		"Original task:",
		original,
	}, "\n\n")
}

func parseStructuredOutput(text string, schema map[string]any) (any, error) {
	value, err := parseJSONValue(text)
	if err != nil {
		return nil, err
	}
	if err := validateAgainstSchema(value, schema, "$"); err != nil {
		return nil, err
	}
	return normalizeJSONNumbers(value), nil
}

func parseJSONValue(text string) (any, error) {
	candidates := []string{strings.TrimSpace(text)}
	if fenced := fencedJSON(text); fenced != "" {
		candidates = append(candidates, fenced)
	}
	if extracted := extractJSON(text); extracted != "" {
		candidates = append(candidates, extracted)
	}
	var lastErr error
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		var value any
		dec := json.NewDecoder(strings.NewReader(candidate))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			lastErr = err
			continue
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			if err == nil {
				lastErr = fmt.Errorf("structured output contains trailing JSON values")
			} else {
				lastErr = err
			}
			continue
		}
		return value, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no JSON value found")
	}
	return nil, fmt.Errorf("structured output must be valid JSON: %w", lastErr)
}

func fencedJSON(text string) string {
	start := strings.Index(text, "```")
	if start < 0 {
		return ""
	}
	rest := text[start+3:]
	end := strings.Index(rest, "```")
	if end < 0 {
		return ""
	}
	body := strings.TrimSpace(rest[:end])
	if newline := strings.IndexByte(body, '\n'); newline >= 0 {
		lang := strings.TrimSpace(body[:newline])
		if strings.EqualFold(lang, "json") || strings.EqualFold(lang, "javascript") || strings.EqualFold(lang, "js") {
			body = strings.TrimSpace(body[newline+1:])
		}
	}
	return body
}

func extractJSON(text string) string {
	for i, r := range text {
		if r != '{' && r != '[' {
			continue
		}
		if end := matchingJSONEnd(text[i:]); end > 0 {
			return text[i : i+end]
		}
	}
	return ""
}

func matchingJSONEnd(text string) int {
	var stack []byte
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return 0
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1
			}
		}
	}
	return 0
}

func validateSchemaDefinition(schema map[string]any, path string) error {
	if rawType, ok := schema["type"]; ok {
		if err := validateSchemaType(rawType, path+".type"); err != nil {
			return err
		}
	}
	if rawRequired, ok := schema["required"]; ok {
		items, ok := rawRequired.([]any)
		if !ok {
			return fmt.Errorf("%s.required must be an array of strings", path)
		}
		for i, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("%s.required[%d] must be a string", path, i+1)
			}
		}
	}
	if rawProps, ok := schema["properties"]; ok {
		props, ok := rawProps.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.properties must be an object", path)
		}
		for name, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be an object", path, name)
			}
			if err := validateSchemaDefinition(child, path+".properties."+name); err != nil {
				return err
			}
		}
	}
	if rawItems, ok := schema["items"]; ok {
		child, ok := rawItems.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items must be an object", path)
		}
		if err := validateSchemaDefinition(child, path+".items"); err != nil {
			return err
		}
	}
	if rawEnum, ok := schema["enum"]; ok {
		if _, ok := rawEnum.([]any); !ok {
			return fmt.Errorf("%s.enum must be an array", path)
		}
	}
	return nil
}

func validateSchemaType(raw any, path string) error {
	switch v := raw.(type) {
	case string:
		if !supportedSchemaType(v) {
			return fmt.Errorf("%s has unsupported type %q", path, v)
		}
	case []any:
		if len(v) == 0 {
			return fmt.Errorf("%s must not be empty", path)
		}
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("%s[%d] must be a string", path, i+1)
			}
			if !supportedSchemaType(s) {
				return fmt.Errorf("%s[%d] has unsupported type %q", path, i+1, s)
			}
		}
	default:
		return fmt.Errorf("%s must be a string or array of strings", path)
	}
	return nil
}

func supportedSchemaType(t string) bool {
	switch t {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func validateAgainstSchema(value any, schema map[string]any, path string) error {
	if rawEnum, ok := schema["enum"]; ok {
		if !matchesEnum(value, rawEnum.([]any)) {
			return fmt.Errorf("%s must match enum", path)
		}
	}
	if rawType, ok := schema["type"]; ok {
		if !valueMatchesType(value, rawType) {
			return fmt.Errorf("%s must be %s", path, schemaTypeDescription(rawType))
		}
	}
	inferredType := ""
	if rawType, ok := schema["type"].(string); ok {
		inferredType = rawType
	}
	if _, ok := schema["properties"]; ok && (inferredType == "" || inferredType == "object") {
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}
		if err := validateRequired(obj, schema, path); err != nil {
			return err
		}
		props := schema["properties"].(map[string]any)
		for name, raw := range props {
			if childValue, ok := obj[name]; ok {
				if err := validateAgainstSchema(childValue, raw.(map[string]any), path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	if _, ok := schema["items"]; ok && (inferredType == "" || inferredType == "array") {
		arr, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be array", path)
		}
		itemSchema := schema["items"].(map[string]any)
		for i, item := range arr {
			if err := validateAgainstSchema(item, itemSchema, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRequired(obj map[string]any, schema map[string]any, path string) error {
	rawRequired, ok := schema["required"]
	if !ok {
		return nil
	}
	for _, item := range rawRequired.([]any) {
		name := item.(string)
		if _, ok := obj[name]; !ok {
			return fmt.Errorf("%s.%s is required", path, name)
		}
	}
	return nil
}

func valueMatchesType(value any, rawType any) bool {
	switch t := rawType.(type) {
	case string:
		return valueMatchesSingleType(value, t)
	case []any:
		for _, item := range t {
			if valueMatchesSingleType(value, item.(string)) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func valueMatchesSingleType(value any, t string) bool {
	switch t {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		return isJSONNumber(value)
	case "integer":
		return isJSONInteger(value)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func isJSONNumber(value any) bool {
	switch n := value.(type) {
	case json.Number:
		_, err := n.Float64()
		return err == nil
	case float64, float32, int, int64, int32:
		return true
	default:
		return false
	}
}

func isJSONInteger(value any) bool {
	switch n := value.(type) {
	case json.Number:
		_, err := n.Int64()
		if err == nil {
			return true
		}
		f, err := n.Float64()
		return err == nil && math.Trunc(f) == f && f >= math.MinInt64 && f <= math.MaxInt64
	case float64:
		return math.Trunc(n) == n
	case float32:
		return math.Trunc(float64(n)) == float64(n)
	case int, int64, int32:
		return true
	default:
		return false
	}
}

func schemaTypeDescription(raw any) string {
	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprint(raw)
	}
	return string(data)
}

func matchesEnum(value any, items []any) bool {
	normalized := normalizeJSONNumbers(value)
	valueData, err := json.Marshal(normalized)
	if err != nil {
		return false
	}
	for _, item := range items {
		itemData, err := json.Marshal(normalizeJSONNumbers(item))
		if err == nil && bytes.Equal(valueData, itemData) {
			return true
		}
	}
	return false
}

func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return string(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeJSONNumbers(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = normalizeJSONNumbers(item)
		}
		return out
	default:
		return value
	}
}
