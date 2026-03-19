package rpc

import (
	"encoding/json"
	"fmt"
	"net/url"

	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

// EncodeRPCRequest builds the triple-nested array structure for batchexecute
// Format: [[[rpc_id, json_params, null, "generic"]]]
func EncodeRPCRequest(method vo.RPCMethod, params []any) ([]any, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	inner := []any{string(method), string(paramsJSON), nil, "generic"}
	return []any{[]any{inner}}, nil
}

// BuildRequestBody creates the form-encoded request body
func BuildRequestBody(rpcRequest []any, csrfToken string) (string, error) {
	fReq, err := json.Marshal(rpcRequest)
	if err != nil {
		return "", err
	}

	body := "f.req=" + url.QueryEscape(string(fReq)) + "&"
	if csrfToken != "" {
		body += "at=" + url.QueryEscape(csrfToken) + "&"
	}

	return body, nil
}

// BuildURL constructs the batchexecute URL with query parameters
func BuildURL(method vo.RPCMethod, sessionID string, sourcePath string) string {
	params := url.Values{}
	params.Set("rpcids", string(method))
	params.Set("source-path", sourcePath)
	if sessionID != "" {
		params.Set("f.sid", sessionID)
	}
	params.Set("rt", "c") // chunked response mode

	return BatchExecuteURL + "?" + params.Encode()
}

// BuildChatURL constructs the chat/query endpoint URL
func BuildChatURL(sessionID string, reqID int) string {
	params := url.Values{}
	params.Set("bl", "boq_labs-tailwind-frontend_20251221.14_p0")
	params.Set("hl", "en")
	params.Set("_reqid", fmt.Sprintf("%d", reqID))
	params.Set("rt", "c")
	if sessionID != "" {
		params.Set("f.sid", sessionID)
	}

	return QueryURL + "?" + params.Encode()
}

// EncodeChatRequest builds the chat request body with CSRF token
func EncodeChatRequest(question string, sourceIDs []string, conversationID string, history []any, csrfToken string) (string, error) {
	// Build source array: [[sid]] for each source (2 levels of nesting)
	// Result: [[[sid1]], [[sid2]], ...] when in outer array
	sources := make([]any, len(sourceIDs))
	for i, sid := range sourceIDs {
		sources[i] = []any{[]any{sid}}
	}

	params := []any{
		sources,
		question,
		history,
		[]any{2, nil, []any{1}},
		conversationID,
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return "", err
	}

	fReq := []any{nil, string(paramsJSON)}
	fReqJSON, err := json.Marshal(fReq)
	if err != nil {
		return "", err
	}

	body := "f.req=" + url.QueryEscape(string(fReqJSON))
	if csrfToken != "" {
		body += "&at=" + url.QueryEscape(csrfToken)
	}
	body += "&"

	return body, nil
}
