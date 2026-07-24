package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

func moduTUIToolOutputSlash(session *coding_agent.CodingSession, line string) (bool, string) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "/tool-output" {
		return false, ""
	}
	if len(fields) != 2 {
		return true, "usage: /tool-output <call-id>"
	}
	out, err := moduTUIToolOutputByID(session, fields[1])
	if err != nil {
		return true, "error: " + err.Error()
	}
	return true, out
}

func moduTUIToolOutputByID(session *coding_agent.CodingSession, callID string) (string, error) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return "", errors.New("call-id is required")
	}
	if session == nil {
		return "", errors.New("session is not available")
	}
	messages := session.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(types.ToolResultMessage)
		if !ok {
			if ptr, ptrOK := messages[i].(*types.ToolResultMessage); ptrOK && ptr != nil {
				msg = *ptr
				ok = true
			}
		}
		if !ok || msg.ToolCallID != callID {
			continue
		}
		if artifact := toolArtifactInfoFromDetails(msg.Details); artifact.Path != "" {
			data, err := os.ReadFile(artifact.Path)
			if err != nil {
				return "", fmt.Errorf("read artifact: %w", err)
			}
			return string(data), nil
		}
		if text := toolOutputFromContent(msg.ToolName, msg.IsError, msg.Content); text != "" {
			return text, nil
		}
		return "", fmt.Errorf("tool %s has no output", callID)
	}
	return "", fmt.Errorf("tool call not found: %s", callID)
}
