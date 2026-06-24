package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openai/symphony/go/internal/orchestrator"
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
	body{font:14px system-ui,sans-serif;margin:0;background:#f7f8fa;color:#20242a}header{display:flex;justify-content:space-between;align-items:center;padding:18px 24px;border-bottom:1px solid #d8dde4;background:#fff}main{padding:20px 24px;display:grid;gap:18px}h1{font-size:20px;margin:0}h2{font-size:15px;margin:0 0 10px}.toolbar{display:flex;gap:12px;align-items:center}.status{font-weight:600}.ok{color:#16723a}.bad{color:#b42318}button{border:1px solid #b8c0cc;background:#fff;border-radius:6px;padding:7px 10px;cursor:pointer}.metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px}.metric{background:#fff;border:1px solid #d8dde4;border-radius:6px;padding:12px}.metric strong{display:block;font-size:22px}.panel{background:#fff;border:1px solid #d8dde4;border-radius:6px;padding:14px;overflow:auto}table{width:100%;border-collapse:collapse}th,td{text-align:left;border-bottom:1px solid #e6e9ee;padding:8px;vertical-align:top}th{font-size:12px;text-transform:uppercase;color:#5d6673}code,pre{font:12px ui-monospace,SFMono-Regular,Menlo,monospace}pre{margin:0;background:#f1f3f6;border-radius:6px;padding:10px;overflow:auto}.empty{color:#6a7280}.tails{display:grid;gap:10px;margin-top:12px}details{border:1px solid #e1e5eb;border-radius:6px;padding:10px;background:#fafbfc}summary{cursor:pointer;font-weight:600}.chat-log{display:grid;gap:10px;margin-top:10px;max-height:520px;overflow:auto;padding-right:4px}.chat-msg{display:grid;gap:4px;max-width:min(920px,100%)}.chat-meta{font-size:12px;color:#6a7280}.chat-bubble{background:#eef4ff;border:1px solid #c9d9f4;border-radius:8px;padding:10px 12px;line-height:1.45}.chat-bubble p{margin:0 0 8px}.chat-bubble p:last-child{margin-bottom:0}.chat-heading{font-weight:700;color:#1f3b68}.chat-list-item{padding-left:14px;text-indent:-14px}.chat-code{white-space:pre-wrap;background:#18202b;color:#f4f7fb;border-radius:6px;padding:10px;overflow:auto}
</style>
<header><h1>Symphony</h1><div class="toolbar"><span id="status" class="status">loading...</span><button onclick="refresh()">Refresh now</button></div></header>
<main>
  <section class="metrics">
    <div class="metric"><span>Running</span><strong id="running-count">0</strong></div>
    <div class="metric"><span>Retrying</span><strong id="retrying-count">0</strong></div>
    <div class="metric"><span>Total tokens</span><strong id="token-count">0</strong></div>
    <div class="metric"><span>Runtime seconds</span><strong id="runtime-count">0</strong></div>
  </section>
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
function cell(v){return "<td>"+escape(v)+"</td>"}
function renderTable(rows, columns){
  if(!rows || rows.length === 0){return '<p class="empty">None</p>'}
  return '<table><thead><tr>'+columns.map(function(c){return '<th>'+c.label+'</th>'}).join('')+'</tr></thead><tbody>'+
    rows.map(function(row){return '<tr>'+columns.map(function(c){return cell(c.value(row))}).join('')+'</tr>'}).join('')+
    '</tbody></table>'
}
function logPath(x){return x.log_path || (x.logs && x.logs.codex_session_logs && x.logs.codex_session_logs[0] && x.logs.codex_session_logs[0].path) || ''}
function renderChatText(raw){
  var s=clean(raw).replace(/\r\n/g,"\n").trim();
  if(!s){return '<p class="empty">No agent text yet</p>'}
  var lines=s.split("\n"), out=[], para=[], code=[], inCode=false, fence=String.fromCharCode(96,96,96);
  function flushPara(){
    if(!para.length){return}
    out.push('<p>'+escapeRaw(para.join(" ").trim())+'</p>');
    para=[];
  }
  function flushCode(){
    out.push('<pre class="chat-code">'+escapeRaw(code.join("\n"))+'</pre>');
    code=[];
  }
  lines.forEach(function(line){
    if(line.trim().indexOf(fence) === 0){
      if(inCode){flushCode(); inCode=false}else{flushPara(); inCode=true}
      return;
    }
    if(inCode){code.push(line); return}
    if(line.trim() === ""){flushPara(); return}
    var heading=/^#{1,6}\s+(.+)$/.exec(line.trim());
    if(heading){flushPara(); out.push('<p class="chat-heading">'+escapeRaw(heading[1])+'</p>'); return}
    var list=/^\s*((?:[-*+])|\d+\.)\s+(.+)$/.exec(line);
    if(list){flushPara(); out.push('<p class="chat-list-item">'+escapeRaw(list[1])+' '+escapeRaw(list[2])+'</p>'); return}
    para.push(line.trim());
  });
  if(inCode){flushCode()}
  flushPara();
  return out.join('');
}
function renderAgentTail(rows){
  if(!rows || rows.length === 0){return ''}
  return '<div class="tails">'+rows.map(function(row){
    var messages=row.recent_agent_messages || [];
    var body=messages.length ? messages.map(function(m){
      var at=m.at ? new Date(m.at).toLocaleTimeString() : '';
      return '<div class="chat-msg"><div class="chat-meta">Agent '+escape(at)+'</div><div class="chat-bubble">'+renderChatText(m.text)+'</div></div>';
    }).join('') : '<p class="empty">No agent text yet</p>';
    return '<details open><summary>'+escape(row.issue_identifier || row.issue_id)+'</summary><div class="chat-log">'+body+'</div></details>';
  }).join('')+'</div>';
}
function renderRunning(rows){
  return renderTable(rows,[
    {label:'Issue',value:function(x){return x.issue_identifier}},
    {label:'State',value:function(x){return x.state}},
    {label:'Session',value:function(x){return x.session_id}},
    {label:'Turns',value:function(x){return x.turn_count}},
    {label:'Last event',value:function(x){return x.last_event}},
    {label:'Tokens',value:function(x){return tokens(x.tokens)}},
    {label:'Log',value:function(x){return logPath(x)}},
    {label:'Workspace',value:function(x){return x.workspace}}
  ])+renderAgentTail(rows);
}
function scrollTailLogs(){
  document.querySelectorAll('.chat-log').forEach(function(el){el.scrollTop=el.scrollHeight});
}
async function load(){
  try{
    const r=await fetch('/api/v1/state');
    const j=await r.json();
    document.getElementById('status').innerHTML='<span class="ok">running</span> '+new Date(j.generated_at).toLocaleString();
    document.getElementById('running-count').textContent=text(j.counts && j.counts.running);
    document.getElementById('retrying-count').textContent=text(j.counts && j.counts.retrying);
    document.getElementById('token-count').textContent=text(j.agent_totals && j.agent_totals.total_tokens);
    document.getElementById('runtime-count').textContent=text(j.agent_totals && Math.round(j.agent_totals.seconds_running || 0));
    document.getElementById('running').innerHTML=renderRunning(j.running);
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
