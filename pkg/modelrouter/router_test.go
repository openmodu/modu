package modelrouter

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	cfg := Config{Providers: []Provider{{ID: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY", Models: []string{"deepseek-chat"}}}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode: %v, %v", st, err)
	}
	got, err := Load(path)
	if err != nil || len(got.Providers) != 1 || got.Providers[0].ID != "deepseek" {
		t.Fatalf("Load: %#v, %v", got, err)
	}
	if err := Save(path, Config{Providers: []Provider{{ID: "bad/path", BaseURL: "https://example.com/v1"}}}); err == nil {
		t.Fatal("expected invalid provider ID")
	}
	if err := Save(path, Config{Providers: []Provider{{ID: "p", BaseURL: "https://example.com/v1", Protocol: "other", Models: []string{"m"}}}}); err == nil {
		t.Fatal("expected invalid protocol")
	}
}

func TestGatewayRoutesChatAndHidesProviderKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("upstream request: %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"deepseek-chat"`) {
			t.Errorf("model not rewritten: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chat1","object":"chat.completion","model":"deepseek-chat","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	router, err := New(Config{Providers: []Provider{{ID: "deepseek", BaseURL: upstream.URL + "/v1", APIKey: "secret", Models: []string{"deepseek-chat"}}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || got["model"] != "deepseek/deepseek-chat" {
		t.Fatalf("status %d, response %#v", resp.StatusCode, got)
	}
}

func TestGatewayRejectsUnlistedModelBeforeNetwork(t *testing.T) {
	router, err := New(Config{Providers: []Provider{{ID: "x", BaseURL: "https://example.com/v1", Models: []string{"known"}}}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	router.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x/unknown"}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestGatewayRejectsMissingKeyEnvBeforeNetwork(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer upstream.Close()
	t.Setenv("MODELROUTER_TEST_KEY", "")
	router, _ := New(Config{Providers: []Provider{{ID: "x", BaseURL: upstream.URL + "/v1", APIKeyEnv: "MODELROUTER_TEST_KEY", Models: []string{"m"}}}})
	w := httptest.NewRecorder()
	router.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x/m"}`)))
	if w.Code != http.StatusBadGateway || called {
		t.Fatalf("status %d, called %v", w.Code, called)
	}
}

func TestResponsesToChatWithToolCall(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		messages, _ := body["messages"].([]any)
		if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[1].(map[string]any)["content"] != "Find weather" {
			t.Errorf("messages %#v", messages)
		}
		_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	}))
	defer upstream.Close()
	router, _ := New(Config{Providers: []Provider{{ID: "p", BaseURL: upstream.URL + "/v1", Models: []string{"m"}}}})
	w := httptest.NewRecorder()
	router.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"p/m","instructions":"Be concise","input":[{"role":"user","content":[{"type":"input_text","text":"Find weather"}]}],"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}]}`)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Model  string `json:"model"`
		Output []struct {
			Type   string `json:"type"`
			Name   string `json:"name"`
			CallID string `json:"call_id"`
		} `json:"output"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Model != "p/m" || len(got.Output) != 1 || got.Output[0].Type != "function_call" || got.Output[0].Name != "weather" || got.Output[0].CallID != "call_1" {
		t.Fatalf("response %#v, %v: %s", got, err, w.Body.String())
	}
}

func TestMessagesToChatWithToolUse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		messages, _ := body["messages"].([]any)
		if len(messages) != 1 || messages[0].(map[string]any)["content"] != "Call weather" {
			t.Errorf("messages %#v", messages)
		}
		_, _ = io.WriteString(w, `{"id":"c2","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_2","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Tokyo\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`)
	}))
	defer upstream.Close()
	router, _ := New(Config{Providers: []Provider{{ID: "p", BaseURL: upstream.URL + "/v1", Models: []string{"m"}}}})
	w := httptest.NewRecorder()
	router.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"p/m","max_tokens":100,"messages":[{"role":"user","content":"Call weather"}],"tools":[{"name":"weather","input_schema":{"type":"object"}}]}`)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Type != "message" || got.StopReason != "tool_use" || len(got.Content) != 1 || got.Content[0].Name != "weather" || got.Content[0].ID != "call_2" {
		t.Fatalf("response %#v, %v: %s", got, err, w.Body.String())
	}
}

func TestTranslatedStreamsIncludeContentEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"c3","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	router, _ := New(Config{Providers: []Provider{{ID: "p", BaseURL: upstream.URL + "/v1", Models: []string{"m"}}}})
	for _, tc := range []struct {
		path, body, want string
	}{
		{"/v1/responses", `{"model":"p/m","input":"hi","stream":true}`, "event: response.output_text.delta"},
		{"/v1/messages", `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":true}`, "event: content_block_delta"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMessagesImageInputReachesChatUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 || body.Messages[0].Content[1].ImageURL.URL != "data:image/png;base64,cG5n" {
			t.Errorf("image missing from upstream Chat request: %#v", body)
		}
		_, _ = io.WriteString(w, `{"id":"c4","choices":[{"message":{"role":"assistant","content":"seen"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	router, _ := New(Config{Providers: []Provider{{ID: "p", BaseURL: upstream.URL + "/v1", Models: []string{"m"}}}})
	w := httptest.NewRecorder()
	router.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"p/m","messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cG5n"}}]}]}`)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestNativeResponsesPreservesRequestAndResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer upstream-key" || r.Header.Get("OpenAI-Beta") != "responses=v1" {
			t.Errorf("upstream route or headers: %s %s %#v", r.Method, r.URL.Path, r.Header)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["model"]) != `"native-model"` || string(body["reasoning"]) != `{"effort":"high"}` || string(body["tools"]) != `[{"type":"web_search"},{"type":"custom","name":"exec","format":{"type":"text"}}]` || string(body["previous_response_id"]) != `"resp_previous"` {
			t.Errorf("request fields changed: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req_upstream")
		_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","model":"native-model","output":[{"type":"reasoning","summary":[]},{"type":"custom_tool_call","name":"exec","input":"ls"}],"metadata":{"source":"upstream"}}`)
	}))
	defer upstream.Close()
	router, err := New(Config{Providers: []Provider{{ID: "native", BaseURL: upstream.URL + "/v1", APIKey: "upstream-key", Protocol: "responses", Models: []string{"native-model"}}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router.Handler())
	defer srv.Close()
	request, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", strings.NewReader(`{"model":"native/native-model","input":"hi","reasoning":{"effort":"high"},"tools":[{"type":"web_search"},{"type":"custom","name":"exec","format":{"type":"text"}}],"previous_response_id":"resp_previous"}`))
	request.Header.Set("OpenAI-Beta", "responses=v1")
	request.Header.Set("Authorization", "Bearer client-key")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var got map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || string(got["model"]) != `"native/native-model"` || !strings.Contains(string(got["id"]), "resp_modu_native~") || !strings.Contains(string(got["output"]), "custom_tool_call") || response.Header.Get("X-Request-Id") != "req_upstream" {
		t.Fatalf("native response: status=%d headers=%#v body=%#v", response.StatusCode, response.Header, got)
	}
}

func TestNativeResponsesStateRoutesAndPreviousID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			var body map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			if previous := body["previous_response_id"]; len(previous) > 0 && string(previous) != `"resp_upstream"` {
				t.Errorf("previous ID was not restored: %s", previous)
			}
			_, _ = io.WriteString(w, `{"id":"resp_upstream","object":"response","model":"m","output":[]}`)
		case "/v1/responses/resp_upstream":
			if r.Method == http.MethodDelete {
				_, _ = io.WriteString(w, `{"id":"resp_upstream","object":"response.deleted","deleted":true}`)
			} else {
				_, _ = io.WriteString(w, `{"id":"resp_upstream","object":"response","model":"m","output":[]}`)
			}
		case "/v1/responses/resp_upstream/cancel":
			_, _ = io.WriteString(w, `{"id":"resp_upstream","object":"response","model":"m","status":"cancelled"}`)
		case "/v1/responses/resp_upstream/input_items":
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"msg_1","type":"message"}]}`)
		case "/v1/responses/input_tokens":
			var body map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			if string(body["model"]) != `"m"` {
				t.Errorf("count model %s", body["model"])
			}
			_, _ = io.WriteString(w, `{"object":"response.input_tokens","input_tokens":17}`)
		case "/v1/responses/compact":
			var body map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			if string(body["model"]) != `"m"` {
				t.Errorf("compact model %s", body["model"])
			}
			_, _ = io.WriteString(w, `{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":"opaque"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	router, _ := New(Config{Providers: []Provider{{ID: "p", BaseURL: upstream.URL + "/v1", Protocol: "responses", Models: []string{"m"}}}})
	srv := httptest.NewServer(router.Handler())
	defer srv.Close()
	create, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"p/m","input":"first"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(create.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	create.Body.Close()
	if !strings.HasPrefix(result.ID, "resp_modu_p~") {
		t.Fatalf("namespaced ID %q", result.ID)
	}
	for _, tc := range []struct{ method, path, body, want string }{
		{http.MethodPost, "/v1/responses", `{"model":"p/m","input":"next","previous_response_id":"` + result.ID + `"}`, `"object":"response"`},
		{http.MethodGet, "/v1/responses/" + result.ID, "", `"model":"p/m"`},
		{http.MethodPost, "/v1/responses/" + result.ID + "/cancel", "", `"status":"cancelled"`},
		{http.MethodGet, "/v1/responses/" + result.ID + "/input_items?limit=1", "", `"msg_1"`},
		{http.MethodDelete, "/v1/responses/" + result.ID, "", `"deleted":true`},
		{http.MethodPost, "/v1/responses/input_tokens", `{"model":"p/m","input":"count"}`, `"input_tokens":17`},
		{http.MethodPost, "/v1/responses/compact", `{"model":"p/m","input":"long context"}`, `"encrypted_content":"opaque"`},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), tc.want) {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, resp.StatusCode, body)
		}
	}
}

func TestNativeResponsesStreamsBeforeUpstreamCompletes(t *testing.T) {
	finish := make(chan struct{})
	closed := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if string(body["stream"]) != "true" || string(body["model"]) != `"m"` {
			t.Errorf("stream request: %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"model\":\"m\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\n")
		w.(http.Flusher).Flush()
		<-finish
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n")
	}))
	defer func() {
		if !closed {
			close(finish)
		}
		upstream.Close()
	}()
	router, _ := New(Config{Providers: []Provider{{ID: "p", BaseURL: upstream.URL + "/v1", Protocol: "responses", Models: []string{"m"}}}})
	srv := httptest.NewServer(router.Handler())
	defer srv.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"p/m","input":"hi","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	var first strings.Builder
	for {
		line, readErr := reader.ReadString('\n')
		first.WriteString(line)
		if readErr != nil || line == "\n" {
			err = readErr
			break
		}
	}
	if err != nil || !strings.Contains(first.String(), "resp_modu_p~") {
		t.Fatalf("first event before completion: %q, %v", first.String(), err)
	}
	close(finish)
	closed = true
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "response.reasoning_summary_text.delta") {
		t.Fatalf("reasoning event lost: %q, %v", rest, err)
	}
}
