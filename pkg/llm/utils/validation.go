package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

var (
	schemaMu    sync.Mutex
	schemaCache = map[string]*jsonschema.Schema{}
)

func ValidateToolCall(tools []llm.ToolDefinition, toolCall llm.ToolCall) (map[string]any, error) {
	var tool *llm.ToolDefinition
	for i := range tools {
		if tools[i].Name == toolCall.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("tool %q not found", toolCall.Name)
	}
	return ValidateToolArguments(*tool, toolCall)
}

func ValidateToolArguments(tool llm.ToolDefinition, toolCall llm.ToolCall) (map[string]any, error) {
	if tool.Parameters == nil {
		return toolCall.Arguments, nil
	}

	schemaBytes, err := json.Marshal(tool.Parameters)
	if err != nil {
		return nil, err
	}

	schemaKey := string(schemaBytes)

	schemaMu.Lock()
	schema, ok := schemaCache[schemaKey]
	schemaMu.Unlock()

	if !ok {
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
			return nil, err
		}
		compiled, err := compiler.Compile("schema.json")
		if err != nil {
			return nil, err
		}
		schemaMu.Lock()
		schemaCache[schemaKey] = compiled
		schemaMu.Unlock()
		schema = compiled
	}

	if err := schema.Validate(toolCall.Arguments); err != nil {
		return nil, fmt.Errorf("validation failed for tool %q: %v", toolCall.Name, err)
	}

	return toolCall.Arguments, nil
}
