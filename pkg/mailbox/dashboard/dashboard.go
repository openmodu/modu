// Package dashboard 提供 Agent Teams 的 HTTP 可视化看板。
// 内嵌一个轻量 HTTP server，提供 REST API、SSE 实时推送和一个无外部依赖的 HTML 界面。
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/crosszan/modu/pkg/mailbox"
)

// Dashboard 订阅 Hub 事件，通过 HTTP 对外暴露 agents/tasks 状态和 SSE 流
type Dashboard struct {
	hub     *mailbox.Hub
	mu      sync.Mutex
	clients map[chan string]struct{}
}

// NewDashboard 创建一个绑定到给定 Hub 的 Dashboard 实例
func NewDashboard(hub *mailbox.Hub) *Dashboard {
	return &Dashboard{
		hub:     hub,
		clients: make(map[chan string]struct{}),
	}
}

// Start 启动 HTTP 服务，阻塞直到 ctx 取消
func (d *Dashboard) Start(ctx context.Context, addr string) error {
	sub := d.hub.Subscribe()

	// 将 Hub 事件转发给所有 SSE 客户端
	go func() {
		for {
			select {
			case <-ctx.Done():
				d.hub.Unsubscribe(sub)
				return
			case e, ok := <-sub:
				if !ok {
					return
				}
				b, err := json.Marshal(e)
				if err != nil {
					continue
				}
				d.broadcast(string(e.Type), string(b))
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handleIndex)
	mux.HandleFunc("/api/agents", d.handleAgents)
	mux.HandleFunc("/api/tasks", d.handleTasks)
	mux.HandleFunc("/api/tasks/", d.handleTaskByID)
	mux.HandleFunc("/events", d.handleSSE)

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	log.Printf("[Dashboard] Listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (d *Dashboard) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	agents := d.hub.ListAgentInfos()
	writeJSON(w, agents)
}

func (d *Dashboard) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tasks := d.hub.ListTasks()
	writeJSON(w, tasks)
}

func (d *Dashboard) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	if id == "" {
		http.Error(w, "task id required", http.StatusBadRequest)
		return
	}
	task, err := d.hub.GetTask(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, task)
}

func (d *Dashboard) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan string, 64)
	d.addClient(ch)
	defer d.removeClient(ch)

	// 连接建立时推送当前快照
	d.sendSnapshot(w, flusher)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// msg 格式: "event\ndata"
			parts := strings.SplitN(msg, "\n", 2)
			if len(parts) == 2 {
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", parts[0], parts[1])
			} else {
				fmt.Fprintf(w, "data: %s\n\n", msg)
			}
			flusher.Flush()
		}
	}
}

func (d *Dashboard) sendSnapshot(w http.ResponseWriter, flusher http.Flusher) {
	agents := d.hub.ListAgentInfos()
	if b, err := json.Marshal(agents); err == nil {
		fmt.Fprintf(w, "event: snapshot.agents\ndata: %s\n\n", b)
	}
	tasks := d.hub.ListTasks()
	if b, err := json.Marshal(tasks); err == nil {
		fmt.Fprintf(w, "event: snapshot.tasks\ndata: %s\n\n", b)
	}
	flusher.Flush()
}

func (d *Dashboard) broadcast(eventType, data string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	msg := eventType + "\n" + data
	for ch := range d.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (d *Dashboard) addClient(ch chan string) {
	d.mu.Lock()
	d.clients[ch] = struct{}{}
	d.mu.Unlock()
}

func (d *Dashboard) removeClient(ch chan string) {
	d.mu.Lock()
	delete(d.clients, ch)
	d.mu.Unlock()
	close(ch)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

const indexHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Agent Teams Dashboard</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #0f0f13; color: #e0e0e0; padding: 24px; }
  h1 { font-size: 1.4rem; font-weight: 600; margin-bottom: 20px; color: #fff; letter-spacing: 0.5px; }
  h2 { font-size: 1rem; font-weight: 500; margin-bottom: 12px; color: #aaa; text-transform: uppercase; letter-spacing: 1px; }
  .section { margin-bottom: 32px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 12px; }
  .card { background: #1a1a24; border: 1px solid #2a2a38; border-radius: 8px; padding: 14px; }
  .card .name { font-weight: 600; font-size: 0.9rem; margin-bottom: 6px; word-break: break-all; }
  .card .meta { font-size: 0.78rem; color: #888; line-height: 1.6; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 0.72rem; font-weight: 600; margin-left: 6px; }
  .idle { background: #1e3a2a; color: #4ade80; }
  .busy { background: #3a2a10; color: #fb923c; }
  table { width: 100%; border-collapse: collapse; font-size: 0.82rem; }
  thead th { text-align: left; padding: 8px 10px; color: #777; font-weight: 500; border-bottom: 1px solid #2a2a38; }
  tbody tr { border-bottom: 1px solid #1e1e2a; }
  tbody tr:hover { background: #1e1e2c; }
  td { padding: 8px 10px; vertical-align: top; max-width: 300px; word-break: break-all; }
  .status-pending   { color: #94a3b8; }
  .status-running   { color: #60a5fa; }
  .status-completed { color: #4ade80; }
  .status-failed    { color: #f87171; }
  .dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-right: 5px; }
  .dot-pending   { background: #94a3b8; }
  .dot-running   { background: #60a5fa; animation: pulse 1.2s infinite; }
  .dot-completed { background: #4ade80; }
  .dot-failed    { background: #f87171; }
  @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.4} }
  .conn { font-size: 0.75rem; color: #555; margin-bottom: 18px; }
  .conn.ok { color: #4ade80; }
  .result-cell { max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #aaa; }
  .empty { color: #444; font-style: italic; padding: 12px 0; }
</style>
</head>
<body>
<h1>Agent Teams Dashboard</h1>
<div class="conn" id="conn-status">⬤ connecting...</div>

<div class="section">
  <h2>Agents</h2>
  <div class="grid" id="agents-grid"><div class="empty">No agents yet</div></div>
</div>

<div class="section">
  <h2>Tasks</h2>
  <table>
    <thead><tr><th>ID</th><th>Description</th><th>Status</th><th>Assigned To</th><th>Result / Error</th></tr></thead>
    <tbody id="tasks-body"><tr><td colspan="5" class="empty">No tasks yet</td></tr></tbody>
  </table>
</div>

<script>
const agents = {};
const tasks = {};

function renderAgents() {
  const grid = document.getElementById('agents-grid');
  const keys = Object.keys(agents);
  if (!keys.length) { grid.innerHTML = '<div class="empty">No agents yet</div>'; return; }
  grid.innerHTML = keys.map(id => {
    const a = agents[id];
    const statusClass = a.status === 'busy' ? 'busy' : 'idle';
    return ` + "`" + `<div class="card">
      <div class="name">${a.id}<span class="badge ${statusClass}">${a.status}</span></div>
      <div class="meta">
        ${a.role ? '<div>Role: ' + a.role + '</div>' : ''}
        ${a.current_task ? '<div>Task: ' + a.current_task + '</div>' : ''}
        <div>Seen: ${new Date(a.last_seen).toLocaleTimeString()}</div>
      </div>
    </div>` + "`" + `;
  }).join('');
}

function renderTasks() {
  const tbody = document.getElementById('tasks-body');
  const keys = Object.keys(tasks).sort((a,b) => tasks[a].created_at > tasks[b].created_at ? 1 : -1);
  if (!keys.length) { tbody.innerHTML = '<tr><td colspan="5" class="empty">No tasks yet</td></tr>'; return; }
  tbody.innerHTML = keys.map(id => {
    const t = tasks[id];
    const sc = 'status-' + t.status;
    const result = t.result || t.error || '';
    return ` + "`" + `<tr>
      <td>${t.id}</td>
      <td>${t.description}</td>
      <td class="${sc}"><span class="dot dot-${t.status}"></span>${t.status}</td>
      <td>${t.assigned_to || '-'}</td>
      <td class="result-cell" title="${result}">${result || '-'}</td>
    </tr>` + "`" + `;
  }).join('');
}

function applyEvent(type, data) {
  if (type === 'snapshot.agents') {
    data.forEach(a => { agents[a.id] = a; });
    renderAgents();
  } else if (type === 'snapshot.tasks') {
    data.forEach(t => { tasks[t.id] = t; });
    renderTasks();
  } else if (type === 'agent.registered' || type === 'agent.updated') {
    if (data.data) { agents[data.agent_id] = data.data; renderAgents(); }
  } else if (type === 'agent.evicted') {
    delete agents[data.agent_id]; renderAgents();
  } else if (type === 'task.created' || type === 'task.updated') {
    if (data.data) { tasks[data.task_id] = data.data; renderTasks(); }
  }
}

function connect() {
  const es = new EventSource('/events');
  const connEl = document.getElementById('conn-status');

  es.onopen = () => { connEl.textContent = '⬤ connected'; connEl.className = 'conn ok'; };
  es.onerror = () => { connEl.textContent = '⬤ disconnected - reconnecting...'; connEl.className = 'conn'; };

  ['snapshot.agents','snapshot.tasks','agent.registered','agent.updated','agent.evicted','task.created','task.updated'].forEach(evt => {
    es.addEventListener(evt, e => {
      try { applyEvent(evt, JSON.parse(e.data)); } catch(err) { console.error(err); }
    });
  });
}

connect();
</script>
</body>
</html>`
