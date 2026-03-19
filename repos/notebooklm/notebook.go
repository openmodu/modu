package notebooklm

import (
	"context"
	"fmt"

	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

// ========== Notebook Operations ==========

// ListNotebooks returns all notebooks
func (c *Client) ListNotebooks(ctx context.Context) ([]vo.Notebook, error) {
	params := []any{nil, 1, nil, []any{2}}
	result, err := c.rpcCall(ctx, vo.RPCListNotebooks, params, "/")
	if err != nil {
		return nil, err
	}

	return parseNotebookList(result)
}

// CreateNotebook creates a new notebook
func (c *Client) CreateNotebook(ctx context.Context, title string) (*vo.Notebook, error) {
	params := []any{title, nil, nil, []any{2}, []any{1}}
	result, err := c.rpcCall(ctx, vo.RPCCreateNotebook, params, "/")
	if err != nil {
		return nil, err
	}

	return parseNotebook(result)
}

// GetNotebook retrieves a notebook by ID
func (c *Client) GetNotebook(ctx context.Context, notebookID string) (*vo.Notebook, error) {
	params := []any{notebookID, nil, []any{2}, nil, 0}
	result, err := c.rpcCall(ctx, vo.RPCGetNotebook, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	arr, ok := result.([]any)
	if !ok || len(arr) == 0 {
		return nil, fmt.Errorf("invalid notebook response")
	}

	return parseNotebook(arr[0])
}

// RenameNotebook renames a notebook
func (c *Client) RenameNotebook(ctx context.Context, notebookID, newTitle string) error {
	params := []any{notebookID, newTitle}
	_, err := c.rpcCall(ctx, vo.RPCRenameNotebook, params, "/notebook/"+notebookID)
	return err
}

// DeleteNotebook deletes a notebook
func (c *Client) DeleteNotebook(ctx context.Context, notebookID string) error {
	params := []any{[]any{notebookID}}
	_, err := c.rpcCall(ctx, vo.RPCDeleteNotebook, params, "/")
	return err
}
