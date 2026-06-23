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
body{font:14px system-ui,sans-serif;margin:0;background:#f7f8fa;color:#20242a}header{display:flex;justify-content:space-between;align-items:center;padding:18px 24px;border-bottom:1px solid #d8dde4;background:#fff}main{padding:20px 24px;display:grid;gap:18px}h1{font-size:20px;margin:0}h2{font-size:15px;margin:0 0 10px}.toolbar{display:flex;gap:12px;align-items:center}.status{font-weight:600}.ok{color:#16723a}.bad{color:#b42318}button{border:1px solid #b8c0cc;background:#fff;border-radius:6px;padding:7px 10px;cursor:pointer}.metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px}.metric{background:#fff;border:1px solid #d8dde4;border-radius:6px;padding:12px}.metric strong{display:block;font-size:22px}.panel{background:#fff;border:1px solid #d8dde4;border-radius:6px;padding:14px;overflow:auto}table{width:100%;border-collapse:collapse}th,td{text-align:left;border-bottom:1px solid #e6e9ee;padding:8px;vertical-align:top}th{font-size:12px;text-transform:uppercase;color:#5d6673}code,pre{font:12px ui-monospace,SFMono-Regular,Menlo,monospace}pre{margin:0;background:#f1f3f6;border-radius:6px;padding:10px;overflow:auto}.empty{color:#6a7280}
</style>
<header><h1>Symphony</h1><div class="toolbar"><span id="status" class="status">loading...</span><button onclick="refresh()">Refresh now</button></div></header>
<main>
  <section class="metrics">
    <div class="metric"><span>Running</span><strong id="running-count">0</strong></div>
    <div class="metric"><span>Retrying</span><strong id="retrying-count">0</strong></div>
    <div class="metric"><span>Total tokens</span><strong id="token-count">0</strong></div>
    <div class="metric"><span>Runtime seconds</span><strong id="runtime-count">0</strong></div>
  </section>
  <section class="panel"><h2>Running sessions</h2><div id="running"></div></section>
  <section class="panel"><h2>Retry queue</h2><div id="retrying"></div></section>
  <section class="panel"><h2>Rate limits</h2><pre id="rate-limits">null</pre></section>
</main>
<script>
function text(v){return v === undefined || v === null || v === "" ? "n/a" : String(v)}
function tokens(t){return t ? text(t.total_tokens) : "0"}
function cell(v){return "<td>"+text(v).replace(/[&<>]/g,function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;"}[c]})+"</td>"}
function renderTable(rows, columns){
  if(!rows || rows.length === 0){return '<p class="empty">None</p>'}
  return '<table><thead><tr>'+columns.map(function(c){return '<th>'+c.label+'</th>'}).join('')+'</tr></thead><tbody>'+
    rows.map(function(row){return '<tr>'+columns.map(function(c){return cell(c.value(row))}).join('')+'</tr>'}).join('')+
    '</tbody></table>'
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
    document.getElementById('running').innerHTML=renderTable(j.running,[
      {label:'Issue',value:function(x){return x.issue_identifier}},
      {label:'State',value:function(x){return x.state}},
      {label:'Session',value:function(x){return x.session_id}},
      {label:'Turns',value:function(x){return x.turn_count}},
      {label:'Last event',value:function(x){return x.last_event}},
      {label:'Tokens',value:function(x){return tokens(x.tokens)}},
      {label:'Workspace',value:function(x){return x.workspace}}
    ]);
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
load(); setInterval(load,5000);
</script>`
