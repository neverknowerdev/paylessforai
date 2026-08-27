package statserver

// The stat-server is deliberately self-contained: HTTP APIs, source refresh,
// score calculation, telemetry ingestion, and the admin application run in
// one Go process. The implementation favors explicit, inspectable SQL and
// deterministic normalization so a catalog snapshot can be reproduced.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "github.com/lib/pq"
)

//go:embed migrations/001_init.sql
var migrationSQL string

type Config struct {
	ListenAddr        string
	AdminListenAddr   string
	DatabaseURL       string
	RefreshInterval   time.Duration
	ArtificialKey     string
	OpenRouterKey     string
	HuggingFaceToken  string
	SurplusKey        string
	BootstrapEmail    string
	BootstrapPassword string
}

func ConfigFromEnv() Config {
	interval := 1 * time.Hour
	if v := os.Getenv("STAT_SERVER_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}
	return Config{
		ListenAddr:      getenvDefault("STAT_SERVER_LISTEN", "127.0.0.1:9580"),
		AdminListenAddr: getenvDefault("STAT_SERVER_ADMIN_LISTEN", "127.0.0.1:9581"),
		DatabaseURL:     os.Getenv("STAT_SERVER_DATABASE_URL"), RefreshInterval: interval,
		ArtificialKey: os.Getenv("ARTIFICIAL_ANALYSIS_API_KEY"), OpenRouterKey: os.Getenv("OPENROUTER_API_KEY"),
		HuggingFaceToken: os.Getenv("HUGGINGFACE_TOKEN"), SurplusKey: os.Getenv("SURPLUS_API_KEY"),
		BootstrapEmail: os.Getenv("STAT_SERVER_BOOTSTRAP_ADMIN_EMAIL"), BootstrapPassword: os.Getenv("STAT_SERVER_BOOTSTRAP_ADMIN_PASSWORD"),
	}
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("STAT_SERVER_DATABASE_URL is required")
	}
	if c.ListenAddr == "" || c.AdminListenAddr == "" {
		return errors.New("listen addresses are required")
	}
	if c.RefreshInterval <= 0 {
		return errors.New("refresh interval must be positive")
	}
	return nil
}

type Server struct {
	cfg            Config
	db             *sql.DB
	public, admin  *http.Server
	mu             sync.RWMutex
	refreshRunning bool
}

func New(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(migrationSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	s := &Server{cfg: cfg, db: db}
	if cfg.BootstrapEmail != "" && cfg.BootstrapPassword != "" {
		if err := s.bootstrapAdmin(); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Server) Close() error {
	if s.public != nil {
		_ = s.public.Close()
	}
	if s.admin != nil {
		_ = s.admin.Close()
	}
	return s.db.Close()
}

func (s *Server) Run(ctx context.Context) error {
	s.public = &http.Server{Addr: s.cfg.ListenAddr, Handler: s.publicMux(), ReadHeaderTimeout: 10 * time.Second}
	s.admin = &http.Server{Addr: s.cfg.AdminListenAddr, Handler: s.adminMux(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 2)
	go func() {
		log.Printf("public stat-server listening on %s", s.cfg.ListenAddr)
		errCh <- s.public.ListenAndServe()
	}()
	go func() {
		log.Printf("admin stat-server listening on %s", s.cfg.AdminListenAddr)
		errCh <- s.admin.ListenAndServe()
	}()
	go s.scheduler(ctx)
	// Initial refresh is synchronous so readiness means useful data is present.
	if err := s.Refresh(ctx); err != nil {
		log.Printf("initial refresh degraded: %v", err)
	}
	select {
	case <-ctx.Done():
		_ = s.public.Shutdown(context.Background())
		_ = s.admin.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) scheduler(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Refresh(ctx); err != nil {
				log.Printf("scheduled refresh degraded: %v", err)
			}
		}
	}
}

func (s *Server) Refresh(ctx context.Context) error {
	s.mu.Lock()
	if s.refreshRunning {
		s.mu.Unlock()
		return nil
	}
	s.refreshRunning = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.refreshRunning = false; s.mu.Unlock() }()
	if _, err := s.db.ExecContext(ctx, `SELECT pg_advisory_lock(873491)`); err != nil {
		return err
	}
	defer s.db.ExecContext(context.Background(), `SELECT pg_advisory_unlock(873491)`)
	var errs []string
	connectors := []connector{
		{name: "artificial_analysis", display: "Artificial Analysis", url: "https://artificialanalysis.ai/api/v2/language/models/free", key: s.cfg.ArtificialKey, run: s.fetchAA},
		{name: "openrouter", display: "OpenRouter", url: "https://openrouter.ai/api/v1/models", key: s.cfg.OpenRouterKey, run: s.fetchOpenRouter},
		{name: "huggingface", display: "Hugging Face", url: "https://huggingface.co/api/models?limit=200&sort=downloads&direction=-1", key: s.cfg.HuggingFaceToken, run: s.fetchHF},
		{name: "surplus", display: "Surplus Intelligence", url: "https://api.surplusintelligence.ai/v1/models", key: s.cfg.SurplusKey, run: s.fetchSurplus},
	}
	for _, c := range connectors {
		if c.key == "" && c.name != "huggingface" && c.name != "surplus" {
			continue
		}
		if err := s.runConnector(ctx, c); err != nil {
			errs = append(errs, c.name+": "+err.Error())
		}
	}
	if err := s.computeScores(ctx); err != nil {
		errs = append(errs, "scores: "+err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

type connector struct {
	name, display, url, key string
	run                     func(context.Context, string) ([]normalizedRecord, error)
}
type normalizedRecord struct {
	SourceID, Name, Creator, Family, Revision, Description string
	Context                                                int64
	ProviderModel                                          string
	Input, Output, CacheRead, CacheWrite                   *float64
	Benchmarks                                             map[string]float64
	Metadata                                               map[string]any
}

func (s *Server) runConnector(ctx context.Context, c connector) error {
	now := time.Now().UTC()
	sid := int64(0)
	err := s.db.QueryRowContext(ctx, `INSERT INTO sources(key,display_name,base_url,last_attempt_at,updated_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(key) DO UPDATE SET last_attempt_at=EXCLUDED.last_attempt_at,base_url=EXCLUDED.base_url,updated_at=now() RETURNING id`, c.name, c.display, c.url, now).Scan(&sid)
	if err != nil {
		return err
	}
	recs, err := c.run(ctx, c.key)
	if err != nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE sources SET last_error=$1,updated_at=now() WHERE id=$2`, err.Error(), sid)
		return err
	}
	payload, _ := json.Marshal(recs)
	sum := sha256.Sum256(payload)
	_, err = s.db.ExecContext(ctx, `INSERT INTO source_snapshots(source_id,content_hash,payload) VALUES($1,$2,$3)`, sid, hex.EncodeToString(sum[:]), payload)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if err := s.upsertRecord(ctx, c.name, r); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE sources SET last_success_at=$1,last_error=NULL,record_count=$2,updated_at=now() WHERE id=$3`, now, len(recs), sid)
	return err
}

func (s *Server) upsertRecord(ctx context.Context, source string, r normalizedRecord) error {
	identityName := r.Name
	if r.Creator != "" {
		prefix := strings.ToLower(strings.TrimSpace(r.Creator)) + ":"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(identityName)), prefix) {
			identityName = strings.TrimSpace(identityName[len(prefix):])
		}
	}
	norm := Normalize(identityName)
	slug := canonicalSlug(r.Creator, r.Name, r.Revision)
	var mid int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM models WHERE normalized_name=$1 AND (lower(creator)=lower($2) OR creator='' OR $2='') ORDER BY id LIMIT 1`, norm, r.Creator).Scan(&mid)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRowContext(ctx, `INSERT INTO models(canonical_slug,display_name,normalized_name,creator,family,revision,description,context_length,source_key,source_id,metadata,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,$10,$11,now()) ON CONFLICT(source_key,source_id) DO UPDATE SET display_name=EXCLUDED.display_name,normalized_name=EXCLUDED.normalized_name,creator=EXCLUDED.creator,family=EXCLUDED.family,revision=EXCLUDED.revision,description=EXCLUDED.description,context_length=EXCLUDED.context_length,metadata=EXCLUDED.metadata,updated_at=now() RETURNING id`, slug, r.Name, norm, r.Creator, r.Family, r.Revision, r.Description, r.Context, source, r.SourceID, mustJSON(r.Metadata)).Scan(&mid)
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO model_aliases(model_id,alias,normalized_alias,source_key,evidence) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, mid, r.Name, norm, source, mustJSON(map[string]any{"source_id": r.SourceID}))
	if err != nil {
		return err
	}
	if r.ProviderModel != "" {
		_, err = s.db.ExecContext(ctx, `INSERT INTO provider_offerings(model_id,provider,provider_model_id,input_usd_per_million,output_usd_per_million,cache_read_usd_per_million,cache_write_usd_per_million,context_length,metadata,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,now()) ON CONFLICT(provider,provider_model_id) DO UPDATE SET model_id=EXCLUDED.model_id,input_usd_per_million=EXCLUDED.input_usd_per_million,output_usd_per_million=EXCLUDED.output_usd_per_million,cache_read_usd_per_million=EXCLUDED.cache_read_usd_per_million,cache_write_usd_per_million=EXCLUDED.cache_write_usd_per_million,context_length=EXCLUDED.context_length,metadata=EXCLUDED.metadata,observed_at=now(),status='active'`, mid, source, r.ProviderModel, r.Input, r.Output, r.CacheRead, r.CacheWrite, r.Context, mustJSON(r.Metadata))
		if err != nil {
			return err
		}
	}
	for name, value := range r.Benchmarks {
		_, err = s.db.ExecContext(ctx, `INSERT INTO benchmark_results(model_id,benchmark_name,normalized_name,value,unit,source_key,observed_at) VALUES($1,$2,$3,$4,'fraction',$5,now())`, mid, name, NormalizeBenchmark(name), value, source)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) fetchAA(ctx context.Context, key string) ([]normalizedRecord, error) {
	var env struct {
		Data []struct {
			ID, Name, Slug string
			ModelCreator   struct {
				Name string `json:"name"`
			} `json:"model_creator"`
			Evaluations map[string]float64 `json:"evaluations"`
			Pricing     struct {
				Input  float64 `json:"price_1m_input_tokens"`
				Output float64 `json:"price_1m_output_tokens"`
			}
			Performance map[string]float64 `json:"performance"`
		} `json:"data"`
	}
	if err := getJSON(ctx, "https://artificialanalysis.ai/api/v2/language/models/free", key, "x-api-key", &env); err != nil {
		return nil, err
	}
	out := make([]normalizedRecord, 0, len(env.Data))
	for _, m := range env.Data {
		in, outp := m.Pricing.Input, m.Pricing.Output
		out = append(out, normalizedRecord{SourceID: m.ID, Name: m.Name, Creator: m.ModelCreator.Name, ProviderModel: m.Slug, Input: &in, Output: &outp, Benchmarks: m.Evaluations, Metadata: map[string]any{"slug": m.Slug, "performance": m.Performance}})
	}
	return out, nil
}

func (s *Server) fetchOpenRouter(ctx context.Context, key string) ([]normalizedRecord, error) {
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := getJSON(ctx, "https://openrouter.ai/api/v1/models?output_modalities=all", key, "Authorization", &env); err != nil {
		return nil, err
	}
	out := make([]normalizedRecord, 0, len(env.Data))
	for _, m := range env.Data {
		id, _ := m["id"].(string)
		name, _ := m["name"].(string)
		if name == "" {
			name = id
		}
		creator := strings.Split(id, "/")
		cr := ""
		if len(creator) > 1 {
			cr = creator[0]
		}
		p, _ := m["pricing"].(map[string]any)
		in := number(p["prompt"]) * 1e6
		outp := number(p["completion"]) * 1e6
		crd := numberPtr(p["cache_read"])
		cwr := numberPtr(p["cache_write"])
		if crd != nil {
			v := *crd * 1e6
			crd = &v
		}
		if cwr != nil {
			v := *cwr * 1e6
			cwr = &v
		}
		ctxlen := int64(number(m["context_length"]))
		out = append(out, normalizedRecord{SourceID: id, Name: name, Creator: cr, ProviderModel: id, Input: &in, Output: &outp, CacheRead: crd, CacheWrite: cwr, Context: ctxlen, Metadata: m})
	}
	return out, nil
}

func (s *Server) fetchHF(ctx context.Context, key string) ([]normalizedRecord, error) {
	var data []map[string]any
	if err := getJSON(ctx, "https://huggingface.co/api/models?limit=200&sort=downloads&direction=-1", key, "Authorization", &data); err != nil {
		return nil, err
	}
	out := make([]normalizedRecord, 0, len(data))
	for _, m := range data {
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		name := id
		if v, ok := m["modelId"].(string); ok && v != "" {
			name = v
		}
		out = append(out, normalizedRecord{SourceID: id, Name: name, Creator: strings.Split(id, "/")[0], Metadata: m})
	}
	return out, nil
}

func (s *Server) fetchSurplus(ctx context.Context, key string) ([]normalizedRecord, error) {
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := getJSON(ctx, "https://api.surplusintelligence.ai/v1/models", key, "Authorization", &env); err != nil {
		return nil, err
	}
	out := make([]normalizedRecord, 0, len(env.Data))
	for _, m := range env.Data {
		id, _ := m["id"].(string)
		name, _ := m["name"].(string)
		if name == "" {
			name = id
		}
		in := number(m["input_price"])
		outp := number(m["output_price"])
		out = append(out, normalizedRecord{SourceID: id, Name: name, ProviderModel: id, Input: floatPtr(in), Output: floatPtr(outp), Metadata: m})
	}
	return out, nil
}

func getJSON(ctx context.Context, url, key, header string, dst any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if key != "" {
		if header == "Authorization" {
			req.Header.Set(header, "Bearer "+key)
		} else {
			req.Header.Set(header, key)
		}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %s", resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func (s *Server) publicMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("ok")) })
	m.HandleFunc("/readyz", s.ready)
	m.HandleFunc("/v1/models", s.models)
	m.HandleFunc("/v1/models/", s.modelDetail)
	m.HandleFunc("/v1/models/search", s.search)
	m.HandleFunc("/v1/models/resolve", s.resolve)
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
	w.Write([]byte("ready"))
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
	var canonical, name, creator, family, revision, description string
	var contextLength sql.NullInt64
	var updated time.Time
	err := s.db.QueryRowContext(r.Context(), `SELECT id,canonical_slug,display_name,creator,family,revision,description,context_length,updated_at FROM models WHERE canonical_slug=$1 OR id::text=$1 LIMIT 1`, slug).Scan(&id, &canonical, &name, &creator, &family, &revision, &description, &contextLength, &updated)
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
	offerRows := []map[string]any{}
	for offers.Next() {
		var p, pm, v, status string
		var in, out, cr, cw sql.NullFloat64
		var observed time.Time
		_ = offers.Scan(&p, &pm, &v, &in, &out, &cr, &cw, &status, &observed)
		offerRows = append(offerRows, map[string]any{"provider": p, "provider_model_id": pm, "variant": v, "input_usd_per_million": nullFloat(in), "output_usd_per_million": nullFloat(out), "cache_read_usd_per_million": nullFloat(cr), "cache_write_usd_per_million": nullFloat(cw), "status": status, "observed_at": observed})
	}
	bench, _ := s.db.QueryContext(r.Context(), `SELECT benchmark_name,version,metric,value,unit,verified,source_key,observed_at FROM benchmark_results WHERE model_id=$1 ORDER BY normalized_name,observed_at DESC`, id)
	defer bench.Close()
	benchRows := []map[string]any{}
	for bench.Next() {
		var n, v, m, u, src string
		var value float64
		var verified bool
		var observed time.Time
		_ = bench.Scan(&n, &v, &m, &value, &u, &verified, &src, &observed)
		benchRows = append(benchRows, map[string]any{"name": n, "version": v, "metric": m, "value": value, "unit": u, "verified": verified, "source": src, "observed_at": observed})
	}
	jsonResponse(w, map[string]any{"id": id, "canonical_slug": canonical, "display_name": name, "creator": creator, "family": family, "revision": revision, "description": description, "context_length": nullInt(contextLength), "updated_at": updated, "offerings": offerRows, "benchmarks": benchRows})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := Normalize(r.URL.Query().Get("q"))
	if q == "" {
		s.models(w, r)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.id,m.canonical_slug,m.display_name,m.creator,m.updated_at,(SELECT count(*) FROM provider_offerings o WHERE o.model_id=m.id),(SELECT count(*) FROM benchmark_results b WHERE b.model_id=m.id),CASE WHEN m.normalized_name=$1 THEN 0 WHEN m.normalized_name LIKE $1||'%' THEN 1 ELSE 2 END AS rank FROM models m LEFT JOIN model_aliases a ON a.model_id=m.id WHERE m.normalized_name=$1 OR m.normalized_name LIKE $1||'%' OR m.normalized_name ILIKE '%'||$1||'%' OR a.normalized_alias ILIKE '%'||$1||'%' ORDER BY rank,m.display_name LIMIT 100`, q)
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

func (s *Server) statistics(w http.ResponseWriter, r *http.Request) {
	model, provider := r.URL.Query().Get("model"), r.URL.Query().Get("provider")
	args := []any{}
	filters := []string{"1=1"}
	if model != "" {
		args = append(args, model)
		filters = append(filters, fmt.Sprintf("(model_name=$%d OR lower(model_name)=lower((SELECT display_name FROM models WHERE canonical_slug=$%d LIMIT 1)) OR model_id::text=$%d OR model_id=(SELECT id FROM models WHERE canonical_slug=$%d LIMIT 1))", len(args), len(args), len(args), len(args)))
	}
	if provider != "" {
		args = append(args, provider)
		filters = append(filters, fmt.Sprintf("provider=$%d", len(args)))
	}
	q := `SELECT count(*),coalesce(avg(total_ms),0),coalesce(percentile_cont(0.5) WITHIN GROUP(ORDER BY total_ms),0),coalesce(avg(ttft_ms),0),coalesce(avg(generation_ms),0),coalesce(avg(input_tokens),0),coalesce(avg(output_tokens),0),coalesce(sum(CASE WHEN cache_status='hit' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN cache_status='miss' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN cache_status='write' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN success THEN 1 ELSE 0 END),0) FROM request_observations WHERE ` + strings.Join(filters, " AND ")
	var count, hit, miss, write, success int
	var avg, p50, ttft, generation, inTok, outTok float64
	if err := s.db.QueryRowContext(r.Context(), q, args...).Scan(&count, &avg, &p50, &ttft, &generation, &inTok, &outTok, &hit, &miss, &write, &success); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]any{"sample_count": count, "success_count": success, "success_rate": ratio(success, count), "avg_total_ms": avg, "p50_total_ms": p50, "avg_ttft_ms": ttft, "avg_generation_ms": generation, "avg_input_tokens": inTok, "avg_output_tokens": outTok, "cache_hits": hit, "cache_misses": miss, "cache_writes": write, "cache_hit_rate": ratio(hit, hit+miss), "provider": provider, "model": model})
}

func (s *Server) modelStatistics(w http.ResponseWriter, r *http.Request, slug string) {
	r2 := r.Clone(r.Context())
	q := r2.URL.Query()
	q.Set("model", slug)
	r2.URL.RawQuery = q.Encode()
	s.statistics(w, r2)
}
func (s *Server) modelScores(w http.ResponseWriter, r *http.Request, slug string) {
	var id int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM models WHERE canonical_slug=$1 OR id::text=$1 LIMIT 1`, slug).Scan(&id); err != nil {
		http.NotFound(w, r)
		return
	}
	rows, _ := s.db.QueryContext(r.Context(), `SELECT p.key,p.display_name,v.version,c.score,c.base_score,c.coverage,c.calculated_at FROM capability_scores c JOIN capability_profile_versions v ON v.id=c.profile_version_id JOIN capability_profiles p ON p.id=v.profile_id WHERE c.model_id=$1 AND v.state='published' ORDER BY p.key`, id)
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var k, d string
		var v int
		var score, base, cov float64
		var at time.Time
		_ = rows.Scan(&k, &d, &v, &score, &base, &cov, &at)
		out = append(out, map[string]any{"key": k, "display_name": d, "version": v, "score": score, "base_score": base, "coverage": cov, "calculated_at": at})
	}
	jsonResponse(w, map[string]any{"data": out})
}

func (s *Server) publicProfiles(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.db.QueryContext(r.Context(), `SELECT p.key,p.display_name,p.description,v.version,v.minimum_coverage,v.missing_data_policy FROM capability_profiles p JOIN capability_profile_versions v ON v.profile_id=p.id AND v.state='published' WHERE p.public ORDER BY p.key`)
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var k, d, desc, policy string
		var v int
		var coverage float64
		_ = rows.Scan(&k, &d, &desc, &v, &coverage, &policy)
		out = append(out, map[string]any{"key": k, "display_name": d, "description": desc, "version": v, "minimum_coverage": coverage, "missing_data_policy": policy})
	}
	jsonResponse(w, map[string]any{"data": out})
}

func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(key) < 16 {
		http.Error(w, "installation credential required", 401)
		return
	}
	hash := hashString(key)
	var body struct {
		BatchID string `json:"batch_id"`
		Events  []struct {
			EventID                 string    `json:"event_id"`
			ModelName               string    `json:"model_name"`
			Provider                string    `json:"provider"`
			OccurredAt              time.Time `json:"occurred_at"`
			TotalMS                 int       `json:"total_ms"`
			TTFTMS                  int       `json:"ttft_ms"`
			GenerationMS            int       `json:"generation_ms"`
			InputTokens             int       `json:"input_tokens"`
			OutputTokens            int       `json:"output_tokens"`
			CachedReadTokens        int       `json:"cached_read_tokens"`
			CacheWriteTokens        int       `json:"cache_write_tokens"`
			CacheTTLSeconds         int       `json:"cache_ttl_seconds"`
			ObservedReuseAgeSeconds int       `json:"observed_reuse_age_seconds"`
			RetryCount              int       `json:"retry_count"`
			CacheStatus             string    `json:"cache_status"`
			Success                 bool      `json:"success"`
			CostUSD                 *float64  `json:"cost_usd"`
		} `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if body.BatchID == "" || len(body.Events) > 1000 {
		http.Error(w, "invalid batch", 400)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback()
	var iid int64
	_ = tx.QueryRowContext(r.Context(), `INSERT INTO telemetry_installations(installation_key_hash,last_seen_at) VALUES($1,now()) ON CONFLICT(installation_key_hash) DO UPDATE SET last_seen_at=now() RETURNING id`, hash).Scan(&iid)
	var bid int64
	err = tx.QueryRowContext(r.Context(), `INSERT INTO telemetry_batches(installation_id,batch_id,event_count) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING id`, iid, body.BatchID, len(body.Events)).Scan(&bid)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Commit()
		jsonResponse(w, map[string]any{"accepted": 0, "duplicate": true})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, e := range body.Events {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO request_observations(installation_id,event_id,model_name,provider,occurred_at,total_ms,ttft_ms,generation_ms,input_tokens,output_tokens,cached_read_tokens,cache_write_tokens,cache_status,cache_ttl_seconds,observed_reuse_age_seconds,success,retry_count,cost_usd) VALUES($1,$2,$3,$4,$5,NULLIF($6,0),NULLIF($7,0),NULLIF($8,0),NULLIF($9,0),NULLIF($10,0),NULLIF($11,0),NULLIF($12,0),$13,NULLIF($14,0),NULLIF($15,0),$16,$17,$18) ON CONFLICT DO NOTHING`, iid, e.EventID, e.ModelName, e.Provider, e.OccurredAt, e.TotalMS, e.TTFTMS, e.GenerationMS, e.InputTokens, e.OutputTokens, e.CachedReadTokens, e.CacheWriteTokens, e.CacheStatus, e.CacheTTLSeconds, e.ObservedReuseAgeSeconds, e.Success, e.RetryCount, e.CostUSD)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]any{"accepted": len(body.Events), "batch_id": body.BatchID})
}

func (s *Server) adminMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.adminPage)
	m.HandleFunc("/admin/api/v1/session", s.adminSession)
	m.HandleFunc("/admin/api/v1/capability-profiles", s.adminProfiles)
	m.HandleFunc("/admin/api/v1/capability-profiles/create", s.adminProfileCreate)
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
	if email == "" {
		var b struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		email, password = b.Email, b.Password
	}
	var id int64
	var hash string
	err := s.db.QueryRowContext(r.Context(), `SELECT id,password_hash FROM users WHERE email=$1 AND disabled_at IS NULL AND is_admin`, email).Scan(&id, &hash)
	if err != nil || !checkPassword(hash, password) {
		http.Error(w, "invalid credentials", 401)
		return
	}
	tok := randomToken()
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO admin_sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '8 hours')`, hashString(tok), id)
	http.SetCookie(w, &http.Cookie{Name: "stat_admin", Value: tok, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 8 * 3600})
	if r.Header.Get("Accept") == "application/json" {
		jsonResponse(w, map[string]any{"authenticated": true})
		return
	}
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
		var id, v int64
		var cov float64
		_ = rows.Scan(&k, &d, &desc, &id, &v, &state, &cov, &pol)
		out = append(out, map[string]any{"key": k, "display_name": d, "description": desc, "version_id": id, "version": v, "state": state, "minimum_coverage": cov, "missing_data_policy": pol})
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
	if err = tx.QueryRowContext(r.Context(), `INSERT INTO capability_profile_versions(profile_id,version,state,minimum_coverage,missing_data_policy) VALUES($1,1,'draft',0.5,'linear_penalty') RETURNING id`, pid).Scan(&vid); err != nil {
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
		_, err := s.db.ExecContext(r.Context(), `INSERT INTO manual_score_signals(key,display_name,description) VALUES($1,$2,$3)`, b.Key, b.DisplayName, b.Description)
		if err != nil {
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
		_, err := s.db.ExecContext(r.Context(), `INSERT INTO capability_profile_components(profile_version_id,signal_type,benchmark_selector,weight,required,min_value,max_value,direction,rationale) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9)`, vid, b.SignalType, b.Selector, b.Weight, b.Required, b.MinValue, b.MaxValue, b.Direction, b.Rationale)
		if err != nil {
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

func (s *Server) authorized(r *http.Request) bool {
	c, err := r.Cookie("stat_admin")
	if err != nil {
		return false
	}
	var ok bool
	_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM admin_sessions x JOIN users u ON u.id=x.user_id WHERE x.token_hash=$1 AND x.expires_at>now() AND u.is_admin AND u.disabled_at IS NULL)`, hashString(c.Value)).Scan(&ok)
	return ok
}

func (s *Server) bootstrapAdmin() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO users(email,password_hash,is_admin) VALUES($1,$2,true)`, s.cfg.BootstrapEmail, hashPassword(s.cfg.BootstrapPassword))
	return err
}

func (s *Server) computeScores(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,v.id FROM capability_profiles p JOIN capability_profile_versions v ON v.profile_id=p.id AND v.state='published'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, vid int64
		if err := rows.Scan(&pid, &vid); err != nil {
			return err
		}
		var comps []component
		cr, err := s.db.QueryContext(ctx, `SELECT signal_type,benchmark_selector,weight,required,min_value,max_value,direction FROM capability_profile_components WHERE profile_version_id=$1 ORDER BY display_order,id`, vid)
		if err != nil {
			return err
		}
		for cr.Next() {
			var c component
			var sel sql.NullString
			_ = cr.Scan(&c.Type, &sel, &c.Weight, &c.Required, &c.Min, &c.Max, &c.Direction)
			c.Selector = sel.String
			comps = append(comps, c)
		}
		cr.Close()
		if len(comps) == 0 {
			continue
		}
		mr, err := s.db.QueryContext(ctx, `SELECT id FROM models`)
		if err != nil {
			return err
		}
		for mr.Next() {
			var mid int64
			_ = mr.Scan(&mid)
			vals := []scorePart{}
			for _, c := range comps {
				if c.Type != "benchmark" {
					continue
				}
				var value float64
				err := s.db.QueryRowContext(ctx, `SELECT value FROM benchmark_results WHERE model_id=$1 AND normalized_name=$2 ORDER BY verified DESC,observed_at DESC,id DESC LIMIT 1`, mid, NormalizeBenchmark(c.Selector)).Scan(&value)
				if err != nil {
					continue
				}
				if c.Max <= c.Min {
					continue
				}
				norm := (value - c.Min) / (c.Max - c.Min)
				if c.Direction == "lower" {
					norm = 1 - norm
				}
				norm = math.Max(0, math.Min(1, norm))
				vals = append(vals, scorePart{Selector: c.Selector, Value: norm, Weight: c.Weight})
			}
			if len(vals) == 0 {
				continue
			}
			var sw, weighted float64
			for _, v := range vals {
				sw += float64(v.Weight)
				weighted += float64(v.Weight) * v.Value
			}
			base := weighted / sw
			score := 100 * base
			ex := mustJSON(map[string]any{"components": vals, "policy": "linear_penalty"})
			_, _ = s.db.ExecContext(ctx, `INSERT INTO capability_scores(profile_version_id,model_id,score,base_score,coverage,explanation) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(profile_version_id,model_id) DO UPDATE SET score=EXCLUDED.score,base_score=EXCLUDED.base_score,coverage=EXCLUDED.coverage,explanation=EXCLUDED.explanation,calculated_at=now()`, vid, mid, score, base, 1, ex)
		}
		mr.Close()
	}
	return nil
}

type component struct {
	Type, Selector, Direction string
	Weight                    int
	Required                  bool
	Min, Max                  float64
}
type scorePart struct {
	Selector string
	Value    float64
	Weight   int
}

func Normalize(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	lastSpace := false
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
		} else if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}
func NormalizeBenchmark(v string) string {
	return Normalize(strings.NewReplacer("_", " ", "-", " ").Replace(v))
}
func canonicalSlug(creator, name, revision string) string {
	v := strings.Trim(strings.Join([]string{creator, name, revision}, " "), " ")
	v = Normalize(v)
	return strings.ReplaceAll(v, " ", "-")
}
func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
func numberPtr(v any) *float64 {
	if v == nil {
		return nil
	}
	f := number(v)
	return &f
}
func floatPtr(v float64) *float64 { return &v }
func nullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
func nullTime(v sql.NullTime) any {
	if v.Valid {
		return v.Time
	}
	return nil
}
func nullFloat(v sql.NullFloat64) any {
	if v.Valid {
		return v.Float64
	}
	return nil
}
func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func parseLimit(v string) int {
	n, _ := strconv.Atoi(v)
	if n < 1 {
		n = 100
	}
	if n > 500 {
		n = 500
	}
	return n
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func randomToken() string               { b := make([]byte, 32); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func hashString(v string) string        { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func hashPassword(v string) string      { return "sha256$" + hashString(v) }
func checkPassword(hash, v string) bool { return subtleConstant(hash, hashPassword(v)) }
func subtleConstant(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}
func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
