package repositories

import (
	"context"
	"database/sql"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

type ProfileRepository struct{ db *sql.DB }

func (r *ProfileRepository) Public(ctx context.Context) ([]models.Profile, error) {
	return r.list(ctx, "WHERE p.public AND v.state='published'")
}
func (r *ProfileRepository) Admin(ctx context.Context) ([]models.Profile, error) {
	return r.list(ctx, "WHERE v.state='published'")
}

func (r *ProfileRepository) list(ctx context.Context, condition string) ([]models.Profile, error) {
	query := `SELECT p.key,p.display_name,p.description,v.id,v.version,v.state,v.minimum_coverage,v.missing_data_policy FROM capability_profiles p JOIN capability_profile_versions v ON v.profile_id=p.id ` + condition + ` ORDER BY p.key`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []models.Profile{}
	for rows.Next() {
		var profile models.Profile
		if err := rows.Scan(&profile.Key, &profile.DisplayName, &profile.Description, &profile.VersionID, &profile.Version, &profile.State, &profile.MinimumCoverage, &profile.MissingDataPolicy); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (r *ProfileRepository) Create(ctx context.Context, input models.CreateProfile) (profileID, versionID int64, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `INSERT INTO capability_profiles(key,display_name,description) VALUES($1,$2,$3) RETURNING id`, input.Key, input.DisplayName, input.Description).Scan(&profileID); err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO capability_profile_versions(profile_id,version,state) VALUES($1,1,'draft') RETURNING id`, profileID).Scan(&versionID); err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return profileID, versionID, nil
}

func (r *ProfileRepository) CreateSignal(ctx context.Context, input models.CreateSignal) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO manual_score_signals(key,display_name,description) VALUES($1,$2,$3)`, input.Key, input.DisplayName, input.Description)
	return err
}

func (r *ProfileRepository) AddComponent(ctx context.Context, versionID int64, input models.CreateComponent) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO capability_profile_components(profile_version_id,signal_type,benchmark_selector,weight,required,min_value,max_value,direction,rationale) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9)`, versionID, input.SignalType, input.Selector, input.Weight, input.Required, input.MinValue, input.MaxValue, input.Direction, input.Rationale)
	return err
}

func (r *ProfileRepository) Publish(ctx context.Context, versionID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE capability_profile_versions SET state='superseded' WHERE profile_id=(SELECT profile_id FROM capability_profile_versions WHERE id=$1) AND state='published'`, versionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capability_profile_versions SET state='published',published_at=now() WHERE id=$1`, versionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ProfileRepository) PublishedVersions(ctx context.Context) ([]models.ProfileVersion, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT p.id,v.id,p.key,p.display_name,v.minimum_coverage,v.missing_data_policy FROM capability_profiles p JOIN capability_profile_versions v ON v.profile_id=p.id AND v.state='published'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []models.ProfileVersion{}
	for rows.Next() {
		var version models.ProfileVersion
		if err := rows.Scan(&version.ProfileID, &version.ID, &version.Key, &version.DisplayName, &version.MinimumCoverage, &version.MissingDataPolicy); err != nil {
			return nil, err
		}
		components, err := r.Components(ctx, version.ID)
		if err != nil {
			return nil, err
		}
		version.Components = components
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (r *ProfileRepository) Components(ctx context.Context, versionID int64) ([]models.ProfileComponent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT signal_type,benchmark_selector,weight,required,min_value,max_value,direction FROM capability_profile_components WHERE profile_version_id=$1 ORDER BY display_order,id`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	components := []models.ProfileComponent{}
	for rows.Next() {
		var component models.ProfileComponent
		var selector sql.NullString
		if err := rows.Scan(&component.SignalType, &selector, &component.Weight, &component.Required, &component.MinValue, &component.MaxValue, &component.Direction); err != nil {
			return nil, err
		}
		component.Selector = selector.String
		components = append(components, component)
	}
	return components, rows.Err()
}

func (r *ProfileRepository) UpsertScore(ctx context.Context, versionID, modelID int64, score, baseScore, coverage float64, explanation any) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO capability_scores(profile_version_id,model_id,score,base_score,coverage,explanation) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(profile_version_id,model_id) DO UPDATE SET score=EXCLUDED.score,base_score=EXCLUDED.base_score,coverage=EXCLUDED.coverage,explanation=EXCLUDED.explanation,calculated_at=now()`, versionID, modelID, score, baseScore, coverage, jsonBytes(explanation))
	return err
}

func (r *ProfileRepository) ScoresForModel(ctx context.Context, modelID int64) ([]models.CapabilityScore, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT p.key,p.display_name,v.version,c.score,c.base_score,c.coverage,c.calculated_at FROM capability_scores c JOIN capability_profile_versions v ON v.id=c.profile_version_id JOIN capability_profiles p ON p.id=v.profile_id WHERE c.model_id=$1 AND v.state='published' ORDER BY p.key`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scores := []models.CapabilityScore{}
	for rows.Next() {
		var score models.CapabilityScore
		if err := rows.Scan(&score.Key, &score.DisplayName, &score.Version, &score.Score, &score.BaseScore, &score.Coverage, &score.CalculatedAt); err != nil {
			return nil, err
		}
		scores = append(scores, score)
	}
	return scores, rows.Err()
}
