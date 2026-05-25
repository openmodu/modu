package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runner"
)

func TestCompletionPostsWebhookPayload(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("CST", 8*60*60)
	t.Cleanup(func() { time.Local = oldLocal })

	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type=%q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	logPath := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(logPath, []byte(`{"type":"assistant","text":"all done"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	ended := started.Add(1500 * time.Millisecond)
	cfg := &config.Config{Channels: map[string]config.Channel{
		"ops": {Type: "webhook", URL: srv.URL},
	}}
	task := config.Task{ID: "daily", Channel: "ops"}

	err := NewSender().Completion(context.Background(), cfg, task, runner.Result{
		LogPath: logPath,
		Started: started,
		Ended:   ended,
	}, nil)
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if got.Event != "modu_cron.task_completed" || got.TaskID != "daily" || got.Status != "ok" {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if got.Summary != "all done" || !strings.Contains(got.Text, "summary: all done") {
		t.Fatalf("summary missing from payload: %+v", got)
	}
	if got.DurationMS != 1500 {
		t.Fatalf("duration=%d, want 1500", got.DurationMS)
	}
	if got.StartedAt != "2026-05-24T18:00:00+08:00" || got.EndedAt != "2026-05-24T18:00:01.5+08:00" {
		t.Fatalf("local timestamps not used: started=%q ended=%q", got.StartedAt, got.EndedAt)
	}
}

func TestCompletionReportsMissingChannel(t *testing.T) {
	err := NewSender().Completion(context.Background(), &config.Config{}, config.Task{
		ID:      "x",
		Channel: "missing",
	}, runner.Result{}, nil)
	if err == nil || !strings.Contains(err.Error(), `channel "missing" not found`) {
		t.Fatalf("expected missing channel error, got %v", err)
	}
}

func TestCompletionUsesEnvURL(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("MODU_CRON_TEST_WEBHOOK", srv.URL)

	cfg := &config.Config{Channels: map[string]config.Channel{
		"ops": {Type: "webhook", URLEnv: "MODU_CRON_TEST_WEBHOOK"},
	}}
	err := NewSender().Completion(context.Background(), cfg, config.Task{
		ID:      "x",
		Channel: "ops",
	}, runner.Result{}, nil)
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if !called {
		t.Fatal("server was not called")
	}
}
