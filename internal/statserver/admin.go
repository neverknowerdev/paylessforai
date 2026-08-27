package statserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) adminMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.adminPage)
	m.HandleFunc("/admin/api/v1/session", s.adminSession)
	m.HandleFunc("/admin/api/v1/capability-profiles/create", s.adminProfileCreate)
	m.HandleFunc("/admin/api/v1/capability-profiles", s.adminProfiles)
	m.HandleFunc("/admin/api/v1/manual-signals", s.adminSignals)
	m.HandleFunc("/admin/api/v1/profile-versions/", s.adminVersion)
	return m
}
func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.authorized(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body style="font:16px system-ui;max-width:420px;margin:80px auto"><h1>Stat Server Admin</h1><p>Private administration console</p><form method="post" action="/admin/api/v1/session"><input name="email" type="email" placeholder="Email" required style="display:block;padding:10px;width:100%;margin:8px 0"><input name="password" type="password" placeholder="Password" required style="display:block;padding:10px;width:100%;margin:8px 0"><button style="padding:10px 18px">Sign in</button></form></body></html>`)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><title>Stat Server Admin</title><style>body{font:15px system-ui;background:#f5f7fa;color:#17202a;margin:30px}main{max-width:1100px;margin:auto}.card{background:#fff;border-radius:14px;padding:20px;margin:15px 0;box-shadow:0 2px 12px #0001}.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:14px}.score{font-size:30px;color:#2563eb}table{width:100%;border-collapse:collapse}td,th{padding:8px;border-bottom:1px solid #ddd;text-align:left}input{padding:8px;margin:3px;border:1px solid #cbd5e1;border-radius:6px}button{padding:8px 12px;border:0;border-radius:6px;background:#2563eb;color:white}</style></head><body><main><h1>Stat Server Administration</h1><div class="card"><h2>Capability profiles</h2><form id="profile"><input name="key" placeholder="role key" required><input name="display_name" placeholder="Display name" required><input name="description" placeholder="Description"><button>Create role</button></form><div id="profiles">Loading…</div></div><div class="card"><h2>Source health</h2><div id="sources">Loading…</div></div><div class="card"><h2>Manual score signal</h2><form id="sig"><input name="key" placeholder="signal key" required><input name="display_name" placeholder="Display name" required><button>Create</button></form><div id="msg"></div></div></main><script>async function load(){let p=await fetch('/admin/api/v1/capability-profiles').then(r=>r.json());document.getElementById('profiles').innerHTML='<div class="grid">'+(p.data||[]).map(x=>'<div><h3>'+x.display_name+'</h3><div class="score">v'+x.version+'</div><p>'+x.description+'</p><small>'+x.state+' · coverage '+x.minimum_coverage+' · version '+x.version_id+'</small><form onsubmit="rule(event,'+x.version_id+')"><input name="selector" placeholder="benchmark name" required><input name="weight" type="number" value="1" min="1"><button>Add rule</button></form></div>').join('')+'</div>';let s=await fetch('http://127.0.0.1:9580/v1/sources/status').then(r=>r.json()).catch(()=>({data:[]}));document.getElementById('sources').innerHTML='<table><tr><th>Source</th><th>Records</th><th>Last success</th></tr>'+(s.data||[]).map(x=>'<tr><td>'+x.display_name+'</td><td>'+x.record_count+'</td><td>'+x.last_success_at+'</td></tr>').join('')}</script><script>async function rule(e,id){e.preventDefault();let f=new FormData(e.target);await fetch('/admin/api/v1/profile-versions/'+id+'/components',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({signal_type:'benchmark',selector:f.get('selector'),weight:Number(f.get('weight')),min_value:0,max_value:1,direction:'higher'})});load()}document.getElementById('profile').onsubmit=async e=>{e.preventDefault();let f=new FormData(e.target);await fetch('/admin/api/v1/capability-profiles/create',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(Object.fromEntries(f))});load()};document.getElementById('sig').onsubmit=async e=>{e.preventDefault();let f=new FormData(e.target);let r=await fetch('/admin/api/v1/manual-signals',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(Object.fromEntries(f))});document.getElementById('msg').textContent=r.ok?'Saved':'Error'};load()</script></body></html>`)
}
func (s *Server) adminSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	email, password := r.FormValue("email"), r.FormValue("password")
	var id int64
	var hash string
	if err := s.db.QueryRowContext(r.Context(), `SELECT id,password_hash FROM users WHERE email=$1 AND disabled_at IS NULL AND is_admin`, email).Scan(&id, &hash); err != nil || !checkPassword(hash, password) {
		http.Error(w, "invalid credentials", 401)
		return
	}
	tok := randomToken()
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO admin_sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '8 hours')`, hashString(tok), id)
	http.SetCookie(w, &http.Cookie{Name: "stat_admin", Value: tok, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 8 * 3600})
	http.Redirect(w, r, "/", 303)
}
func (s *Server) adminProfiles(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	rows, _ := s.db.QueryContext(r.Context(), `SELECT p.key,p.display_name,p.description,v.id,v.version,v.state,v.minimum_coverage,v.missing_data_policy FROM capability_profiles p JOIN capability_profile_versions v ON v.profile_id=p.id AND v.state='published' ORDER BY p.key`)
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var k, d, desc, state, pol string
		var id, version int64
		var cov float64
		_ = rows.Scan(&k, &d, &desc, &id, &version, &state, &cov, &pol)
		out = append(out, map[string]any{"key": k, "display_name": d, "description": desc, "version_id": id, "version": version, "state": state, "minimum_coverage": cov, "missing_data_policy": pol})
	}
	jsonResponse(w, map[string]any{"data": out})
}
func (s *Server) adminProfileCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var b struct{ Key, DisplayName, Description string }
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Key == "" || b.DisplayName == "" {
		http.Error(w, "invalid", 400)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback()
	var pid, vid int64
	if err = tx.QueryRowContext(r.Context(), `INSERT INTO capability_profiles(key,display_name,description) VALUES($1,$2,$3) RETURNING id`, b.Key, b.DisplayName, b.Description).Scan(&pid); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = tx.QueryRowContext(r.Context(), `INSERT INTO capability_profile_versions(profile_id,version,state) VALUES($1,1,'draft') RETURNING id`, pid).Scan(&vid); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]any{"profile_id": pid, "version_id": vid, "state": "draft"})
}
func (s *Server) adminSignals(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if r.Method == "POST" {
		var b struct{ Key, DisplayName, Description string }
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Key == "" {
			http.Error(w, "invalid", 400)
			return
		}
		if _, err := s.db.ExecContext(r.Context(), `INSERT INTO manual_score_signals(key,display_name,description) VALUES($1,$2,$3)`, b.Key, b.DisplayName, b.Description); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonResponse(w, map[string]any{"created": true})
		return
	}
	rows, _ := s.db.QueryContext(r.Context(), `SELECT id,key,display_name,description FROM manual_score_signals ORDER BY key`)
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var k, d, desc string
		_ = rows.Scan(&id, &k, &d, &desc)
		out = append(out, map[string]any{"id": id, "key": k, "display_name": d, "description": desc})
	}
	jsonResponse(w, map[string]any{"data": out})
}
func (s *Server) adminVersion(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/api/v1/profile-versions/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	vid, _ := strconv.ParseInt(parts[0], 10, 64)
	if len(parts) > 1 && parts[1] == "components" && r.Method == "POST" {
		var b struct {
			SignalType, Selector, Direction string
			Weight                          int
			Required                        bool
			MinValue, MaxValue              float64
			Rationale                       string
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Weight <= 0 || b.SignalType == "" {
			http.Error(w, "invalid", 400)
			return
		}
		if b.MaxValue == 0 && b.MinValue == 0 {
			b.MaxValue = 1
		}
		if _, err := s.db.ExecContext(r.Context(), `INSERT INTO capability_profile_components(profile_version_id,signal_type,benchmark_selector,weight,required,min_value,max_value,direction,rationale) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9)`, vid, b.SignalType, b.Selector, b.Weight, b.Required, b.MinValue, b.MaxValue, b.Direction, b.Rationale); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = s.computeScores(r.Context())
		jsonResponse(w, map[string]any{"created": true})
		return
	}
	if len(parts) > 1 && parts[1] == "publish" && r.Method == "POST" {
		_, err := s.db.ExecContext(r.Context(), `UPDATE capability_profile_versions SET state='superseded' WHERE profile_id=(SELECT profile_id FROM capability_profile_versions WHERE id=$1) AND state='published'; UPDATE capability_profile_versions SET state='published',published_at=now() WHERE id=$1`, vid)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = s.computeScores(r.Context())
		jsonResponse(w, map[string]any{"published": true, "version_id": vid})
		return
	}
	http.Error(w, "profile version endpoint ready", 501)
}
