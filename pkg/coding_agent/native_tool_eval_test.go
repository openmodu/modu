package coding_agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codingtools "github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/evals"
	"github.com/openmodu/modu/pkg/types"
)

// TestNativeToolRuntimeSafetyEval is a deterministic modu_eval gate for the
// native tool runtime contract, especially the Claude-style read-before-edit
// and read-before-overwrite protections.
func TestNativeToolRuntimeSafetyEval(t *testing.T) {
	evals.Run(t, "coding tools: native runtime safety", func(e *evals.EvalT) {
		dir := t.TempDir()
		targetPath := filepath.Join(dir, "target.txt")
		if err := os.WriteFile(targetPath, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		provider := codingtools.NewProvider(codingtools.ToolSetCoding)
		tools := provider.Tools(types.ToolContext{Cwd: dir})
		readTool := evalToolByName(e, tools, "read")
		editTool := evalToolByName(e, tools, "edit")
		writeTool := evalToolByName(e, tools, "write")
		bashTool := evalToolByName(e, tools, "bash")

		unreadWriteText := evalExecuteToolText(e, writeTool, map[string]any{
			"path":    "target.txt",
			"content": "new\n",
		})
		evals.AssertT(e,
			"write rejects overwriting an existing file before read",
			unreadWriteText,
			strings.Contains(unreadWriteText, "File has not been read yet"),
		)

		_ = evalExecuteToolText(e, readTool, map[string]any{
			"path": "target.txt",
		})
		editText := evalExecuteToolText(e, editTool, map[string]any{
			"path":     "target.txt",
			"old_text": "old",
			"new_text": "mid",
		})
		evals.AssertT(e,
			"edit succeeds after a fresh full read",
			editText,
			strings.Contains(editText, "Successfully edited"),
		)

		writeText := evalExecuteToolText(e, writeTool, map[string]any{
			"path":    "target.txt",
			"content": "final\n",
		})
		evals.AssertT(e,
			"write succeeds after edit refreshes shared read state",
			writeText,
			strings.Contains(writeText, "updated successfully"),
		)

		data, err := os.ReadFile(targetPath)
		if err != nil {
			e.Fatal(err)
		}
		evals.AssertT(e,
			"final file content reflects the allowed write",
			string(data),
			string(data) == "final\n",
		)

		searchPath := filepath.Join(dir, "data.txt")
		if err := os.WriteFile(searchPath, []byte("needle\n"), 0o644); err != nil {
			e.Fatal(err)
		}
		bashSearchText := evalExecuteToolText(e, bashTool, map[string]any{
			"command": "rg -n needle data.txt",
		})
		evals.AssertT(e,
			"bash blocks common rg flag searches so native grep handles them",
			bashSearchText,
			strings.Contains(bashSearchText, "use the grep tool for normal content search"),
		)

		bashGlobSearchText := evalExecuteToolText(e, bashTool, map[string]any{
			"command": "rg -g '*.go' needle",
		})
		evals.AssertT(e,
			"bash blocks rg glob-filter searches so native grep glob handles them",
			bashGlobSearchText,
			strings.Contains(bashGlobSearchText, "use the grep tool for normal content search"),
		)

		bashSedSearchText := evalExecuteToolText(e, bashTool, map[string]any{
			"command": "sed -n '/needle/p' data.txt",
		})
		evals.AssertT(e,
			"bash blocks simple sed print searches so native grep handles them",
			bashSedSearchText,
			strings.Contains(bashSedSearchText, "use the grep tool for normal content search"),
		)

		bashAwkSearchText := evalExecuteToolText(e, bashTool, map[string]any{
			"command": "awk '/needle/' data.txt",
		})
		evals.AssertT(e,
			"bash blocks simple awk pattern searches so native grep handles them",
			bashAwkSearchText,
			strings.Contains(bashAwkSearchText, "use the grep tool for normal content search"),
		)
	})
}

func evalToolByName(e *evals.EvalT, tools []types.Tool, name string) types.Tool {
	e.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	e.Fatalf("tool %s not found", name)
	return nil
}

func evalExecuteToolText(e *evals.EvalT, tool types.Tool, args map[string]any) string {
	e.Helper()
	result, err := tool.Execute(context.Background(), "eval-tool", args, nil)
	if err != nil {
		e.Fatalf("execute %s: %v", tool.Name(), err)
	}
	var parts []string
	for _, block := range result.Content {
		if text, ok := block.(*types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}
