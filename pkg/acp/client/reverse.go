package client

import (
	"strings"

	"github.com/openmodu/modu/pkg/acp/jsonrpc"
)

func (c *Client) handleReverse(msg *jsonrpc.Message) {
	if msg.ID == nil {
		return
	}
	id := *msg.ID
	switch msg.Method {
	case "session/request_permission":
		c.handlePermission(id, msg)
	case "fs/read_text_file":
		c.handleReadFile(id, msg)
	case "fs/write_text_file":
		c.handleWriteFile(id, msg)
	default:
		c.sendError(id, jsonrpc.MethodNotFound, "method not found: "+msg.Method)
	}
}

func (c *Client) handlePermission(id int, msg *jsonrpc.Message) {
	if c.onPermission == nil {
		c.sendError(id, jsonrpc.InternalError, "no permission handler registered")
		return
	}
	var req PermissionRequest
	if err := msg.ParseParams(&req); err != nil {
		c.sendError(id, jsonrpc.InvalidParams, "invalid params")
		return
	}
	optionID := c.onPermission(&req)
	outcome := "selected"
	if strings.HasPrefix(optionID, "reject") {
		outcome = "rejected"
	}
	c.sendResponse(id, map[string]any{
		"outcome": map[string]any{
			"outcome":  outcome,
			"optionId": optionID,
		},
	})
}

func (c *Client) handleReadFile(id int, msg *jsonrpc.Message) {
	if c.fs == nil {
		c.sendError(id, jsonrpc.InternalError, "fs handler not configured")
		return
	}
	var p struct {
		Path string `json:"path"`
	}
	if err := msg.ParseParams(&p); err != nil {
		c.sendError(id, jsonrpc.InvalidParams, "invalid params")
		return
	}
	content, err := c.fs.ReadTextFile(p.Path)
	if err != nil {
		c.sendError(id, jsonrpc.InternalError, err.Error())
		return
	}
	c.sendResponse(id, map[string]string{"content": content})
}

func (c *Client) handleWriteFile(id int, msg *jsonrpc.Message) {
	if c.fs == nil {
		c.sendError(id, jsonrpc.InternalError, "fs handler not configured")
		return
	}
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := msg.ParseParams(&p); err != nil {
		c.sendError(id, jsonrpc.InvalidParams, "invalid params")
		return
	}
	if err := c.fs.WriteTextFile(p.Path, p.Content); err != nil {
		c.sendError(id, jsonrpc.InternalError, err.Error())
		return
	}
	c.sendResponse(id, nil)
}
