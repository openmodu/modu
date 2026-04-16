package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// runOPC executes an opc CLI command and returns stdout+stderr.
func runOPC(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "uv", append([]string{"run", "python", "scripts/opc.py"}, args...)...)
	cmd.Dir = opcCLI
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if errOut := strings.TrimSpace(stderr.String()); errOut != "" {
		if output != "" {
			output += "\n" + errOut
		} else {
			output = errOut
		}
	}
	if err != nil && output == "" {
		output = err.Error()
	}
	return output, err
}

func textResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
}

// --- opc_tts ---

type TTSTool struct{}

func (t *TTSTool) Name() string        { return "opc_tts" }
func (t *TTSTool) Label() string       { return "OPC TTS" }
func (t *TTSTool) Description() string { return "Generate speech audio from text using edge-tts or qwen engine" }
func (t *TTSTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":       map[string]any{"type": "string", "description": "Text to convert to speech"},
			"engine":     map[string]any{"type": "string", "description": "TTS engine: edge-tts or qwen", "enum": []string{"edge-tts", "qwen"}},
			"voice":      map[string]any{"type": "string", "description": "Voice name for edge-tts"},
			"speaker":    map[string]any{"type": "string", "description": "Speaker for qwen: Vivian, Serena, Dylan, Eric, Ryan, Aiden, Uncle_Fu, Ono_Anna, Sohee"},
			"mode":       map[string]any{"type": "string", "description": "Qwen mode: custom_voice, voice_design, voice_clone"},
			"instruct":   map[string]any{"type": "string", "description": "Emotion/style instruction for qwen"},
			"language":   map[string]any{"type": "string", "description": "Language: Auto, Chinese, English, Japanese, Korean"},
			"rate":       map[string]any{"type": "string", "description": "Speech rate for edge-tts, e.g. +20%, -10%"},
			"pitch":      map[string]any{"type": "string", "description": "Pitch for edge-tts, e.g. +5Hz, -3Hz"},
			"output":     map[string]any{"type": "string", "description": "Output file path"},
			"model_size": map[string]any{"type": "string", "description": "Qwen model size: 1.7B or 0.6B"},
		},
		"required": []string{"text"},
	}
}

func (t *TTSTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return textResult("Error: text is required"), nil
	}
	cmdArgs := []string{"tts", text}
	cmdArgs = appendStringArg(cmdArgs, args, "engine", "-e")
	cmdArgs = appendStringArg(cmdArgs, args, "voice", "-v")
	cmdArgs = appendStringArg(cmdArgs, args, "speaker", "--speaker")
	cmdArgs = appendStringArg(cmdArgs, args, "mode", "--mode")
	cmdArgs = appendStringArg(cmdArgs, args, "instruct", "--instruct")
	cmdArgs = appendStringArg(cmdArgs, args, "language", "-l")
	cmdArgs = appendStringArg(cmdArgs, args, "rate", "--rate")
	cmdArgs = appendStringArg(cmdArgs, args, "pitch", "--pitch")
	cmdArgs = appendStringArg(cmdArgs, args, "output", "-o")
	cmdArgs = appendStringArg(cmdArgs, args, "model_size", "--model-size")

	output, err := runOPC(ctx, cmdArgs...)
	if err != nil {
		return textResult(fmt.Sprintf("TTS failed: %s", output)), nil
	}
	return textResult(output), nil
}

// --- opc_say ---

type SayTool struct{}

func (t *SayTool) Name() string        { return "opc_say" }
func (t *SayTool) Label() string       { return "OPC Say" }
func (t *SayTool) Description() string { return "Generate speech and play on a device (TTS + playback)" }
func (t *SayTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":     map[string]any{"type": "string", "description": "Text to speak"},
			"engine":   map[string]any{"type": "string", "description": "TTS engine: edge-tts or qwen"},
			"voice":    map[string]any{"type": "string", "description": "Voice name for edge-tts"},
			"speaker":  map[string]any{"type": "string", "description": "Speaker for qwen"},
			"instruct": map[string]any{"type": "string", "description": "Emotion/style instruction for qwen"},
			"device":   map[string]any{"type": "string", "description": "Target device name"},
		},
		"required": []string{"text"},
	}
}

func (t *SayTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return textResult("Error: text is required"), nil
	}
	cmdArgs := []string{"say", text}
	cmdArgs = appendStringArg(cmdArgs, args, "engine", "-e")
	cmdArgs = appendStringArg(cmdArgs, args, "voice", "-v")
	cmdArgs = appendStringArg(cmdArgs, args, "speaker", "--speaker")
	cmdArgs = appendStringArg(cmdArgs, args, "instruct", "--instruct")
	cmdArgs = appendStringArg(cmdArgs, args, "device", "-d")

	output, err := runOPC(ctx, cmdArgs...)
	if err != nil {
		return textResult(fmt.Sprintf("Say failed: %s", output)), nil
	}
	return textResult(output), nil
}

// --- opc_asr ---

type ASRTool struct{}

func (t *ASRTool) Name() string        { return "opc_asr" }
func (t *ASRTool) Label() string       { return "OPC ASR" }
func (t *ASRTool) Description() string { return "Transcribe audio to text, JSON, SRT, or ASS subtitles" }
func (t *ASRTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"audio":       map[string]any{"type": "string", "description": "Path to audio file (wav, mp3, flac)"},
			"format":      map[string]any{"type": "string", "description": "Output format: text, json, srt, ass", "enum": []string{"text", "json", "srt", "ass"}},
			"language":    map[string]any{"type": "string", "description": "Language hint: Chinese, English, etc."},
			"model_size":  map[string]any{"type": "string", "description": "ASR model size: 1.7B or 0.6B"},
			"output":      map[string]any{"type": "string", "description": "Output file path (for json format)"},
			"fix_dir":     map[string]any{"type": "string", "description": "Directory with fix_*.csv correction files"},
			"resume_from": map[string]any{"type": "string", "description": "Resume from stage: asr, break, fix, render"},
		},
		"required": []string{"audio"},
	}
}

func (t *ASRTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	audio, _ := args["audio"].(string)
	if audio == "" {
		return textResult("Error: audio file path is required"), nil
	}
	cmdArgs := []string{"asr", audio}
	cmdArgs = appendStringArg(cmdArgs, args, "format", "-f")
	cmdArgs = appendStringArg(cmdArgs, args, "language", "-l")
	cmdArgs = appendStringArg(cmdArgs, args, "model_size", "--model-size")
	cmdArgs = appendStringArg(cmdArgs, args, "output", "-o")
	cmdArgs = appendStringArg(cmdArgs, args, "fix_dir", "--fix-dir")
	cmdArgs = appendStringArg(cmdArgs, args, "resume_from", "--resume-from")

	output, err := runOPC(ctx, cmdArgs...)
	if err != nil {
		return textResult(fmt.Sprintf("ASR failed: %s", output)), nil
	}
	return textResult(output), nil
}

// --- opc_voices ---

type VoicesTool struct{}

func (t *VoicesTool) Name() string        { return "opc_voices" }
func (t *VoicesTool) Label() string       { return "OPC Voices" }
func (t *VoicesTool) Description() string { return "List available TTS voices for a given engine" }
func (t *VoicesTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"engine": map[string]any{"type": "string", "description": "TTS engine: edge-tts or qwen", "enum": []string{"edge-tts", "qwen"}},
		},
	}
}

func (t *VoicesTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	cmdArgs := []string{"voices"}
	cmdArgs = appendStringArg(cmdArgs, args, "engine", "-e")
	output, err := runOPC(ctx, cmdArgs...)
	if err != nil {
		return textResult(fmt.Sprintf("Voices failed: %s", output)), nil
	}
	return textResult(output), nil
}

// --- opc_discover ---

type DiscoverTool struct{}

func (t *DiscoverTool) Name() string        { return "opc_discover" }
func (t *DiscoverTool) Label() string       { return "OPC Discover" }
func (t *DiscoverTool) Description() string { return "Discover AirPlay and DLNA playback devices on the network" }
func (t *DiscoverTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"set_default": map[string]any{"type": "boolean", "description": "Automatically set the default device if only one found"},
		},
	}
}

func (t *DiscoverTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	cmdArgs := []string{"discover"}
	if setDefault, _ := args["set_default"].(bool); setDefault {
		cmdArgs = append(cmdArgs, "--set-default")
	}
	output, err := runOPC(ctx, cmdArgs...)
	if err != nil {
		return textResult(fmt.Sprintf("Discover failed: %s", output)), nil
	}
	return textResult(output), nil
}

// --- opc_config ---

type ConfigTool struct{}

func (t *ConfigTool) Name() string        { return "opc_config" }
func (t *ConfigTool) Label() string       { return "OPC Config" }
func (t *ConfigTool) Description() string { return "View or update OPC configuration settings" }
func (t *ConfigTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"show":             map[string]any{"type": "boolean", "description": "Show current configuration"},
			"set_engine":       map[string]any{"type": "string", "description": "Set default TTS engine: edge-tts or qwen"},
			"set_speaker":      map[string]any{"type": "string", "description": "Set default qwen speaker"},
			"set_mode":         map[string]any{"type": "string", "description": "Set qwen mode: custom_voice, voice_design, voice_clone"},
			"set_model_size":   map[string]any{"type": "string", "description": "Set qwen model size: 1.7B or 0.6B"},
			"device":           map[string]any{"type": "string", "description": "Set default playback device name"},
			"set_model_source": map[string]any{"type": "string", "description": "Set model download source: modelscope or huggingface"},
		},
	}
}

func (t *ConfigTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	cmdArgs := []string{"config"}
	if show, _ := args["show"].(bool); show {
		cmdArgs = append(cmdArgs, "--show")
	}
	if v, _ := args["set_engine"].(string); v != "" {
		cmdArgs = append(cmdArgs, "--set-engine", v)
	}
	if v, _ := args["set_speaker"].(string); v != "" {
		cmdArgs = append(cmdArgs, "--set-speaker", v)
	}
	if v, _ := args["set_mode"].(string); v != "" {
		cmdArgs = append(cmdArgs, "--set-mode", v)
	}
	if v, _ := args["set_model_size"].(string); v != "" {
		cmdArgs = append(cmdArgs, "--set-model-size", v)
	}
	if v, _ := args["device"].(string); v != "" {
		cmdArgs = append(cmdArgs, "--device", v)
	}
	if v, _ := args["set_model_source"].(string); v != "" {
		cmdArgs = append(cmdArgs, "--set-model-source", v)
	}
	output, err := runOPC(ctx, cmdArgs...)
	if err != nil {
		return textResult(fmt.Sprintf("Config failed: %s", output)), nil
	}
	return textResult(output), nil
}

// --- opc_asr_split ---

type ASRSplitTool struct{}

func (t *ASRSplitTool) Name() string  { return "opc_asr_split" }
func (t *ASRSplitTool) Label() string { return "OPC ASR Split" }
func (t *ASRSplitTool) Description() string {
	return "Split a long subtitle line at a specific text position. Used after ASR pipeline check stage reports lines exceeding max_chars."
}
func (t *ASRSplitTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"lines_json": map[string]any{"type": "string", "description": "Path to the .lines.json file"},
			"line":       map[string]any{"type": "integer", "description": "Line number to split"},
			"after":      map[string]any{"type": "string", "description": "Text fragment to split after (must be unique in the line)"},
		},
		"required": []string{"lines_json", "line", "after"},
	}
}

func (t *ASRSplitTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	linesJSON, _ := args["lines_json"].(string)
	line := int(toFloat(args["line"]))
	after, _ := args["after"].(string)
	if linesJSON == "" || after == "" {
		return textResult("Error: lines_json, line, and after are all required"), nil
	}
	cmdArgs := []string{"asr-split", linesJSON, "--line", fmt.Sprintf("%d", line), "--after", after}
	output, err := runOPC(ctx, cmdArgs...)
	if err != nil {
		return textResult(fmt.Sprintf("ASR split failed: %s", output)), nil
	}
	return textResult(output), nil
}

// --- helpers ---

func appendStringArg(cmdArgs []string, args map[string]any, key, flag string) []string {
	if v, _ := args[key].(string); v != "" {
		return append(cmdArgs, flag, v)
	}
	return cmdArgs
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
