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
	mux.HandleFunc("/api/conversations/", d.handleConversation)
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

func (d *Dashboard) handleConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	taskID := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
	if taskID == "" {
		http.Error(w, "task id required", http.StatusBadRequest)
		return
	}
	entries := d.hub.GetConversation(taskID)
	writeJSON(w, entries)
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
<title>Agent Teams</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #0c0c10; color: #d4d4d8; height: 100vh; display: flex; flex-direction: column; overflow: hidden; }

/* ── Top bar ── */
.topbar { display: flex; align-items: center; gap: 16px; padding: 0 20px; height: 48px; background: #111118; border-bottom: 1px solid #222230; flex-shrink: 0; }
.topbar h1 { font-size: 0.95rem; font-weight: 600; color: #fff; letter-spacing: 0.3px; }
.conn { font-size: 0.72rem; color: #555; margin-left: auto; }
.conn.ok { color: #4ade80; }

/* ── Agents strip ── */
.agents-strip { display: flex; gap: 8px; padding: 8px 20px; background: #0f0f16; border-bottom: 1px solid #1e1e28; flex-shrink: 0; overflow-x: auto; }
.agent-chip { display: flex; align-items: center; gap: 6px; background: #1a1a26; border: 1px solid #2a2a3a; border-radius: 20px; padding: 4px 10px; font-size: 0.76rem; white-space: nowrap; }
.agent-chip .aname { font-weight: 600; color: #c4c4d4; }
.agent-chip .arole { color: #666; }
.dot { display: inline-block; width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
.dot-idle { background: #4ade80; }
.dot-busy { background: #fb923c; animation: pulse 1.2s infinite; }
@keyframes pulse { 0%,100%{opacity:1} 50%{opacity:.4} }

/* ── Main split ── */
.main { display: flex; flex: 1; overflow: hidden; }

/* ── Task list (left) ── */
.task-list { width: 280px; flex-shrink: 0; border-right: 1px solid #1e1e28; display: flex; flex-direction: column; overflow: hidden; }
.task-list-header { padding: 12px 16px 8px; font-size: 0.7rem; font-weight: 600; color: #555; text-transform: uppercase; letter-spacing: 1px; flex-shrink: 0; }
.task-items { flex: 1; overflow-y: auto; }
.task-item { padding: 10px 16px; cursor: pointer; border-bottom: 1px solid #16161e; transition: background .1s; }
.task-item:hover { background: #161620; }
.task-item.active { background: #1a1e30; border-left: 2px solid #4f7ef0; }
.task-item .tid { font-size: 0.7rem; color: #555; margin-bottom: 2px; }
.task-item .tdesc { font-size: 0.82rem; color: #c4c4d4; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-bottom: 4px; }
.task-item .tmeta { display: flex; align-items: center; gap: 6px; font-size: 0.72rem; color: #666; }
.badge { display: inline-block; padding: 1px 6px; border-radius: 8px; font-size: 0.68rem; font-weight: 600; }
.badge-pending   { background: #1e2030; color: #94a3b8; }
.badge-running   { background: #1a2a40; color: #60a5fa; }
.badge-completed { background: #1a3028; color: #4ade80; }
.badge-failed    { background: #301a1a; color: #f87171; }
.empty-list { padding: 20px 16px; font-size: 0.8rem; color: #444; font-style: italic; }

/* ── Detail panel (right) ── */
.detail { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
.detail-empty { flex: 1; display: flex; align-items: center; justify-content: center; color: #333; font-size: 0.9rem; }

/* Task header */
.task-header { padding: 16px 20px 12px; border-bottom: 1px solid #1e1e28; flex-shrink: 0; }
.task-header .th-id { font-size: 0.72rem; color: #555; margin-bottom: 4px; }
.task-header .th-desc { font-size: 1rem; font-weight: 600; color: #e8e8f0; margin-bottom: 8px; }
.task-header .th-meta { display: flex; gap: 16px; font-size: 0.78rem; color: #666; }
.task-header .th-meta span { display: flex; align-items: center; gap: 4px; }
.result-box { margin-top: 8px; padding: 6px 10px; background: #1a2030; border-radius: 6px; font-size: 0.78rem; color: #a0c0e0; border-left: 2px solid #4f7ef0; max-height: 80px; overflow-y: auto; word-break: break-word; }
.result-box.err { background: #201818; color: #f08080; border-color: #f87171; }

/* Session log */
.session-label { padding: 10px 20px 6px; font-size: 0.7rem; font-weight: 600; color: #444; text-transform: uppercase; letter-spacing: 1px; flex-shrink: 0; }
.session-log { flex: 1; overflow-y: auto; padding: 0 20px 20px; }
.session-empty { color: #333; font-size: 0.82rem; font-style: italic; padding-top: 12px; }

/* Message entry */
.msg-entry { display: flex; gap: 10px; margin-bottom: 10px; align-items: flex-start; }
.msg-avatar { width: 28px; height: 28px; border-radius: 50%; background: #1e2030; display: flex; align-items: center; justify-content: center; font-size: 0.65rem; font-weight: 700; color: #7090d0; flex-shrink: 0; margin-top: 1px; }
.msg-body { flex: 1; }
.msg-header { display: flex; align-items: baseline; gap: 6px; margin-bottom: 3px; }
.msg-from { font-size: 0.8rem; font-weight: 600; }
.msg-arrow { font-size: 0.72rem; color: #444; }
.msg-to   { font-size: 0.78rem; color: #666; }
.msg-type-tag { font-size: 0.65rem; padding: 1px 5px; border-radius: 4px; background: #1e2030; color: #666; }
.msg-time { font-size: 0.68rem; color: #444; margin-left: auto; }
.msg-content { font-size: 0.82rem; color: #b0b0c0; line-height: 1.55; background: #141420; border-radius: 6px; padding: 6px 10px; word-break: break-word; }

/* Color palette for agent avatars */
.c0 { color: #7c9df0; } .c1 { color: #f09070; } .c2 { color: #70d0a0; }
.c3 { color: #d0a070; } .c4 { color: #a070d0; } .c5 { color: #70c0d0; }
</style>
</head>
<body>

<div class="topbar">
  <h1>Agent Teams</h1>
  <div class="conn" id="conn-status">⬤ connecting...</div>
</div>

<div class="agents-strip" id="agents-strip">
  <span style="font-size:0.75rem;color:#444;align-self:center">No agents</span>
</div>

<div class="main">
  <div class="task-list">
    <div class="task-list-header">Tasks</div>
    <div class="task-items" id="task-items"><div class="empty-list">No tasks yet</div></div>
  </div>
  <div class="detail" id="detail">
    <div class="detail-empty">← 选择一个任务查看详情</div>
  </div>
</div>

<script>
const agents = {};
const tasks = {};
const conversations = {};
let selectedTaskID = null;
const agentColors = {};
let colorIdx = 0;

function agentColor(id) {
  if (!agentColors[id]) agentColors[id] = colorIdx++ % 6;
  return agentColors[id];
}
function agentInitials(id) {
  return id.replace(/[^a-zA-Z0-9]/g,'').slice(0,2).toUpperCase() || '?';
}
function escHtml(s) {
  return String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}
function fmtTime(ts) {
  return new Date(ts).toLocaleTimeString();
}

// ── Agents strip ──
function renderAgents() {
  const el = document.getElementById('agents-strip');
  const keys = Object.keys(agents);
  if (!keys.length) { el.innerHTML = '<span style="font-size:0.75rem;color:#444;align-self:center">No agents</span>'; return; }
  el.innerHTML = keys.map(id => {
    const a = agents[id];
    const dc = a.status === 'busy' ? 'dot-busy' : 'dot-idle';
    return ` + "`" + `<div class="agent-chip">
      <span class="dot ${dc}"></span>
      <span class="aname">${escHtml(a.id)}</span>
      ${a.role ? ` + "`" + `<span class="arole">${escHtml(a.role)}</span>` + "`" + ` : ''}
      ${a.current_task ? ` + "`" + `<span style="color:#4f7ef0;font-size:0.68rem">${escHtml(a.current_task)}</span>` + "`" + ` : ''}
    </div>` + "`" + `;
  }).join('');
}

// ── Task list ──
function renderTaskList() {
  const el = document.getElementById('task-items');
  const keys = Object.keys(tasks).sort((a,b) => tasks[a].created_at > tasks[b].created_at ? 1 : -1);
  if (!keys.length) { el.innerHTML = '<div class="empty-list">No tasks yet</div>'; return; }
  el.innerHTML = keys.map(id => {
    const t = tasks[id];
    const active = selectedTaskID === id ? ' active' : '';
    return ` + "`" + `<div class="task-item${active}" onclick="selectTask('${id}')">
      <div class="tid">${escHtml(t.id)}</div>
      <div class="tdesc">${escHtml(t.description)}</div>
      <div class="tmeta">
        <span class="badge badge-${t.status}">${t.status}</span>
        ${t.assigned_to ? ` + "`" + `<span>${escHtml(t.assigned_to)}</span>` + "`" + ` : ''}
      </div>
    </div>` + "`" + `;
  }).join('');
}

// ── Detail panel ──
function renderDetail() {
  const el = document.getElementById('detail');
  if (!selectedTaskID || !tasks[selectedTaskID]) {
    el.innerHTML = '<div class="detail-empty">← 选择一个任务查看详情</div>';
    return;
  }
  const t = tasks[selectedTaskID];
  const entries = conversations[selectedTaskID] || [];

  let resultHtml = '';
  if (t.result) {
    resultHtml = ` + "`" + `<div class="result-box">${escHtml(t.result)}</div>` + "`" + `;
  } else if (t.error) {
    resultHtml = ` + "`" + `<div class="result-box err">${escHtml(t.error)}</div>` + "`" + `;
  }

  const msgHtml = entries.length === 0
    ? '<div class="session-empty">暂无对话记录</div>'
    : entries.map(e => {
        const ci = agentColor(e.from);
        const initials = agentInitials(e.from);
        return ` + "`" + `<div class="msg-entry">
          <div class="msg-avatar c${ci}">${initials}</div>
          <div class="msg-body">
            <div class="msg-header">
              <span class="msg-from c${ci}">${escHtml(e.from)}</span>
              <span class="msg-arrow">→</span>
              <span class="msg-to">${escHtml(e.to)}</span>
              <span class="msg-type-tag">${escHtml(e.msg_type)}</span>
              <span class="msg-time">${fmtTime(e.at)}</span>
            </div>
            <div class="msg-content">${escHtml(e.content)}</div>
          </div>
        </div>` + "`" + `;
      }).join('');

  el.innerHTML = ` + "`" + `
    <div class="task-header">
      <div class="th-id">${escHtml(t.id)}</div>
      <div class="th-desc">${escHtml(t.description)}</div>
      <div class="th-meta">
        <span><span class="badge badge-${t.status}">${t.status}</span></span>
        ${t.assigned_to ? ` + "`" + `<span>assigned → ${escHtml(t.assigned_to)}</span>` + "`" + ` : ''}
        ${t.created_by  ? ` + "`" + `<span>by ${escHtml(t.created_by)}</span>` + "`" + ` : ''}
        <span>${fmtTime(t.created_at)}</span>
      </div>
      ${resultHtml}
    </div>
    <div class="session-label">Session Log</div>
    <div class="session-log" id="session-log">${msgHtml}</div>
  ` + "`" + `;
}

function scrollSessionToBottom() {
  const el = document.getElementById('session-log');
  if (el) el.scrollTop = el.scrollHeight;
}

function selectTask(id) {
  selectedTaskID = id;
  renderTaskList();
  if (!conversations[id]) {
    fetch('/api/conversations/' + id)
      .then(r => r.json())
      .then(data => { conversations[id] = data || []; renderDetail(); scrollSessionToBottom(); })
      .catch(() => { conversations[id] = []; renderDetail(); });
    renderDetail(); // 先渲染空状态
    return;
  }
  renderDetail();
  scrollSessionToBottom();
}

// ── SSE events ──
function applyEvent(type, data) {
  if (type === 'snapshot.agents') {
    data.forEach(a => { agents[a.id] = a; });
    renderAgents();
  } else if (type === 'snapshot.tasks') {
    data.forEach(t => { tasks[t.id] = t; });
    renderTaskList();
    if (selectedTaskID) renderDetail();
  } else if (type === 'agent.registered' || type === 'agent.updated') {
    if (data.data) { agents[data.agent_id] = data.data; renderAgents(); }
  } else if (type === 'agent.evicted') {
    delete agents[data.agent_id]; renderAgents();
  } else if (type === 'task.created') {
    if (data.data) {
      tasks[data.task_id] = data.data;
      renderTaskList();
      // 自动选中第一个任务
      if (!selectedTaskID) selectTask(data.task_id);
    }
  } else if (type === 'task.updated') {
    if (data.data) {
      tasks[data.task_id] = data.data;
      renderTaskList();
      if (selectedTaskID === data.task_id) renderDetail();
    }
  } else if (type === 'conversation.added') {
    const entry = data.data;
    if (!entry) return;
    const tid = entry.task_id;
    if (!conversations[tid]) conversations[tid] = [];
    conversations[tid].push(entry);
    if (selectedTaskID === tid) {
      // 追加一条消息而不重绘整个 detail（保留滚动位置判断）
      const log = document.getElementById('session-log');
      if (log) {
        const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 60;
        const ci = agentColor(entry.from);
        const div = document.createElement('div');
        div.className = 'msg-entry';
        div.innerHTML = ` + "`" + `<div class="msg-avatar c${ci}">${agentInitials(entry.from)}</div>
          <div class="msg-body">
            <div class="msg-header">
              <span class="msg-from c${ci}">${escHtml(entry.from)}</span>
              <span class="msg-arrow">→</span>
              <span class="msg-to">${escHtml(entry.to)}</span>
              <span class="msg-type-tag">${escHtml(entry.msg_type)}</span>
              <span class="msg-time">${fmtTime(entry.at)}</span>
            </div>
            <div class="msg-content">${escHtml(entry.content)}</div>
          </div>` + "`" + `;
        // 清除"暂无记录"占位
        const empty = log.querySelector('.session-empty');
        if (empty) empty.remove();
        log.appendChild(div);
        if (atBottom) log.scrollTop = log.scrollHeight;
      }
    }
  }
}

function connect() {
  const es = new EventSource('/events');
  const connEl = document.getElementById('conn-status');
  es.onopen = () => { connEl.textContent = '⬤ connected'; connEl.className = 'conn ok'; };
  es.onerror = () => { connEl.textContent = '⬤ disconnected'; connEl.className = 'conn'; };
  ['snapshot.agents','snapshot.tasks','agent.registered','agent.updated','agent.evicted',
   'task.created','task.updated','conversation.added'].forEach(evt => {
    es.addEventListener(evt, e => {
      try { applyEvent(evt, JSON.parse(e.data)); } catch(err) { console.error(err); }
    });
  });
}

connect();
</script>
</body>
</html>`
