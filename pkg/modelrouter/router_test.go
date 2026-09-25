package modelrouter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
