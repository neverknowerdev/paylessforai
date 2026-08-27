package statserver

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) publicMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })
	m.HandleFunc("/readyz", s.ready)
	m.HandleFunc("/v1/models/search", s.search)
	m.HandleFunc("/v1/models/resolve", s.resolve)
	m.HandleFunc("/v1/models/", s.modelDetail)
	m.HandleFunc("/v1/models", s.models)
	m.HandleFunc("/v1/sources/status", s.sourceStatus)
	m.HandleFunc("/v1/statistics", s.statistics)
	m.HandleFunc("/v1/capability-profiles", s.publicProfiles)
	m.HandleFunc("/v1/telemetry", s.telemetry)
	m.HandleFunc("/", s.publicIndex)
	return m
}
func (s *Server) publicIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8"><title>PayLessForAI Stat Server</title><style>body{font:15px system-ui;margin:40px;color:#16202a;background:#f5f7fa}main{max-width:1100px;margin:auto}h1{margin-bottom:4px}.muted{color:#64748b}.card{background:white;border-radius:14px;padding:20px;margin:18px 0;box-shadow:0 2px 12px #122b3b12}input{padding:10px;border:1px solid #cbd5e1;border-radius:8px;width:60%}button{padding:10px 15px;border:0;border-radius:8px;background:#2563eb;color:white}table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:9px;border-bottom:1px solid #e2e8f0;font-size:13px}.pill{padding:3px 7px;border-radius:12px;background:#e0f2fe}</style></head><body><main><h1>PayLessForAI Stat Server</h1><div class="muted">Public model catalog and runtime intelligence</div><div class="card"><input id="q" placeholder="Search models, e.g. deepseek flash"><button onclick="load()">Search</button></div><div class="card"><div id="status">Loading catalog…</div><table><thead><tr><th>Model</th><th>Creator</th><th>Offerings</th><th>Benchmarks</th><th>Updated</th></tr></thead><tbody id="rows"></tbody></table></div></main><script>async function load(){let q=document.getElementById('q').value;let u=q?'/v1/models/search?q='+encodeURIComponent(q):'/v1/models?limit=100';let d=await fetch(u).then(r=>r.json());let a=d.data||d.results||[];document.getElementById('status').textContent=(d.total||a.length)+' models';document.getElementById('rows').innerHTML=a.map(x=>'<tr><td><b>'+esc(x.display_name||x.name)+'</b><br><span class="muted">'+esc(x.canonical_slug||x.id)+'</span></td><td>'+esc(x.creator||'—')+'</td><td><span class="pill">'+(x.offering_count||0)+'</span></td><td>'+((x.benchmark_count||0))+'</td><td>'+esc(x.updated_at||'')+'</td></tr>').join('')}function esc(x){return String(x||'').replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]))}load()</script></body></html>`)
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	var n int
	_ = s.db.QueryRowContext(r.Context(), `SELECT count(*) FROM models`).Scan(&n)
	if n == 0 {
		http.Error(w, "catalog unavailable", 503)
		return
	}
	w.WriteHeader(200)
	_, _ = w.Write([]byte("ready"))
}
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	var total int
	_ = s.db.QueryRowContext(r.Context(), `SELECT count(*) FROM models`).Scan(&total)
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.id,m.canonical_slug,m.display_name,m.creator,m.family,m.revision,m.description,m.context_length,m.updated_at,(SELECT count(*) FROM provider_offerings o WHERE o.model_id=m.id AND o.status='active'),(SELECT count(*) FROM benchmark_results b WHERE b.model_id=m.id) FROM models m ORDER BY m.display_name LIMIT $1`, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var slug, name, creator, fam, rev, desc string
		var ctx sql.NullInt64
		var updated time.Time
		var offers, bench int
		_ = rows.Scan(&id, &slug, &name, &creator, &fam, &rev, &desc, &ctx, &updated, &offers, &bench)
		out = append(out, map[string]any{"id": id, "canonical_slug": slug, "display_name": name, "creator": creator, "family": fam, "revision": rev, "description": desc, "context_length": nullInt(ctx), "offering_count": offers, "benchmark_count": bench, "updated_at": updated})
	}
	jsonResponse(w, map[string]any{"object": "list", "data": out, "total": total})
}
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := Normalize(r.URL.Query().Get("q"))
	if q == "" {
		s.models(w, r)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.id,m.canonical_slug,m.display_name,m.creator,m.updated_at,(SELECT count(*) FROM provider_offerings o WHERE o.model_id=m.id),(SELECT count(*) FROM benchmark_results b WHERE b.model_id=m.id),CASE WHEN m.normalized_name=$1 THEN 0 WHEN m.normalized_name LIKE $1||'%' THEN 1 ELSE 2 END FROM models m LEFT JOIN model_aliases a ON a.model_id=m.id WHERE m.normalized_name=$1 OR m.normalized_name LIKE $1||'%' OR m.normalized_name ILIKE '%'||$1||'%' OR a.normalized_alias ILIKE '%'||$1||'%' ORDER BY 8,m.display_name LIMIT 100`, q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var slug, name, creator string
		var updated time.Time
		var offers, bench, rank int
		_ = rows.Scan(&id, &slug, &name, &creator, &updated, &offers, &bench, &rank)
		out = append(out, map[string]any{"id": id, "canonical_slug": slug, "display_name": name, "creator": creator, "offering_count": offers, "benchmark_count": bench, "match_type": []string{"exact", "prefix", "contains"}[min(rank, 2)], "updated_at": updated})
	}
	jsonResponse(w, map[string]any{"results": out, "total": len(out)})
}
func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	q := Normalize(r.URL.Query().Get("name"))
	var id int64
	var slug, name, creator string
	err := s.db.QueryRowContext(r.Context(), `SELECT id,canonical_slug,display_name,creator FROM models WHERE normalized_name=$1 OR id IN(SELECT model_id FROM model_aliases WHERE normalized_alias=$1 AND (valid_until IS NULL OR valid_until>now())) ORDER BY id LIMIT 1`, q).Scan(&id, &slug, &name, &creator)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "model not found", 404)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]any{"id": id, "canonical_slug": slug, "display_name": name, "creator": creator, "resolved_from": q})
}
func (s *Server) sourceStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:9581")
	rows, _ := s.db.QueryContext(r.Context(), `SELECT key,display_name,last_attempt_at,last_success_at,last_error,record_count FROM sources ORDER BY key`)
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var k, d string
		var a, ok sql.NullTime
		var e sql.NullString
		var n int
		_ = rows.Scan(&k, &d, &a, &ok, &e, &n)
		out = append(out, map[string]any{"key": k, "display_name": d, "last_attempt_at": nullTime(a), "last_success_at": nullTime(ok), "last_error": e.String, "record_count": n})
	}
	jsonResponse(w, map[string]any{"data": out})
}
func (s *Server) modelDetail(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/v1/models/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(slug, "/")
	if len(parts) == 2 && parts[1] == "statistics" {
		s.modelStatistics(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "scores" {
		s.modelScores(w, r, parts[0])
		return
	}
	var id int64
	var canonical, name, creator, fam, rev, desc string
	var contextLength sql.NullInt64
	var updated time.Time
	err := s.db.QueryRowContext(r.Context(), `SELECT id,canonical_slug,display_name,creator,family,revision,description,context_length,updated_at FROM models WHERE canonical_slug=$1 OR id::text=$1 LIMIT 1`, slug).Scan(&id, &canonical, &name, &creator, &fam, &rev, &desc, &contextLength, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	offers, _ := s.db.QueryContext(r.Context(), `SELECT provider,provider_model_id,variant,input_usd_per_million,output_usd_per_million,cache_read_usd_per_million,cache_write_usd_per_million,status,observed_at FROM provider_offerings WHERE model_id=$1 ORDER BY provider,provider_model_id`, id)
	defer offers.Close()
	or := []map[string]any{}
	for offers.Next() {
		var p, pm, v, status string
		var in, out, cr, cw sql.NullFloat64
		var at time.Time
		_ = offers.Scan(&p, &pm, &v, &in, &out, &cr, &cw, &status, &at)
		or = append(or, map[string]any{"provider": p, "provider_model_id": pm, "variant": v, "input_usd_per_million": nullFloat(in), "output_usd_per_million": nullFloat(out), "cache_read_usd_per_million": nullFloat(cr), "cache_write_usd_per_million": nullFloat(cw), "status": status, "observed_at": at})
	}
	bench, _ := s.db.QueryContext(r.Context(), `SELECT benchmark_name,version,metric,value,unit,verified,source_key,observed_at FROM benchmark_results WHERE model_id=$1 ORDER BY normalized_name,observed_at DESC`, id)
	defer bench.Close()
	br := []map[string]any{}
	for bench.Next() {
		var n, v, m, u, src string
		var value float64
		var verified bool
		var at time.Time
		_ = bench.Scan(&n, &v, &m, &value, &u, &verified, &src, &at)
		br = append(br, map[string]any{"name": n, "version": v, "metric": m, "value": value, "unit": u, "verified": verified, "source": src, "observed_at": at})
	}
	jsonResponse(w, map[string]any{"id": id, "canonical_slug": canonical, "display_name": name, "creator": creator, "family": fam, "revision": rev, "description": desc, "context_length": nullInt(contextLength), "updated_at": updated, "offerings": or, "benchmarks": br})
}
func (s *Server) publicProfiles(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.db.QueryContext(r.Context(), `SELECT p.key,p.display_name,p.description,v.version,v.minimum_coverage,v.missing_data_policy FROM capability_profiles p JOIN capability_profile_versions v ON v.profile_id=p.id AND v.state='published' WHERE p.public ORDER BY p.key`)
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var k, d, desc, policy string
		var v int
		var cov float64
		_ = rows.Scan(&k, &d, &desc, &v, &cov, &policy)
		out = append(out, map[string]any{"key": k, "display_name": d, "description": desc, "version": v, "minimum_coverage": cov, "missing_data_policy": policy})
	}
	jsonResponse(w, map[string]any{"data": out})
}
