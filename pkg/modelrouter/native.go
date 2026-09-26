package modelrouter

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const responseIDPrefix = "resp_modu_"

func namespacedResponseID(providerID, upstreamID string) string {
	return responseIDPrefix + providerID + "~" + base64.RawURLEncoding.EncodeToString([]byte(upstreamID))
}

func parseResponseID(id string) (string, string, error) {
	if !strings.HasPrefix(id, responseIDPrefix) {
		return "", "", fmt.Errorf("response ID is not routed by modu")
	}
	name, encoded, ok := strings.Cut(strings.TrimPrefix(id, responseIDPrefix), "~")
	if !ok || !providerID.MatchString(name) {
		return "", "", fmt.Errorf("invalid response ID")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return "", "", fmt.Errorf("invalid response ID")
	}
	return name, string(decoded), nil
}

func (r *Router) nativeCreate(w http.ResponseWriter, req *http.Request, p Provider, payload map[string]json.RawMessage, localModel string) {
	if err := restorePreviousResponseID(payload, p.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, _ := json.Marshal(payload)
	r.proxyNative(w, req, p, "/responses", body, localModel, true)
}

func (r *Router) nativeModelOperation(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(io.LimitReader(req.Body, 16<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var localModel string
	if err := json.Unmarshal(payload["model"], &localModel); err != nil {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	p, model, ok := r.resolve(localModel)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown model: "+localModel)
		return
	}
	if p.Protocol != "responses" {
		writeError(w, http.StatusBadRequest, "provider "+p.ID+" does not support native Responses operations")
		return
	}
	payload["model"], _ = json.Marshal(model)
	if err := restorePreviousResponseID(payload, p.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, _ = json.Marshal(payload)
	suffix := strings.TrimPrefix(req.URL.Path, "/v1")
	r.proxyNative(w, req, p, suffix, body, localModel, strings.HasSuffix(suffix, "/compact"))
}

func (r *Router) nativeByID(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("responseID")
	providerName, upstreamID, err := parseResponseID(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, ok := r.providers[providerName]
	if !ok || p.Protocol != "responses" {
		writeError(w, http.StatusNotFound, "unknown Responses provider")
		return
	}
	suffix := "/responses/" + url.PathEscape(upstreamID)
	if strings.HasSuffix(req.URL.Path, "/cancel") {
		suffix += "/cancel"
	} else if strings.HasSuffix(req.URL.Path, "/input_items") {
		suffix += "/input_items"
	}
	var body []byte
	if req.Method == http.MethodPost {
		body, err = io.ReadAll(io.LimitReader(req.Body, 16<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	r.proxyNative(w, req, p, suffix, body, "", !strings.HasSuffix(suffix, "/input_items"))
}

func restorePreviousResponseID(payload map[string]json.RawMessage, providerName string) error {
	var id string
	if len(payload["previous_response_id"]) == 0 || string(payload["previous_response_id"]) == "null" {
		return nil
	}
	if err := json.Unmarshal(payload["previous_response_id"], &id); err != nil {
		return fmt.Errorf("invalid previous_response_id")
	}
	if !strings.HasPrefix(id, responseIDPrefix) {
		return nil
	}
	provider, upstreamID, err := parseResponseID(id)
	if err != nil || provider != providerName {
		return fmt.Errorf("previous_response_id belongs to another provider or is invalid")
	}
	payload["previous_response_id"], _ = json.Marshal(upstreamID)
	return nil
}

func (r *Router) proxyNative(w http.ResponseWriter, req *http.Request, p Provider, suffix string, body []byte, localModel string, rewriteIDs bool) {
	base, _ := url.Parse(strings.TrimRight(p.BaseURL, "/"))
	base.RawPath = strings.TrimRight(base.EscapedPath(), "/") + suffix
	base.Path, _ = url.PathUnescape(base.RawPath)
	base.RawQuery = req.URL.RawQuery
	up, err := http.NewRequestWithContext(req.Context(), req.Method, base.String(), bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if req.Method == http.MethodPost {
		up.Header.Set("Content-Type", "application/json")
	}
	for _, header := range []string{"Accept", "OpenAI-Beta", "OpenAI-Organization", "OpenAI-Project", "Idempotency-Key"} {
		if value := req.Header.Get(header); value != "" {
			up.Header.Set(header, value)
		}
	}
	key := p.APIKey
	if p.APIKeyEnv != "" {
		key = os.Getenv(p.APIKeyEnv)
		if key == "" {
			writeError(w, http.StatusBadGateway, "provider "+p.ID+": environment variable "+p.APIKeyEnv+" is empty")
			return
		}
	}
	if key != "" {
		up.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.client.Do(up)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("provider %s: %v", p.ID, err))
		return
	}
	defer resp.Body.Close()
	for _, header := range []string{"Content-Type", "Cache-Control", "X-Request-Id", "OpenAI-Processing-Ms", "OpenAI-Version", "Retry-After"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	if resp.StatusCode >= 400 || !rewriteIDs {
		w.WriteHeader(resp.StatusCode)
		copyNative(w, resp.Body, strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"))
		return
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		w.WriteHeader(resp.StatusCode)
		streamNative(w, resp.Body, p.ID, localModel)
		return
	}
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	result = rewriteNativeJSON(result, p.ID, localModel)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(result)
}

func rewriteNativeJSON(body []byte, providerName, localModel string) []byte {
	var event map[string]json.RawMessage
	if json.Unmarshal(body, &event) != nil || event == nil {
		return body
	}
	if nested := event["response"]; len(nested) > 0 {
		var response map[string]json.RawMessage
		if json.Unmarshal(nested, &response) == nil {
			rewriteResponseObject(response, providerName, localModel)
			event["response"], _ = json.Marshal(response)
		}
	} else {
		var object string
		_ = json.Unmarshal(event["object"], &object)
		if object == "response" || object == "response.deleted" {
			rewriteResponseObject(event, providerName, localModel)
		}
	}
	result, err := json.Marshal(event)
	if err != nil {
		return body
	}
	return result
}

func rewriteResponseObject(response map[string]json.RawMessage, providerName, localModel string) {
	for _, field := range []string{"id", "previous_response_id"} {
		var id string
		if json.Unmarshal(response[field], &id) == nil && id != "" {
			response[field], _ = json.Marshal(namespacedResponseID(providerName, id))
		}
	}
	if len(response["model"]) > 0 {
		if localModel == "" {
			var upstreamModel string
			if json.Unmarshal(response["model"], &upstreamModel) == nil && upstreamModel != "" {
				localModel = providerName + "/" + upstreamModel
			}
		}
		if localModel != "" {
			response["model"], _ = json.Marshal(localModel)
		}
	}
}

func streamNative(w http.ResponseWriter, body io.Reader, providerName, localModel string) {
	reader := bufio.NewReader(body)
	flusher, _ := w.(http.Flusher)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if bytes.HasPrefix(line, []byte("data:")) {
				data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
				if len(data) > 0 && data[0] == '{' {
					line = append(append([]byte("data: "), rewriteNativeJSON(data, providerName, localModel)...), '\n')
				}
			}
			if _, writeErr := w.Write(line); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func copyNative(w http.ResponseWriter, body io.Reader, streaming bool) {
	buf := make([]byte, 16<<10)
	flusher, _ := w.(http.Flusher)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if streaming && flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
