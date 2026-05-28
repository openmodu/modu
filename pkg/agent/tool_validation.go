package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/openmodu/modu/pkg/types"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

var schemaCache sync.Map

func ValidateToolArguments(tool types.ToolDefinition, call types.ToolCallContent) (map[string]any, error) {
	if tool.Parameters == nil {
		if call.Arguments == nil {
			return map[string]any{}, nil
		}
		return call.Arguments, nil
	}

	schemaBytes, err := json.Marshal(tool.Parameters)
	if err != nil {
		return nil, err
	}
	schemaKey := string(schemaBytes)
	if cached, ok := schemaCache.Load(schemaKey); ok {
		return validateAgainstSchema(cached.(*jsonschema.Schema), call.Arguments, tool.Name)
	}

	compiled, err := compileSchema(schemaBytes)
	if err != nil {
		return nil, err
	}
	if cached, ok := schemaCache.LoadOrStore(schemaKey, compiled); ok {
		compiled = cached.(*jsonschema.Schema)
	}
	return validateAgainstSchema(compiled, call.Arguments, tool.Name)
}

func compileSchema(schemaBytes []byte) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
		return nil, err
	}
	return compiler.Compile("schema.json")
}

func validateAgainstSchema(schema *jsonschema.Schema, args any, toolName string) (map[string]any, error) {
	if err := schema.Validate(args); err != nil {
		return nil, fmt.Errorf("validation failed for tool %q: %v", toolName, err)
	}
	result, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid argument type for tool %q", toolName)
	}
	return result, nil
}
