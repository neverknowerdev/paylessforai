package statserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

func (s *Server) connectors() []connector {
	return []connector{
		{name: "artificial_analysis", display: "Artificial Analysis", url: "https://artificialanalysis.ai/api/v2/language/models/free", key: s.cfg.ArtificialKey, run: s.fetchAA},
		{name: "openrouter", display: "OpenRouter", url: "https://openrouter.ai/api/v1/models", key: s.cfg.OpenRouterKey, run: s.fetchOpenRouter},
		{name: "huggingface", display: "Hugging Face", url: "https://huggingface.co/api/models?limit=200&sort=downloads&direction=-1", key: s.cfg.HuggingFaceToken, run: s.fetchHF},
		{name: "surplus", display: "Surplus Intelligence", url: "https://api.surplusintelligence.ai/v1/models", key: s.cfg.SurplusKey, run: s.fetchSurplus},
	}
}

func (s *Server) runConnector(ctx context.Context, c connector) error {
	now := time.Now().UTC()
	var sid int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO sources(key,display_name,base_url,last_attempt_at,updated_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(key) DO UPDATE SET last_attempt_at=EXCLUDED.last_attempt_at,base_url=EXCLUDED.base_url,updated_at=now() RETURNING id`, c.name, c.display, c.url, now).Scan(&sid); err != nil {
		return err
	}
	recs, err := c.run(ctx, c.key)
	if err != nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE sources SET last_error=$1,updated_at=now() WHERE id=$2`, err.Error(), sid)
		return err
	}
	payload := mustJSON(recs)
	sum := sha256.Sum256(payload)
	if _, err = s.db.ExecContext(ctx, `INSERT INTO source_snapshots(source_id,content_hash,payload) VALUES($1,$2,$3)`, sid, hex.EncodeToString(sum[:]), payload); err != nil {
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
	identity := r.Name
	if r.Creator != "" {
		prefix := strings.ToLower(strings.TrimSpace(r.Creator)) + ":"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(identity)), prefix) {
			identity = strings.TrimSpace(identity[len(prefix):])
		}
	}
	norm := Normalize(identity)
	slug := canonicalSlug(r.Creator, r.Name, r.Revision)
	var mid int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM models WHERE normalized_name=$1 AND (lower(creator)=lower($2) OR creator='' OR $2='') ORDER BY id LIMIT 1`, norm, r.Creator).Scan(&mid)
	if err == sql.ErrNoRows {
		err = s.db.QueryRowContext(ctx, `INSERT INTO models(canonical_slug,display_name,normalized_name,creator,family,revision,description,context_length,source_key,source_id,metadata,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,$10,$11,now()) ON CONFLICT(source_key,source_id) DO UPDATE SET display_name=EXCLUDED.display_name,normalized_name=EXCLUDED.normalized_name,creator=EXCLUDED.creator,family=EXCLUDED.family,revision=EXCLUDED.revision,description=EXCLUDED.description,context_length=EXCLUDED.context_length,metadata=EXCLUDED.metadata,updated_at=now() RETURNING id`, slug, r.Name, norm, r.Creator, r.Family, r.Revision, r.Description, r.Context, source, r.SourceID, mustJSON(r.Metadata)).Scan(&mid)
	}
	if err != nil {
		return err
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO model_aliases(model_id,alias,normalized_alias,source_key,evidence) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, mid, r.Name, norm, source, mustJSON(map[string]any{"source_id": r.SourceID})); err != nil {
		return err
	}
	if r.ProviderModel != "" {
		if _, err = s.db.ExecContext(ctx, `INSERT INTO provider_offerings(model_id,provider,provider_model_id,input_usd_per_million,output_usd_per_million,cache_read_usd_per_million,cache_write_usd_per_million,context_length,metadata,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,now()) ON CONFLICT(provider,provider_model_id) DO UPDATE SET model_id=EXCLUDED.model_id,input_usd_per_million=EXCLUDED.input_usd_per_million,output_usd_per_million=EXCLUDED.output_usd_per_million,cache_read_usd_per_million=EXCLUDED.cache_read_usd_per_million,cache_write_usd_per_million=EXCLUDED.cache_write_usd_per_million,context_length=EXCLUDED.context_length,metadata=EXCLUDED.metadata,observed_at=now(),status='active'`, mid, source, r.ProviderModel, r.Input, r.Output, r.CacheRead, r.CacheWrite, r.Context, mustJSON(r.Metadata)); err != nil {
			return err
		}
	}
	for name, value := range r.Benchmarks {
		if _, err = s.db.ExecContext(ctx, `INSERT INTO benchmark_results(model_id,benchmark_name,normalized_name,value,unit,source_key,observed_at) VALUES($1,$2,$3,$4,'fraction',$5,now())`, mid, name, NormalizeBenchmark(name), value, source); err != nil {
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
		parts := strings.Split(id, "/")
		creator := ""
		if len(parts) > 1 {
			creator = parts[0]
		}
		p, _ := m["pricing"].(map[string]any)
		in := number(p["prompt"]) * 1e6
		outp := number(p["completion"]) * 1e6
		crd, cwr := numberPtr(p["cache_read"]), numberPtr(p["cache_write"])
		if crd != nil {
			v := *crd * 1e6
			crd = &v
		}
		if cwr != nil {
			v := *cwr * 1e6
			cwr = &v
		}
		out = append(out, normalizedRecord{SourceID: id, Name: name, Creator: creator, ProviderModel: id, Input: &in, Output: &outp, CacheRead: crd, CacheWrite: cwr, Context: int64(number(m["context_length"])), Metadata: m})
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
		in, outp := number(m["input_price"]), number(m["output_price"])
		out = append(out, normalizedRecord{SourceID: id, Name: name, ProviderModel: id, Input: floatPtr(in), Output: floatPtr(outp), Metadata: m})
	}
	return out, nil
}

func getJSON(ctx context.Context, url, key, header string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
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
