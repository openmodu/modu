package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "modu-mcp-test", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "echo",
		Title:       "Echo",
		Description: "Echo a value with process context",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
			"required": []string{"value"},
		},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &args)
		cwd, _ := os.Getwd()
		structured := map[string]any{
			"value": args.Value,
			"cwd":   cwd,
			"env":   os.Getenv("MCP_TEST_VALUE"),
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "echo:" + args.Value}},
			StructuredContent: structured,
		}, nil
	})
	server.AddTool(&mcp.Tool{
		Name:        "image",
		Description: "Return a tiny image",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.ImageContent{Data: []byte{0x01, 0x02, 0x03}, MIMEType: "image/png"}},
		}, nil
	})
	server.AddTool(&mcp.Tool{
		Name:        "fail",
		Description: "Return an MCP tool error",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "expected failure"}},
			IsError: true,
		}, nil
	})
	server.AddTool(&mcp.Tool{
		Name:        "slow",
		Description: "Wait before returning",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-time.After(2 * time.Second):
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "slow"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}
