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
.detail { flex: 1; overflow-y: auto; }
.detail-empty { display: flex; align-items: center; justify-content: center; height: 100%; color: #333; font-size: 0.9rem; }

/* Task header — compact, no result here */
.task-header { padding: 14px 20px 10px; border-bottom: 1px solid #1e1e28; }
.task-header .th-id { font-size: 0.7rem; color: #555; margin-bottom: 4px; }
.task-header .th-desc { font-size: 0.92rem; font-weight: 600; color: #e8e8f0; margin-bottom: 8px; line-height: 1.5; }
.task-header .th-meta { display: flex; gap: 16px; font-size: 0.75rem; color: #666; flex-wrap: wrap; }

/* Session log */
.session-label { padding: 10px 20px 6px; font-size: 0.7rem; font-weight: 600; color: #444; text-transform: uppercase; letter-spacing: 1px; }
.session-log { padding: 0 20px 20px; }
.session-empty { color: #333; font-size: 0.82rem; font-style: italic; padding-top: 12px; }

/* Regular message entry */
.msg-entry { display: flex; gap: 10px; margin-bottom: 10px; align-items: flex-start; }
.msg-avatar { width: 28px; height: 28px; border-radius: 50%; background: #1e2030; display: flex; align-items: center; justify-content: center; font-size: 0.65rem; font-weight: 700; flex-shrink: 0; margin-top: 1px; }
.msg-body { flex: 1; min-width: 0; }
.msg-header { display: flex; align-items: baseline; gap: 6px; margin-bottom: 3px; flex-wrap: wrap; }
.msg-from { font-size: 0.8rem; font-weight: 600; }
.msg-arrow { font-size: 0.72rem; color: #444; }
.msg-to   { font-size: 0.78rem; color: #666; }
.msg-type-tag { font-size: 0.65rem; padding: 1px 5px; border-radius: 4px; background: #1e2030; color: #666; }
.msg-time { font-size: 0.68rem; color: #444; margin-left: auto; }
.msg-content { font-size: 0.82rem; color: #b0b0c0; line-height: 1.6; background: #141420; border-radius: 6px; padding: 8px 12px; word-break: break-word; }

/* Result card — 最终输出，放在 session log 底部 */
.result-card { margin-top: 14px; border-radius: 8px; overflow: hidden; }
.result-card-header { display: flex; align-items: center; gap: 8px; padding: 8px 14px; font-size: 0.72rem; font-weight: 600; letter-spacing: 0.5px; }
.result-card.ok .result-card-header  { background: #162a1e; color: #4ade80; }
.result-card.err .result-card-header { background: #2a1616; color: #f87171; }
.result-card-body { padding: 12px 14px; font-size: 0.82rem; line-height: 1.65; word-break: break-word; }
.result-card.ok  .result-card-body  { background: #111a16; color: #a8d8b8; }
.result-card.err .result-card-body  { background: #180f0f; color: #e09090; }

/* Markdown styles inside .msg-content and .result-card-body */
.md h1,.md h2,.md h3,.md h4 { color: #d8d8e8; font-weight: 600; margin: 0.8em 0 0.3em; line-height: 1.3; }
.md h1 { font-size: 1.1em; }
.md h2 { font-size: 1.0em; }
.md h3 { font-size: 0.95em; }
.md h4 { font-size: 0.9em; color: #a0a0b8; }
.md p  { margin: 0.4em 0; }
.md strong { color: #e0e0f0; font-weight: 600; }
.md em { font-style: italic; color: #c8b8f0; }
.md code { background: #1e1e30; color: #7ec8c8; padding: 1px 5px; border-radius: 3px; font-family: 'JetBrains Mono', 'Fira Code', monospace; font-size: 0.9em; }
.md hr { border: none; border-top: 1px solid #2a2a38; margin: 0.8em 0; }
.md ul,.md ol { padding-left: 1.4em; margin: 0.3em 0; }
.md li { margin: 0.15em 0; }
.md blockquote { border-left: 3px solid #3a3a50; padding-left: 10px; color: #888; margin: 0.4em 0; }

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
function fmtTime(ts) {
  return new Date(ts).toLocaleTimeString();
}

// ── Minimal Markdown renderer ──────────────────────────────────────────────
function renderMd(raw) {
  let s = String(raw || '')
    .replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');

  // Fenced code blocks (triple-backtick)
  s = s.replace(/\x60{3}[\s\S]*?\x60{3}/g, function(m) {
    const code = m.slice(3, -3).replace(/^\n/, '');
    return '<pre style="background:#1a1a2c;padding:8px 10px;border-radius:5px;overflow-x:auto;margin:6px 0"><code style="font-family:monospace;font-size:0.85em;color:#8ec07c">' + code + '</code></pre>';
  });

  // Headings
  s = s.replace(/^#### (.+)$/gm, '<h4>$1</h4>');
  s = s.replace(/^### (.+)$/gm,  '<h3>$1</h3>');
  s = s.replace(/^## (.+)$/gm,   '<h2>$1</h2>');
  s = s.replace(/^# (.+)$/gm,    '<h1>$1</h1>');

  // Horizontal rule
  s = s.replace(/^---+$/gm, '<hr>');

  // Bold + italic
  s = s.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>');
  s = s.replace(/\*\*(.+?)\*\*/g,     '<strong>$1</strong>');
  s = s.replace(/\*([^*\n]+?)\*/g,    '<em>$1</em>');

  // Inline code
  s = s.replace(/\x60([^\x60\n]+)\x60/g, '<code>$1</code>');

  // Blockquote
  s = s.replace(/^&gt; (.+)$/gm, '<blockquote>$1</blockquote>');

  // Unordered list items
  s = s.replace(/^[\*\-] (.+)$/gm, '<li>$1</li>');
  s = s.replace(/(<li>.*<\/li>\n?)+/g, m => '<ul>' + m + '</ul>');

  // Paragraphs: split by blank lines
  const blocks = s.split(/\n{2,}/);
  s = blocks.map(b => {
    b = b.trim();
    if (!b) return '';
    if (/^<(h[1-4]|ul|ol|li|hr|pre|blockquote)/.test(b)) return b;
    return '<p>' + b.replace(/\n/g, '<br>') + '</p>';
  }).join('\n');

  return '<span class="md">' + s + '</span>';
}

// plain text strip (for task list preview)
function stripMd(s) {
  return String(s||'').replace(/\*+/g,'').replace(/#+\s*/g,'').replace(/\x60/g,'').replace(/\n/g,' ').trim();
}

// ── Agents strip ──────────────────────────────────────────────────────────
function renderAgents() {
  const el = document.getElementById('agents-strip');
  const keys = Object.keys(agents);
  if (!keys.length) { el.innerHTML = '<span style="font-size:0.75rem;color:#444;align-self:center">No agents</span>'; return; }
  el.innerHTML = keys.map(id => {
    const a = agents[id];
    const dc = a.status === 'busy' ? 'dot-busy' : 'dot-idle';
    const name = String(a.id||'');
    const role = a.role ? '<span class="arole">' + a.role + '</span>' : '';
    const task = a.current_task ? '<span style="color:#4f7ef0;font-size:0.68rem">' + a.current_task + '</span>' : '';
    return '<div class="agent-chip"><span class="dot ' + dc + '"></span><span class="aname">' + name + '</span>' + role + task + '</div>';
  }).join('');
}

// ── Task list ─────────────────────────────────────────────────────────────
function renderTaskList() {
  const el = document.getElementById('task-items');
  const keys = Object.keys(tasks).sort((a,b) => tasks[a].created_at > tasks[b].created_at ? 1 : -1);
  if (!keys.length) { el.innerHTML = '<div class="empty-list">No tasks yet</div>'; return; }
  el.innerHTML = keys.map(id => {
    const t = tasks[id];
    const active = selectedTaskID === id ? ' active' : '';
    const desc = stripMd(t.description).slice(0, 60);
    const assignee = t.assigned_to ? '<span>' + t.assigned_to + '</span>' : '';
    return '<div class="task-item' + active + '" onclick="selectTask(\'' + id + '\')">' +
      '<div class="tid">' + t.id + '</div>' +
      '<div class="tdesc">' + desc + '</div>' +
      '<div class="tmeta"><span class="badge badge-' + t.status + '">' + t.status + '</span>' + assignee + '</div>' +
      '</div>';
  }).join('');
}

// ── Result card (placed in session log) ──────────────────────────────────
function buildResultCard(t) {
  if (!t.result && !t.error) return '';
  if (t.error) {
    return '<div class="result-card err" id="result-card">' +
      '<div class="result-card-header">✕ 任务失败</div>' +
      '<div class="result-card-body">' + renderMd(t.error) + '</div>' +
      '</div>';
  }
  return '<div class="result-card ok" id="result-card">' +
    '<div class="result-card-header">✓ 任务完成</div>' +
    '<div class="result-card-body">' + renderMd(t.result) + '</div>' +
    '</div>';
}

// ── Build a single message entry DOM string ───────────────────────────────
function buildMsgEntry(e) {
  const ci = agentColor(e.from);
  return '<div class="msg-entry">' +
    '<div class="msg-avatar c' + ci + '">' + agentInitials(e.from) + '</div>' +
    '<div class="msg-body">' +
      '<div class="msg-header">' +
        '<span class="msg-from c' + ci + '">' + e.from + '</span>' +
        '<span class="msg-arrow">→</span>' +
        '<span class="msg-to">' + e.to + '</span>' +
        '<span class="msg-type-tag">' + e.msg_type + '</span>' +
        '<span class="msg-time">' + fmtTime(e.at) + '</span>' +
      '</div>' +
      '<div class="msg-content">' + renderMd(e.content) + '</div>' +
    '</div>' +
  '</div>';
}

// ── Detail panel ──────────────────────────────────────────────────────────
function renderDetail() {
  const el = document.getElementById('detail');
  if (!selectedTaskID || !tasks[selectedTaskID]) {
    el.innerHTML = '<div class="detail-empty">← 选择一个任务查看详情</div>';
    return;
  }
  const t = tasks[selectedTaskID];
  const entries = conversations[selectedTaskID] || [];

  const msgsHtml = entries.length === 0
    ? '<div class="session-empty">暂无对话记录</div>'
    : entries.map(buildMsgEntry).join('');

  const assignee = t.assigned_to ? '<span>assigned → ' + t.assigned_to + '</span>' : '';
  const creator  = t.created_by  ? '<span>by ' + t.created_by + '</span>'  : '';

  el.innerHTML =
    '<div class="task-header">' +
      '<div class="th-id">' + t.id + '</div>' +
      '<div class="th-desc">' + renderMd(t.description) + '</div>' +
      '<div class="th-meta">' +
        '<span><span class="badge badge-' + t.status + '">' + t.status + '</span></span>' +
        assignee + creator +
        '<span>' + fmtTime(t.created_at) + '</span>' +
      '</div>' +
    '</div>' +
    '<div class="session-label">Session Log</div>' +
    '<div class="session-log" id="session-log">' +
      msgsHtml +
      buildResultCard(t) +
    '</div>';
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
    renderDetail();
    return;
  }
  renderDetail();
  scrollSessionToBottom();
}

// ── SSE events ────────────────────────────────────────────────────────────
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
      if (!selectedTaskID) selectTask(data.task_id);
    }
  } else if (type === 'task.updated') {
    if (!data.data) return;
    tasks[data.task_id] = data.data;
    renderTaskList();
    if (selectedTaskID !== data.task_id) return;
    // 更新 header badge
    const header = document.querySelector('.task-header');
    if (header) {
      const badge = header.querySelector('.badge');
      if (badge) { badge.className = 'badge badge-' + data.data.status; badge.textContent = data.data.status; }
    }
    // 更新/追加 result card
    const log = document.getElementById('session-log');
    if (log && (data.data.result || data.data.error)) {
      const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
      const old = log.querySelector('#result-card');
      if (old) old.remove();
      log.insertAdjacentHTML('beforeend', buildResultCard(data.data));
      if (atBottom) log.scrollTop = log.scrollHeight;
    }
  } else if (type === 'conversation.added') {
    const entry = data.data;
    if (!entry) return;
    const tid = entry.task_id;
    if (!conversations[tid]) conversations[tid] = [];
    conversations[tid].push(entry);
    if (selectedTaskID !== tid) return;
    const log = document.getElementById('session-log');
    if (!log) return;
    const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 60;
    // 新消息插在 result-card 之前（如果存在）
    const rc = log.querySelector('#result-card');
    const tmp = document.createElement('div');
    tmp.innerHTML = buildMsgEntry(entry);
    const node = tmp.firstChild;
    const empty = log.querySelector('.session-empty');
    if (empty) empty.remove();
    if (rc) log.insertBefore(node, rc);
    else log.appendChild(node);
    if (atBottom) log.scrollTop = log.scrollHeight;
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
