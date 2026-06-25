package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tptodorov/symphony/go/internal/orchestrator"
)

type Server struct{ orch *orchestrator.Orchestrator }

func New(o *orchestrator.Orchestrator) *Server { return &Server{orch: o} }
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(dashboardHTML))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/v1/state" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		json.NewEncoder(w).Encode(s.orch.Snapshot())
		return
	}
	if r.URL.Path == "/api/v1/refresh" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		go func() { _ = s.orch.Tick(context.Background()) }()
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"queued":       true,
			"coalesced":    false,
			"requested_at": time.Now().UTC(),
			"operations":   []string{"poll", "reconcile"},
		})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		if decoded, err := url.PathUnescape(id); err == nil {
			id = decoded
		}
		if issue, ok := s.orch.IssueSnapshot(id); ok {
			json.NewEncoder(w).Encode(issue)
			return
		}
		writeError(w, http.StatusNotFound, "issue_not_found", "issue is not known to the current in-memory state")
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "route not found")
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

const dashboardHTML = `<!doctype html>
<meta charset="utf-8">
<title>Symphony</title>
<style>
	body{font:14px system-ui,sans-serif;margin:0;background:#f7f8fa;color:#20242a}header{display:flex;justify-content:space-between;align-items:center;padding:18px 24px;border-bottom:1px solid #d8dde4;background:#fff}main{padding:20px 24px;display:grid;gap:18px}h1{font-size:20px;margin:0}h2{font-size:15px;margin:0 0 10px}.toolbar{display:flex;gap:12px;align-items:center}.status{font-weight:600}.ok{color:#16723a}.bad{color:#b42318}button{border:1px solid #b8c0cc;background:#fff;border-radius:6px;padding:7px 10px;cursor:pointer}.metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px}.metric{background:#fff;border:1px solid #d8dde4;border-radius:6px;padding:12px}.metric strong{display:block;font-size:22px}.panel{background:#fff;border:1px solid #d8dde4;border-radius:6px;padding:14px;overflow:auto}table{width:100%;border-collapse:collapse}th,td{text-align:left;border-bottom:1px solid #e6e9ee;padding:8px;vertical-align:top}th{font-size:12px;text-transform:uppercase;color:#5d6673}a{color:#175cd3;text-decoration:none}a:hover{text-decoration:underline}code,pre{font:12px ui-monospace,SFMono-Regular,Menlo,monospace}pre{margin:0;background:#f1f3f6;border-radius:6px;padding:10px;overflow:auto}.empty{color:#6a7280}.sessions-table{table-layout:fixed;min-width:840px}.sessions-table th,.sessions-table td{overflow:hidden}.sessions-table .col-issue{width:120px}.sessions-table .col-title{width:260px}.sessions-table .col-state{width:120px}.sessions-table .col-turns{width:70px}.sessions-table .col-event{width:155px}.sessions-table .col-tokens{width:115px}.path-stack{display:grid;gap:6px}.path-label{font-size:11px;text-transform:uppercase;color:#6a7280}.path-value{font:12px ui-monospace,SFMono-Regular,Menlo,monospace;word-break:break-all}.tails{display:grid;gap:10px;margin-top:12px}details{border:1px solid #e1e5eb;border-radius:6px;padding:10px;background:#fafbfc}summary{cursor:pointer;font-weight:600}.chat-log{display:grid;gap:10px;margin-top:10px;max-height:520px;overflow:auto;padding-right:4px}.chat-msg{display:grid;gap:4px;max-width:min(920px,100%)}.chat-meta{font-size:12px;color:#6a7280}.chat-bubble{background:#eef4ff;border:1px solid #c9d9f4;border-radius:8px;padding:10px 12px;line-height:1.45;white-space:pre-wrap}.setup-block{border:1px solid #f0c36a;background:#fff8e5;border-radius:8px;padding:10px 12px}.setup-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:8px;margin-top:8px}.setup-logs{margin-top:8px}.setup-error{white-space:pre-wrap;background:#fff1f0;border:1px solid #ffd0cc;color:#8a1f11;margin-top:8px}
</style>
<header><h1>Symphony</h1><div class="toolbar"><span id="status" class="status">loading...</span><button onclick="refresh()">Refresh now</button></div></header>
<main>
  <section class="metrics">
    <div class="metric"><span>Ready</span><strong id="ready-count">0</strong></div>
    <div class="metric"><span>Setting up</span><strong id="setup-count">0</strong></div>
    <div class="metric"><span>Running</span><strong id="running-count">0</strong></div>
    <div class="metric"><span>Retrying</span><strong id="retrying-count">0</strong></div>
    <div class="metric"><span>Total tokens</span><strong id="token-count">0</strong></div>
    <div class="metric"><span>Runtime seconds</span><strong id="runtime-count">0</strong></div>
  </section>
  <section class="panel"><h2>Queued Work</h2><div id="queued"></div></section>
  <section class="panel"><h2>Running Sessions</h2><div id="running"></div></section>
  <section class="panel"><h2>Retry queue</h2><div id="retrying"></div></section>
  <section class="panel"><h2>Rate limits</h2><pre id="rate-limits">null</pre></section>
</main>
<script>
function clean(v){return v === undefined || v === null ? "" : String(v)}
function text(v){return v === undefined || v === null || v === "" ? "n/a" : String(v)}
function tokens(t){return t ? text(t.total_tokens) : "0"}
function escape(v){return text(v).replace(/[&<>]/g,function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;"}[c]})}
function escapeRaw(v){return clean(v).replace(/[&<>]/g,function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;"}[c]})}
function escapeAttr(v){return clean(v).replace(/[&<>"']/g,function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]})}
function cell(v){return "<td>"+escape(v)+"</td>"}
function renderTable(rows, columns, tableClass){
  if(!rows || rows.length === 0){return '<p class="empty">None</p>'}
  var klass=tableClass ? ' class="'+escapeAttr(tableClass)+'"' : '';
  var cols=columns.some(function(c){return c.className}) ? '<colgroup>'+columns.map(function(c){return '<col class="'+escapeAttr(c.className || '')+'">'}).join('')+'</colgroup>' : '';
  return '<table'+klass+'>'+cols+'<thead><tr>'+columns.map(function(c){return '<th>'+c.label+'</th>'}).join('')+'</tr></thead><tbody>'+
    rows.map(function(row){return '<tr>'+columns.map(function(c){var cls=c.className ? ' class="'+escapeAttr(c.className)+'"' : ''; return c.html ? '<td'+cls+'>'+c.html(row)+'</td>' : '<td'+cls+'>'+escape(c.value(row))+'</td>'}).join('')+'</tr>'}).join('')+
    '</tbody></table>'
}
function logPath(x){return x.log_path || (x.logs && x.logs.codex_session_logs && x.logs.codex_session_logs[0] && x.logs.codex_session_logs[0].path) || ''}
function setupLogPath(x){return x && (x.log_path || (x.logs && x.logs[0] && x.logs[0].path)) || ''}
function renderLink(url){
  if(!url){return '<span class="empty">n/a</span>'}
  return '<a href="'+escapeAttr(url)+'" target="_blank" rel="noreferrer">'+escape(url)+'</a>'
}
function renderSetup(row){
  var setup=row && row.setup;
  if(!setup){return ''}
  var setupLog=setupLogPath(setup);
  var agentLog=logPath(row);
  var workspace=setup.workspace || setup.failed_workspace || row.workspace || '';
  var html='<div class="setup-block"><div class="chat-meta">Setup '+escape(setup.status)+' - '+escape(setup.stage)+'</div><div class="setup-grid">';
  html+='<div><div class="path-label">Stage</div><div class="path-value">'+escape(setup.stage)+'</div></div>';
  html+='<div><div class="path-label">Status</div><div class="path-value">'+escape(setup.status)+'</div></div>';
  if(setup.hook){html+='<div><div class="path-label">Hook</div><div class="path-value">'+escape(setup.hook)+'</div></div>'}
  if(workspace){html+='<div><div class="path-label">Workspace</div><div class="path-value">'+escape(workspace)+'</div></div>'}
  if(setup.failed_workspace){html+='<div><div class="path-label">Failed workspace</div><div class="path-value">'+escape(setup.failed_workspace)+'</div></div>'}
  html+='</div>';
  if(setupLog || (agentLog && agentLog !== setupLog)){
    html+='<div class="path-stack setup-logs"><div class="path-label">Logs</div>';
    if(setupLog){html+='<div><div class="path-label">Setup log</div><div class="path-value">'+escape(setupLog)+'</div></div>'}
    if(agentLog && agentLog !== setupLog){html+='<div><div class="path-label">Agent log</div><div class="path-value">'+escape(agentLog)+'</div></div>'}
    html+='</div>';
  }
  if(setup.error){html+='<pre class="setup-error">'+escapeRaw(setup.error)+'</pre>'}
  return html+'</div>';
}
function renderAgentTail(rows){
  if(!rows || rows.length === 0){return ''}
  return '<div class="tails">'+rows.map(function(row){
    var messages=row.recent_agent_messages || [];
    var setup=renderSetup(row);
    var body=messages.length ? messages.map(function(m){
      var at=m.at ? new Date(m.at).toLocaleTimeString() : '';
      return '<div class="chat-msg"><div class="chat-meta">Agent '+escape(at)+'</div><div class="chat-bubble">'+escapeRaw(m.text)+'</div></div>';
    }).join('') : '<p class="empty">No agent text yet</p>';
    return '<details open><summary>'+escape(row.issue_identifier || row.issue_id)+'</summary><div class="chat-log">'+setup+body+'</div></details>';
  }).join('')+'</div>';
}
function renderQueued(rows){
  return renderTable((rows || []).slice(0,5),[
    {label:'Issue',value:function(x){return x.issue_identifier}},
    {label:'State',value:function(x){return x.state}},
    {label:'Priority',value:function(x){return x.priority}},
    {label:'Title',value:function(x){return x.title}},
    {label:'URL',html:function(x){return renderLink(x.issue_url)}}
  ])
}
function sessionRows(running, setup){
  var rows=[], seen={};
  (running || []).forEach(function(row){rows.push(row); seen[row.issue_id]=true});
  (setup || []).forEach(function(s){
    if(seen[s.issue_id]){
      rows.forEach(function(row){if(row.issue_id===s.issue_id && !row.setup){row.setup=s}});
      return;
    }
    rows.push({
      issue_id:s.issue_id,
      issue_identifier:s.issue_identifier,
      issue_url:s.issue_url,
      title:s.title,
      state:s.state,
      status:'setup_'+s.status,
      turn_count:0,
      last_event:s.stage,
      tokens:null,
      log_path:s.log_path,
      logs:s.logs ? {codex_session_logs:s.logs} : null,
      workspace:s.workspace || s.failed_workspace,
      setup:s,
      recent_agent_messages:[]
    });
  });
  return rows;
}
function renderRunning(rows){
  return renderTable(rows,[
    {label:'Issue',className:'col-issue',value:function(x){return x.issue_identifier}},
    {label:'Title',className:'col-title',value:function(x){return x.title}},
    {label:'State',className:'col-state',value:function(x){return x.state}},
    {label:'Turns',className:'col-turns',value:function(x){return x.turn_count}},
    {label:'Last event',className:'col-event',value:function(x){return x.last_event}},
    {label:'Total tokens',className:'col-tokens',value:function(x){return tokens(x.tokens)}}
  ],'sessions-table')+renderAgentTail(rows);
}
function scrollTailLogs(){
  document.querySelectorAll('.chat-log').forEach(function(el){el.scrollTop=el.scrollHeight});
}
async function load(){
  try{
    const r=await fetch('/api/v1/state');
    const j=await r.json();
    document.getElementById('status').innerHTML='<span class="ok">running</span> '+new Date(j.generated_at).toLocaleString();
    document.getElementById('ready-count').textContent=text(j.counts && j.counts.ready);
    document.getElementById('setup-count').textContent=text(j.counts && j.counts.setup);
    document.getElementById('running-count').textContent=text(j.counts && j.counts.running);
    document.getElementById('retrying-count').textContent=text(j.counts && j.counts.retrying);
    document.getElementById('token-count').textContent=text(j.agent_totals && j.agent_totals.total_tokens);
    document.getElementById('runtime-count').textContent=text(j.agent_totals && Math.round(j.agent_totals.seconds_running || 0));
    document.getElementById('queued').innerHTML=renderQueued(j.ready);
    document.getElementById('running').innerHTML=renderRunning(sessionRows(j.running,j.setup));
    requestAnimationFrame(scrollTailLogs);
    document.getElementById('retrying').innerHTML=renderTable(j.retrying,[
      {label:'Issue',value:function(x){return x.issue_identifier}},
      {label:'Attempt',value:function(x){return x.attempt}},
      {label:'Due at',value:function(x){return x.due_at ? new Date(x.due_at).toLocaleString() : ''}},
      {label:'Error',value:function(x){return x.error}}
    ]);
    document.getElementById('rate-limits').textContent=JSON.stringify(j.rate_limits || null,null,2);
  }catch(e){document.getElementById('status').innerHTML='<span class="bad">offline</span> '+e;}
}
async function refresh(){await fetch('/api/v1/refresh',{method:'POST'}); await load();}
load(); setInterval(load,2000);
	</script>`
