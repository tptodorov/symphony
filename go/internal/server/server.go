package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
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
	if r.URL.Path == "/assets/symphony-mark.svg" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte(symphonyMarkSVG))
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
		eventsRoute := false
		if strings.HasSuffix(id, "/events") {
			eventsRoute = true
			id = strings.TrimSuffix(id, "/events")
		}
		if decoded, err := url.PathUnescape(id); err == nil {
			id = decoded
		}
		if eventsRoute {
			limit := 100
			if raw := r.URL.Query().Get("limit"); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil {
					limit = parsed
				}
			}
			if events, ok := s.orch.IssueEvents(id, limit); ok {
				json.NewEncoder(w).Encode(events)
				return
			}
			writeError(w, http.StatusNotFound, "issue_not_found", "issue is not known to the current in-memory state")
			return
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

const symphonyMarkSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32" role="img" aria-label="Symphony">
  <rect width="32" height="32" rx="7" fill="#091A23"></rect>
  <g>
    <rect x="7.4" y="15" width="2.8" height="9.6" rx="1.4" fill="#FFFFFF"></rect>
    <rect x="12.6" y="8.6" width="2.8" height="16" rx="1.4" fill="#5ED1FF"></rect>
    <rect x="17.8" y="17" width="2.8" height="7.6" rx="1.4" fill="#FFFFFF"></rect>
    <rect x="23" y="12" width="2.8" height="12.6" rx="1.4" fill="#FFFFFF"></rect>
  </g>
</svg>`

const dashboardHTML = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Symphony</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500&family=Space+Mono&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;}
html,body{margin:0;height:100%;-webkit-font-smoothing:antialiased;}
:root{
  --font-sans:'Space Grotesk',system-ui,-apple-system,sans-serif;
  --font-mono:'Space Mono',ui-monospace,'SFMono-Regular',monospace;
  --fg:#1A2B35;
  --fg-muted:#2D4754;
  --fg-subtle:#5C707A;
  --bg:#FFFFFF;
  --bg-elev:#F3F5F6;
  --border:#D9D9D9;
  --page-bg:#EEF1F2;
  --hyper:#FF4438;
  --hyper-bg:#FFF3F2;
  --hyper-border:#FFDFDC;
  --hyper-dark:#B91C10;
  --blue:#0E8EC0;
  --lime:#A9CA03;
  --lime-dark:#5E7000;
  --purple:#9C42CE;
  --slate:#5C707A;
  --midnight:#091A23;
}
body{font-family:var(--font-sans);color:var(--fg);background:var(--page-bg);}
a{color:inherit;text-decoration:none;}
a.lnk{color:var(--fg-subtle);transition:color 120ms;}
a.lnk:hover{color:var(--hyper);text-decoration:underline;}
button{cursor:pointer;font-family:var(--font-sans);}
@keyframes rdpulse{0%{opacity:1;transform:scale(1);}50%{opacity:.4;transform:scale(.8);}100%{opacity:1;transform:scale(1);}}
.ell{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.scroll{overflow-y:auto;}
.scroll::-webkit-scrollbar{width:8px;}
.scroll::-webkit-scrollbar-thumb{background:var(--border);border-radius:999px;border:2px solid var(--page-bg);}

#app{height:100vh;display:flex;flex-direction:column;}

/* Top bar */
#topbar{display:flex;align-items:center;justify-content:space-between;padding:14px 24px;background:var(--bg);border-bottom:1px solid var(--border);flex:none;}
.brand{display:flex;align-items:center;gap:10px;}
.brand-name{font-size:21px;font-weight:500;letter-spacing:-0.01em;}
.controls{display:flex;align-items:center;gap:16px;}
#status-dot{width:9px;height:9px;border-radius:999px;background:#D0F41D;animation:rdpulse 1.6s ease-in-out infinite;flex:none;}
#status-text{font-family:var(--font-mono);font-size:13px;color:var(--fg-subtle);display:inline-flex;align-items:center;gap:8px;}
#status-label{color:var(--fg);font-weight:500;}
.auto-label{font-size:13px;color:var(--fg-subtle);}
.btn-sm{border:1px solid var(--border);background:var(--bg);color:var(--fg);border-radius:6px;padding:6px 12px;font-size:13px;transition:background 120ms;}
.btn-sm:hover{background:var(--page-bg);}

/* KPI strip */
#kpi{display:grid;grid-template-columns:repeat(7,1fr);background:var(--bg);border-bottom:1px solid var(--border);flex:none;}
.kpi-tile{padding:14px 20px;border-right:1px solid var(--border);}
.kpi-tile:last-child{border-right:none;}
.kpi-lbl{font-family:var(--font-mono);font-size:11px;letter-spacing:0.06em;text-transform:uppercase;color:var(--fg-subtle);}
.kpi-val{font-size:28px;font-weight:500;line-height:1.1;margin-top:3px;}

/* Main */
#main{flex:1;min-height:0;display:flex;}

/* Sessions pane */
#pane{flex:1;min-width:0;padding:20px 24px 32px;}
.pane-hdr{display:flex;align-items:baseline;gap:6px;margin-bottom:12px;}
.pane-title{font-size:16px;font-weight:500;}
.pane-count{color:var(--fg-subtle);font-weight:400;}
.ll{display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-bottom:14px;font-family:var(--font-mono);font-size:11px;color:var(--fg-subtle);}
.ll-lbl{letter-spacing:0.06em;text-transform:uppercase;}
.ll-dot{width:9px;height:9px;border-radius:999px;display:inline-block;vertical-align:middle;margin-right:3px;}
.ll-run{display:inline-flex;align-items:center;padding:1px 7px;border-radius:999px;background:var(--blue);color:#fff;font-size:9px;vertical-align:middle;margin-right:3px;}
.ll-hint{color:var(--hyper);margin-left:6px;}
.sessions-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(440px,1fr));gap:14px;align-items:start;}

/* Session card */
.card{background:var(--bg);border:1px solid var(--border);border-radius:8px;padding:15px 16px;cursor:pointer;transition:border-color 120ms;display:flex;flex-direction:column;gap:9px;}
.card:hover{border-color:var(--fg);}
.card-hdr{display:flex;align-items:center;gap:8px;}
.iss-key{font-family:var(--font-mono);font-size:12px;}
.card-right{margin-left:auto;display:flex;align-items:center;gap:8px;}
.no-pr{font-family:var(--font-mono);font-size:11px;color:var(--fg-subtle);}
.pr-chip{display:inline-flex;align-items:center;gap:5px;border:1px solid var(--border);border-radius:999px;padding:2px 7px;font-family:var(--font-mono);font-size:11px;color:var(--fg-muted);background:var(--bg);}
.pr-dot{width:7px;height:7px;border-radius:999px;background:var(--fg-subtle);flex:none;}
.pr-open .pr-dot{background:var(--blue);}
.pr-merged .pr-dot{background:var(--lime);}
.pr-closed .pr-dot{background:var(--hyper);}
.card-title{font-size:14.5px;font-weight:500;line-height:1.3;}
.card-meta{font-family:var(--font-mono);font-size:11px;color:var(--fg-subtle);}
.card-msg{font-size:12.5px;color:var(--fg-muted);line-height:1.4;border-top:1px solid var(--page-bg);padding-top:9px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.detail-box{border-top:1px solid var(--page-bg);padding-top:9px;display:flex;flex-direction:column;gap:5px;}
.detail-row{display:grid;grid-template-columns:72px minmax(0,1fr);gap:8px;align-items:baseline;font-family:var(--font-mono);font-size:10.5px;color:var(--fg-subtle);}
.detail-key{color:var(--fg-muted);}
.detail-val{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}

/* Lifecycle track */
.lc{display:flex;align-items:center;}
.lc-node{display:flex;align-items:center;flex:1;}
.lc-dot{width:10px;height:10px;border-radius:999px;flex:none;}
.lc-pill{display:inline-flex;align-items:center;padding:2px 7px;border-radius:999px;font-family:var(--font-mono);font-size:10px;flex:none;}
.lc-line{flex:1;height:2px;}
.dot-done{background:var(--midnight);}
.dot-cur{background:var(--hyper);box-shadow:0 0 0 3px rgba(255,68,56,.18);}
.dot-fut{background:transparent;border:1.5px solid var(--border);}
.pill-cur{background:var(--blue);color:#fff;}
.pill-done{background:var(--midnight);color:#fff;}
.pill-fut{background:transparent;border:1.5px solid var(--border);color:var(--fg-subtle);}
.line-done{background:var(--midnight);}
.line-fut{background:var(--border);}

/* Activity stream */
.act-hdr{border-top:1px solid var(--border);margin-top:2px;padding-top:9px;}
.act-lbl{font-family:var(--font-mono);font-size:10px;letter-spacing:0.08em;text-transform:uppercase;color:var(--fg-subtle);margin-bottom:6px;}
.act-stream{display:flex;flex-direction:column;max-height:220px;overflow-y:auto;}
.act-row{display:flex;gap:10px;align-items:baseline;padding:5px 0;border-bottom:1px solid var(--page-bg);}
.act-ts{font-family:var(--font-mono);font-size:10.5px;color:var(--fg-subtle);flex:none;width:44px;}
.act-msg{flex:1;font-size:12px;line-height:1.4;color:var(--fg-muted);}

/* Right rail */
#rail{width:390px;flex:none;border-left:1px solid var(--border);background:var(--bg-elev);padding:20px 18px 32px;display:flex;flex-direction:column;gap:24px;}
.rail-hdr{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;}
.rail-title{font-size:15px;font-weight:500;}
.rail-cnt{color:var(--fg-subtle);font-weight:400;}
.rail-hint{font-family:var(--font-mono);font-size:11px;color:var(--fg-subtle);}
.rail-rows{display:flex;flex-direction:column;gap:6px;}
.q-row{display:flex;align-items:center;gap:10px;padding:9px 11px;background:var(--bg);border:1px solid var(--border);border-radius:6px;}
.q-body{flex:1;min-width:0;}
.q-top{display:flex;justify-content:space-between;gap:8px;}
.q-title{font-size:12.5px;color:var(--fg);line-height:1.35;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.q-wait{font-family:var(--font-mono);font-size:11px;color:var(--fg-subtle);}
.prio{width:8px;height:8px;border-radius:999px;flex:none;}
.r-row{padding:11px;background:var(--hyper-bg);border:1px solid var(--hyper-border);border-radius:6px;}
.r-key{font-family:var(--font-mono);font-size:11px;color:var(--hyper-dark);}
.r-att{font-family:var(--font-mono);font-size:11px;color:var(--hyper-dark);}
.r-title{font-size:12.5px;color:var(--fg);line-height:1.35;margin-top:2px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.r-meta{font-family:var(--font-mono);font-size:11px;color:var(--fg-muted);margin-top:4px;}
.d-row{padding:10px 11px;background:var(--bg);border:1px solid var(--border);border-radius:6px;}
.d-top{display:flex;justify-content:space-between;gap:8px;align-items:center;}
.d-key{font-family:var(--font-mono);font-size:11px;}
.d-title{font-size:12.5px;color:var(--fg);line-height:1.35;margin-top:3px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.d-meta{font-family:var(--font-mono);font-size:10.5px;color:var(--fg-subtle);margin-top:4px;}
.empty{font-size:12px;color:var(--fg-subtle);padding:4px 0;}
</style>

<div id="app">
  <div id="topbar">
    <div class="brand">
      <img src="/assets/symphony-mark.svg" width="28" height="28" alt="">
      <span class="brand-name">Symphony</span>
    </div>
    <div class="controls">
      <span id="status-text">
        <span id="status-dot"></span>
        <span id="status-label">loading</span>
        <span id="status-ts"></span>
      </span>
      <span class="auto-label">auto-refresh 5s</span>
      <button class="btn-sm" onclick="doRefresh()">Refresh now</button>
    </div>
  </div>

  <div id="kpi">
    <div class="kpi-tile"><div class="kpi-lbl">Queued</div><div class="kpi-val" id="kv-q">—</div></div>
    <div class="kpi-tile"><div class="kpi-lbl">Preparing</div><div class="kpi-val" id="kv-p">—</div></div>
    <div class="kpi-tile"><div class="kpi-lbl">Agent run</div><div class="kpi-val" id="kv-r" style="color:var(--blue)">—</div></div>
    <div class="kpi-tile"><div class="kpi-lbl">Post-run hooks</div><div class="kpi-val" id="kv-ph" style="color:#7C1AB3">—</div></div>
    <div class="kpi-tile"><div class="kpi-lbl">Retrying</div><div class="kpi-val" id="kv-rt" style="color:var(--hyper)">—</div></div>
    <div class="kpi-tile"><div class="kpi-lbl">Done today</div><div class="kpi-val" id="kv-d" style="color:var(--lime-dark)">—</div></div>
    <div class="kpi-tile"><div class="kpi-lbl">Total tokens</div><div class="kpi-val" id="kv-t">—</div></div>
  </div>

  <div id="main">
    <div id="pane" class="scroll">
      <div class="pane-hdr">
        <span class="pane-title">Active sessions</span>
        <span class="pane-count" id="sess-cnt">· —</span>
      </div>
      <div class="ll">
        <span class="ll-lbl">Lifecycle</span>
        <span><span class="ll-dot" style="background:var(--slate)"></span>prepare</span>
        <span>·</span>
        <span><span class="ll-dot" style="background:var(--purple)"></span>hooks</span>
        <span>·</span>
        <span><span class="ll-run">run</span>open-ended, by turn</span>
        <span>·</span>
        <span><span class="ll-dot" style="background:var(--lime)"></span>done</span>
        <span class="ll-hint">◵ click a card to open its activity stream</span>
      </div>
      <div class="sessions-grid" id="grid"></div>
    </div>

    <div id="rail" class="scroll">
      <div>
        <div class="rail-hdr">
          <span class="rail-title">Queued work <span class="rail-cnt" id="rq-cnt"></span></span>
          <span class="rail-hint">next up ↓</span>
        </div>
        <div class="rail-rows" id="rq-rows"></div>
      </div>
      <div>
        <div class="rail-hdr">
          <span class="rail-title" style="color:var(--hyper-dark)">Retry queue <span class="rail-cnt" style="color:var(--fg-subtle)" id="rr-cnt"></span></span>
        </div>
        <div class="rail-rows" id="rr-rows"></div>
      </div>
      <div>
        <div class="rail-hdr">
          <span class="rail-title">Done today <span class="rail-cnt" id="rd-cnt"></span></span>
          <span class="rail-hint">landed ↑</span>
        </div>
        <div class="rail-rows" id="rd-rows"></div>
      </div>
    </div>
  </div>
</div>

<script>
var expandedId = null;
var lastState = null;

function x(v){
  if(v==null) return '';
  return String(v).replace(/[&<>"']/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];});
}
function val(v,fb){return v==null||v===''?(fb!=null?fb:'—'):String(v);}
function fmtTok(n){
  if(!n) return '0';
  if(n>=1e9) return (n/1e9).toFixed(2)+'B';
  if(n>=1e6) return (n/1e6).toFixed(2)+'M';
  if(n>=1e3) return (n/1e3).toFixed(1)+'K';
  return String(n);
}
function fmtSecs(s){
  if(!s||s<0) return '—';
  s=Math.round(s);
  if(s<60) return s+'s';
  var m=Math.floor(s/60);
  if(m<60) return m+'m';
  var h=Math.floor(m/60),r=m%60;
  return h+'h'+(r?' '+r+'m':'');
}
function fmtRelTime(iso){
  if(!iso) return '—';
  return fmtSecs((Date.now()-new Date(iso).getTime())/1000);
}
function fmtUntil(iso){
  if(!iso) return '—';
  return fmtSecs((new Date(iso).getTime()-Date.now())/1000);
}
function fmtTime(iso){
  if(!iso) return '';
  return new Date(iso).toLocaleTimeString('en-US',{hour12:false,hour:'2-digit',minute:'2-digit',second:'2-digit'});
}
function prioColor(p){
  if(p==null) return 'var(--fg-subtle)';
  if(p<=1) return 'var(--hyper)';
  if(p<=2) return 'var(--lime)';
  return 'var(--fg-subtle)';
}
function maxTurnsFor(row){
  if(row&&row.max_turns) return row.max_turns;
  if(lastState&&lastState.runtime_config&&lastState.runtime_config.agent_max_turns) return lastState.runtime_config.agent_max_turns;
  return 0;
}
function runtimeFor(row){
  if(row&&row.runtime_seconds) return fmtSecs(row.runtime_seconds);
  if(row&&row.started_at) return fmtRelTime(row.started_at);
  return '—';
}
function prHtml(pr,emptyText){
  if(!pr) return '<span class="no-pr">'+x(emptyText||'no PR yet')+'</span>';
  var label=pr.number?'#'+pr.number:(pr.head_branch||'PR');
  var state=(pr.state||'').toLowerCase();
  if(pr.merged_at) state='merged';
  var cls='pr-chip';
  if(state==='open') cls+=' pr-open';
  else if(state==='merged') cls+=' pr-merged';
  else if(state==='closed') cls+=' pr-closed';
  var text=label+(state?' '+state:'');
  var chip='<span class="'+cls+'"><span class="pr-dot"></span>'+x(text)+'</span>';
  if(pr.url) return '<a class="lnk" href="'+x(pr.url)+'" target="_blank" onclick="event.stopPropagation()">'+chip+'</a>';
  return chip;
}

function lcNodes(sess){
  // 7 nodes: 0=prepare 1=after_create 2=before_run 3=run 4=after_run 5=before_remove 6=done
  var nodes=[
    {isPill:false,lbl:'',state:'fut'},
    {isPill:false,lbl:'',state:'fut'},
    {isPill:false,lbl:'',state:'fut'},
    {isPill:true, lbl:'run',state:'fut'},
    {isPill:false,lbl:'',state:'fut'},
    {isPill:false,lbl:'',state:'fut'},
    {isPill:false,lbl:'',state:'fut'}
  ];
  var pos=0;
  var ph=sess.phase||'';
  var st=sess.stage||'';
  var hk=sess.hook||'';
  if(sess._t==='running'&&!ph) ph='agent_run';
  if(ph==='prepare'||st==='prepare'||st==='preparing_workspace'||st==='building_prompt') pos=0;
  else if(ph==='after_create'||hk==='after_create') pos=1;
  else if(ph==='before_run'||hk==='before_run') pos=2;
  else if(ph==='agent_run') pos=3;
  else if(ph==='after_run') pos=4;
  else if(ph==='before_remove') pos=5;
  else if(ph==='completed') pos=6;
  else if(sess._t==='running') pos=3;
  nodes[3].lbl='T'+(sess.turn_count||0);
  for(var i=0;i<nodes.length;i++){
    if(i<pos) nodes[i].state='done';
    else if(i===pos) nodes[i].state='cur';
    else nodes[i].state='fut';
  }
  return nodes;
}

function renderTrack(nodes){
  var h='<div class="lc">';
  for(var i=0;i<nodes.length;i++){
    var n=nodes[i],last=i===nodes.length-1;
    h+='<div class="lc-node">';
    if(n.isPill){
      var pc=n.state==='cur'?'lc-pill pill-cur':n.state==='done'?'lc-pill pill-done':'lc-pill pill-fut';
      h+='<span class="'+pc+'">'+x(n.lbl)+'</span>';
    } else {
      var dc=n.state==='cur'?'lc-dot dot-cur':n.state==='done'?'lc-dot dot-done':'lc-dot dot-fut';
      h+='<span class="'+dc+'"></span>';
    }
    if(!last){
      var lc=n.state==='done'?'lc-line line-done':'lc-line line-fut';
      h+='<span class="'+lc+'"></span>';
    }
    h+='</div>';
  }
  return h+'</div>';
}

function detailRows(sess){
  var rows=[];
  function add(k,v){
    if(v) rows.push('<div class="detail-row"><span class="detail-key">'+x(k)+'</span><span class="detail-val" title="'+x(v)+'">'+x(v)+'</span></div>');
  }
  add('workspace',sess.workspace);
  add('log',sess.log_path);
  if(sess.logs&&sess.logs.codex_session_logs){
    sess.logs.codex_session_logs.forEach(function(l){add(l.label||'log',l.path);});
  }
  if(sess.setup){
    var s=sess.setup;
    add('setup',(s.status||'')+(s.stage?' '+s.stage:'')+(s.hook?' '+s.hook:''));
    add('setup err',s.error);
    add('setup log',s.log_path);
    add('failed ws',s.failed_workspace);
  }
  if(sess.error) add('error',sess.error);
  if(!rows.length) return '';
  return '<div class="detail-box">'+rows.join('')+'</div>';
}

function looksStructuredPayload(v){
  var s=String(v||'').trim();
  if(!s) return false;
  var first=s.charAt(0),last=s.charAt(s.length-1);
  if((first==='{'&&last==='}')||(first==='['&&last===']')){
    try{JSON.parse(s);return true;}catch(e){}
  }
  return s.indexOf('"jsonrpc"')!==-1||(s.indexOf('"method"')!==-1&&s.indexOf('"params"')!==-1);
}

function latestAgentText(sess){
  var msgs=sess.recent_agent_messages||[];
  for(var i=msgs.length-1;i>=0;i--){
    var m=msgs[i];
    if(m&&m.text) return m.text;
  }
  return '';
}

function sessionSummary(sess){
  var text=latestAgentText(sess);
  if(text) return text;
  var msg=sess.last_message||'';
  if(msg&&!looksStructuredPayload(msg)) return msg;
  return sess.last_event||'';
}

function renderCard(sess,open){
  var id=sess.issue_id||sess.issue_identifier||'';
  var key=x(sess.issue_identifier||sess.issue_id);
  var href=sess.issue_url?x(sess.issue_url):'';
  var title=x(sess.title||'(no title)');
  var meta='',msg='';

  if(sess._t==='running'){
    var tk=sess.tokens?fmtTok(sess.tokens.total_tokens):'0';
    var max=maxTurnsFor(sess);
    var turn='turn '+(sess.turn_count||0)+(max?'/'+max:'');
    meta=turn+' · '+tk+' tok · '+runtimeFor(sess);
    msg=sessionSummary(sess);
  } else {
    var stage=sess.stage||'';
    var hook=sess.hook||'';
    meta='preparing'+(stage?' · '+x(stage):'')+(hook?' ('+x(hook)+')':'');
    msg=sess.status==='failed'?'error: '+x(sess.error||'failed'):x(sess.status||'running');
  }

  var streamHtml='';
  if(open){
    var msgs=sess.recent_agent_messages||[];
    var rows='';
    if(msgs.length){
      var rev=msgs.slice().reverse();
      for(var i=0;i<rev.length;i++){
        var m=rev[i];
        rows+='<div class="act-row"><span class="act-ts">'+x(fmtTime(m.at))+'</span><span class="act-msg">'+x(m.text)+'</span></div>';
      }
    } else if(msg){
      rows='<div class="act-row"><span class="act-ts">'+x(fmtTime(new Date().toISOString()))+'</span><span class="act-msg">'+x(msg)+'</span></div>';
    } else {
      rows='<div class="empty">No activity yet</div>';
    }
    streamHtml=detailRows(sess)
      +'<div class="act-hdr" onclick="event.stopPropagation()">'
      +'<div class="act-lbl">Agent activity · newest first · '+msgs.length+' events</div>'
      +'<div class="act-stream scroll">'+rows+'</div>'
      +'</div>';
  }

  var nodes=lcNodes(sess);
  return '<div class="card" data-id="'+x(id)+'" onclick="toggle(this)">'
    +'<div class="card-hdr">'
    +(href?'<a class="lnk iss-key" href="'+href+'" target="_blank" onclick="event.stopPropagation()">'+key+'</a>':'<span class="iss-key">'+key+'</span>')
    +'<div class="card-right" onclick="event.stopPropagation()">'+prHtml(sess.pull_request,'no PR yet')+'</div>'
    +'</div>'
    +'<div class="card-title">'+title+'</div>'
    +renderTrack(nodes)
    +'<div class="card-meta">'+meta+'</div>'
    +(open?streamHtml:'<div class="card-msg">'+x(msg)+'</div>')
    +'</div>';
}

function toggle(el){
  var id=el.getAttribute('data-id');
  expandedId=expandedId===id?null:id;
  renderSessions(lastState);
}

function sessions(state){
  var out=[],seen={};
  (state.running||[]).forEach(function(r){r._t='running';out.push(r);seen[r.issue_id]=true;});
  (state.setup||[]).forEach(function(s){if(!seen[s.issue_id]){s._t='setup';out.push(s);}});
  return out;
}

function renderSessions(state){
  var ss=sessions(state);
  document.getElementById('sess-cnt').textContent='· '+ss.length;
  var h='';
  ss.forEach(function(s){h+=renderCard(s,expandedId===(s.issue_id||s.issue_identifier||''));});
  if(!h) h='<div class="empty">No active sessions</div>';
  document.getElementById('grid').innerHTML=h;
}

function renderQueued(state){
  var rows=state.ready||[];
  document.getElementById('rq-cnt').textContent='· '+rows.length;
  if(!rows.length){document.getElementById('rq-rows').innerHTML='<div class="empty">Queue is empty</div>';return;}
  var h='';
  rows.forEach(function(q){
    var key=x(q.issue_identifier),href=q.issue_url?x(q.issue_url):'',title=x(q.title||'');
    var wait=q.wait_seconds!=null?fmtSecs(q.wait_seconds):(q.queued_since?fmtRelTime(q.queued_since):(q.created_at?fmtRelTime(q.created_at):'—'));
    h+='<div class="q-row">'
      +'<span class="prio" style="background:'+prioColor(q.priority)+'"></span>'
      +'<div class="q-body">'
      +'<div class="q-top">'
      +(href?'<a class="lnk" href="'+href+'" target="_blank" style="font-family:var(--font-mono);font-size:11px;">'+key+'</a>':'<span style="font-family:var(--font-mono);font-size:11px;">'+key+'</span>')
      +'<span class="q-wait">'+x(wait)+'</span>'
      +'</div>'
      +'<div class="q-title">'+title+'</div>'
      +'</div></div>';
  });
  document.getElementById('rq-rows').innerHTML=h;
}

function renderRetry(state){
  var rows=state.retrying||[];
  document.getElementById('rr-cnt').textContent='· '+rows.length;
  if(!rows.length){document.getElementById('rr-rows').innerHTML='<div class="empty">No retries</div>';return;}
  var h='';
  rows.forEach(function(r){
    var key=x(r.issue_identifier),href=r.issue_url?x(r.issue_url):'';
    var title=x(r.title||'');
    var next=r.due_at?fmtUntil(r.due_at):'—';
    var err=x(r.error||'');
    h+='<div class="r-row">'
      +'<div style="display:flex;justify-content:space-between;gap:8px;">'
      +(href?'<a class="lnk r-key" href="'+href+'" target="_blank">'+key+'</a>':'<span class="r-key">'+key+'</span>')
      +'<span class="r-att">attempt '+r.attempt+'</span>'
      +'</div>'
      +'<div class="r-title">'+title+'</div>'
      +'<div class="r-meta">'+err+(err?' · ':'')+'retry '+next+'</div>'
      +'<div style="margin-top:6px;">'+prHtml(r.pull_request,'no PR yet')+'</div>'
      +'</div>';
  });
  document.getElementById('rr-rows').innerHTML=h;
}

function renderDone(state){
  var rows=state.completed||[];
  document.getElementById('rd-cnt').textContent='· '+rows.length;
  if(!rows.length){document.getElementById('rd-rows').innerHTML='<div class="empty">None yet</div>';return;}
  var h='';
  rows.slice().reverse().forEach(function(d){
    var key=x(d.issue_identifier),href=d.issue_url?x(d.issue_url):'',title=x(d.title||'');
    var max=maxTurnsFor(d);
    var turn='turns '+(d.turn_count||0)+(max?'/'+max:'');
    var tok=d.tokens?fmtTok(d.tokens.total_tokens)+' tok':'0 tok';
    var meta=turn+' · '+tok+' · '+runtimeFor(d)+' · '+x(fmtTime(d.completed_at));
    h+='<div class="d-row">'
      +'<div class="d-top">'
      +(href?'<a class="lnk d-key" href="'+href+'" target="_blank">'+key+'</a>':'<span class="d-key">'+key+'</span>')
      +prHtml(d.pull_request,'no PR yet')
      +'</div>'
      +'<div class="d-title">'+title+'</div>'
      +'<div class="d-meta">'+meta+'</div>'
      +'</div>';
  });
  document.getElementById('rd-rows').innerHTML=h;
}

function renderKpi(state){
  var c=state.counts||{},t=state.agent_totals||{};
  document.getElementById('kv-q').textContent=val(c.ready,'0');
  document.getElementById('kv-p').textContent=val(c.setup,'0');
  document.getElementById('kv-r').textContent=val(c.running,'0');
  document.getElementById('kv-ph').textContent=val(c.post_run_hooks,'0');
  document.getElementById('kv-rt').textContent=val(c.retrying,'0');
  document.getElementById('kv-d').textContent=val(c.completed,'0');
  document.getElementById('kv-t').textContent=fmtTok(t.total_tokens||0);
}

function renderAll(state){
  if(!state) return;
  lastState=state;
  renderKpi(state);
  renderSessions(state);
  renderQueued(state);
  renderRetry(state);
  renderDone(state);
  document.getElementById('status-label').textContent='running';
  document.getElementById('status-ts').textContent=state.generated_at?new Date(state.generated_at).toLocaleString():'';
}

async function load(){
  try{
    var r=await fetch('/api/v1/state');
    renderAll(await r.json());
  }catch(e){
    document.getElementById('status-label').textContent='offline';
    document.getElementById('status-ts').textContent=String(e);
  }
}

async function doRefresh(){
  await fetch('/api/v1/refresh',{method:'POST'});
  await load();
}

load();
setInterval(load,5000);
</script>`
