package modelrouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type Router struct {
	providers map[string]Provider
	client    *http.Client
}

func New(cfg Config) (*Router, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	r := &Router{providers: make(map[string]Provider, len(cfg.Providers)), client: &http.Client{Timeout: 10 * time.Minute}}
	for _, p := range cfg.Providers {
		r.providers[p.ID] = p
	}
	return r, nil
}

func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", r.models)
	mux.HandleFunc("POST /v1/chat/completions", r.chat)
	mux.HandleFunc("POST /v1/responses", r.responses)
	mux.HandleFunc("POST /v1/messages", r.messages)
	return mux
}

func (r *Router) models(w http.ResponseWriter, _ *http.Request) {
	data := make([]map[string]any, 0)
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := r.providers[id]
		for _, m := range p.Models {
			data = append(data, map[string]any{"id": p.ID + "/" + m, "object": "model", "owned_by": p.ID})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (r *Router) resolve(name string) (Provider, string, bool) {
	id, model, ok := strings.Cut(name, "/")
	if !ok {
		return Provider{}, "", false
	}
	p, ok := r.providers[id]
	if !ok {
		return Provider{}, "", false
	}
	for _, m := range p.Models {
		if m == model {
			return p, model, true
		}
	}
	return Provider{}, "", false
}

func (r *Router) chat(w http.ResponseWriter, req *http.Request) {
	r.forward(w, req, "chat/completions")
}

func (r *Router) responses(w http.ResponseWriter, req *http.Request) {
	r.forward(w, req, "responses")
}

func (r *Router) messages(w http.ResponseWriter, req *http.Request) {
	r.forward(w, req, "messages")
}

func (r *Router) forward(w http.ResponseWriter, req *http.Request, endpoint string) {
	body, err := io.ReadAll(io.LimitReader(req.Body, 16<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var id string
	if err := json.Unmarshal(payload["model"], &id); err != nil {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	p, model, ok := r.resolve(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown model: "+id)
		return
	}
	payload["model"], _ = json.Marshal(model)
	requestedStream := false
	_ = json.Unmarshal(payload["stream"], &requestedStream)
	upstreamEndpoint := endpoint
	if endpoint != "chat/completions" {
		payload, err = toChat(payload, endpoint)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		upstreamEndpoint = "chat/completions"
		payload["stream"] = json.RawMessage("false")
	}
	body, err = json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	base, _ := url.Parse(strings.TrimRight(p.BaseURL, "/"))
	base.Path = strings.TrimRight(base.Path, "/") + "/" + upstreamEndpoint
	up, err := http.NewRequestWithContext(req.Context(), http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	up.Header.Set("Content-Type", "application/json")
	up.Header.Set("Accept", req.Header.Get("Accept"))
	if upstreamEndpoint == "messages" {
		up.Header.Set("anthropic-version", "2023-06-01")
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
		if upstreamEndpoint == "messages" {
			up.Header.Set("x-api-key", key)
		} else {
			up.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := r.client.Do(up)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("provider %s: %v", p.ID, err))
		return
	}
	defer resp.Body.Close()
	for _, header := range []string{"Content-Type", "Cache-Control"} {
		if v := resp.Header.Get(header); v != "" {
			w.Header().Set(header, v)
		}
	}
	if endpoint != "chat/completions" {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode >= 400 {
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(b)
			return
		}
		out, err := fromChat(b, endpoint, id)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if requestedStream {
			writeStream(w, endpoint, out)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
		return
	}
	w.WriteHeader(resp.StatusCode)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 16<<10)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr != nil {
				break
			}
		}
		return
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 400 {
		var out map[string]json.RawMessage
		if json.Unmarshal(b, &out) == nil && out != nil {
			out["model"], _ = json.Marshal(id)
			b, _ = json.Marshal(out)
		}
	}
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message}})
}
