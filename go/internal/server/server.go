package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/openai/symphony/go/internal/orchestrator"
)

type Server struct{ orch *orchestrator.Orchestrator }

func New(o *orchestrator.Orchestrator) *Server { return &Server{orch: o} }
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(dashboardHTML))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/v1/state" && r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(s.orch.Snapshot())
		return
	}
	if r.URL.Path == "/api/v1/refresh" && r.Method == http.MethodPost {
		go func() { _ = s.orch.Tick(r.Context()) }()
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") && r.Method == http.MethodGet {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		if issue, ok := s.orch.IssueSnapshot(id); ok {
			json.NewEncoder(w).Encode(issue)
			return
		}
		http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
}

const dashboardHTML = `<!doctype html>
<meta charset="utf-8">
<title>Symphony</title>
<style>
body{font:14px system-ui,sans-serif;margin:2rem;background:#111;color:#eee}pre{background:#1b1b1b;padding:1rem;border-radius:8px;overflow:auto}.ok{color:#7ee787}.bad{color:#ff7b72}button{padding:.5rem .8rem}
</style>
<h1>Symphony</h1>
<p id="status">loading…</p>
<button onclick="refresh()">Refresh now</button>
<pre id="state"></pre>
<script>
async function load(){
  try{
    const r=await fetch('/api/v1/state');
    const j=await r.json();
    status.innerHTML='<span class="ok">running</span> '+new Date(j.generated_at).toLocaleString();
    state.textContent=JSON.stringify(j,null,2);
  }catch(e){status.innerHTML='<span class="bad">offline</span> '+e;}
}
async function refresh(){await fetch('/api/v1/refresh',{method:'POST'}); await load();}
load(); setInterval(load,5000);
</script>`
