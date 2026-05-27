// File-based intercom inbox.
//
// This is a deliberately minimal port of pi-subagents' src/intercom/* layer.
// Pi has a full publisher / subscriber pipeline with retry, mode toggles
// (`off`/`fork-only`/`always`), and intercom-aware tool delivery. We only
// implement what's necessary to let a child agent leave structured messages
// for its parent (or another known task) and let the parent read them via a
// management action:
//
//   - File layout: <tool-results>/<project>/subagents/intercom/<taskID>.jsonl,
//     one message per line.
//   - Writers: the `subagent_intercom_send` tool (registered by the extension).
//   - Readers: `subagent action=intercom id=<taskID>`.
//
// The rest of pi's intercom features (notification routing, mode policies,
// auto-detach on session shutdown) stays deferred — see PARITY.md.

package subagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// intercomMessage is one entry in an intercom JSONL file.
type intercomMessage struct {
	From      string `json:"from,omitempty"`
	Timestamp int64  `json:"ts"`
	Text      string `json:"text"`
}

// intercomFilePath returns the on-disk path for taskID's inbox. Empty
// taskID surfaces an error so we don't accidentally collapse all messages
// into a single shared file.
func intercomFilePath(ext *Extension, taskID string) (string, error) {
	id := strings.TrimSpace(taskID)
	if id == "" {
		return "", fmt.Errorf("intercom taskId required")
	}
	if ext == nil || ext.api == nil {
		return "", fmt.Errorf("intercom path requires the host API")
	}
	return filepath.Join(ext.api.AgentDir(), "tool-results", projectKey(ext.api.Cwd()), "subagents", "intercom", id+".jsonl"), nil
}

// appendIntercomMessage adds one message to taskID's inbox. The file is
// created on demand; concurrent writers serialise via os.OpenFile with
// append semantics — single-line JSON encoding keeps the file
// self-recoverable even if a writer is killed mid-line.
func appendIntercomMessage(ext *Extension, taskID, from, text string) error {
	path, err := intercomFilePath(ext, taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	msg := intercomMessage{
		From:      strings.TrimSpace(from),
		Timestamp: time.Now().UnixMilli(),
		Text:      text,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// readIntercomMessages returns every line in taskID's inbox, parsed as
// intercomMessage. Missing inbox returns an empty slice rather than an
// error so a caller polling early doesn't see false negatives.
func readIntercomMessages(ext *Extension, taskID string) ([]intercomMessage, error) {
	path, err := intercomFilePath(ext, taskID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []intercomMessage
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var msg intercomMessage
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		out = append(out, msg)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// runIntercomAction implements `subagent action=intercom id=<taskID>`. It
// returns the inbox contents in a human-readable transcript. Honors the
// optional `since` arg (epoch millis) to filter older messages.
func runIntercomAction(ext *Extension, args map[string]any) (string, error) {
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf(`intercom requires "id"`)
	}
	msgs, err := readIntercomMessages(ext, id)
	if err != nil {
		return "", err
	}
	since, _ := numericInt(args["since"])
	if len(msgs) == 0 {
		return fmt.Sprintf("No intercom messages for task %s.", id), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Intercom messages for task %s:", id)
	for _, msg := range msgs {
		if since > 0 && msg.Timestamp < int64(since) {
			continue
		}
		from := msg.From
		if from == "" {
			from = "(unknown)"
		}
		fmt.Fprintf(&b, "\n- [%s @ %s] %s", from, time.UnixMilli(msg.Timestamp).Format(time.RFC3339), msg.Text)
	}
	return b.String(), nil
}
