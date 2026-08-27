package statserver

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

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
		cr, err := s.db.QueryContext(ctx, `SELECT signal_type,benchmark_selector,weight,required,min_value,max_value,direction FROM capability_profile_components WHERE profile_version_id=$1 ORDER BY display_order,id`, vid)
		if err != nil {
			return err
		}
		var comps []component
		for cr.Next() {
			var c component
			var selector sql.NullString
			_ = cr.Scan(&c.Type, &selector, &c.Weight, &c.Required, &c.Min, &c.Max, &c.Direction)
			c.Selector = selector.String
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
			parts := []scorePart{}
			for _, c := range comps {
				if c.Type != "benchmark" {
					continue
				}
				var value float64
				if err := s.db.QueryRowContext(ctx, `SELECT value FROM benchmark_results WHERE model_id=$1 AND normalized_name=$2 ORDER BY verified DESC,observed_at DESC,id DESC LIMIT 1`, mid, NormalizeBenchmark(c.Selector)).Scan(&value); err != nil || c.Max <= c.Min {
					continue
				}
				norm := (value - c.Min) / (c.Max - c.Min)
				if c.Direction == "lower" {
					norm = 1 - norm
				}
				if norm < 0 {
					norm = 0
				}
				if norm > 1 {
					norm = 1
				}
				parts = append(parts, scorePart{Selector: c.Selector, Value: norm, Weight: c.Weight})
			}
			if len(parts) == 0 {
				continue
			}
			var weights, weighted float64
			for _, p := range parts {
				weights += float64(p.Weight)
				weighted += float64(p.Weight) * p.Value
			}
			base := weighted / weights
			_, _ = s.db.ExecContext(ctx, `INSERT INTO capability_scores(profile_version_id,model_id,score,base_score,coverage,explanation) VALUES($1,$2,$3,$4,1,$5) ON CONFLICT(profile_version_id,model_id) DO UPDATE SET score=EXCLUDED.score,base_score=EXCLUDED.base_score,coverage=EXCLUDED.coverage,explanation=EXCLUDED.explanation,calculated_at=now()`, vid, mid, 100*base, base, mustJSON(map[string]any{"components": parts, "policy": "linear_penalty"}))
		}
		mr.Close()
		_ = pid
	}
	return nil
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
		var version int
		var score, base, cov float64
		var at time.Time
		_ = rows.Scan(&k, &d, &version, &score, &base, &cov, &at)
		out = append(out, map[string]any{"key": k, "display_name": d, "version": version, "score": score, "base_score": base, "coverage": cov, "calculated_at": at})
	}
	jsonResponse(w, map[string]any{"data": out})
}
