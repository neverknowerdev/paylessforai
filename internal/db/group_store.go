package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/ids"
)

func (s *Store) ListGroups(ctx context.Context) ([]groups.Definition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,slug,description,enabled,revision,created_at,updated_at FROM routing_groups ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	result := []groups.Definition{}
	for rows.Next() {
		var item groups.Definition
		var enabled int
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.Slug, &item.Description, &enabled, &item.Revision, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		stages, err := s.loadGroupStages(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Stages = stages
	}
	return result, nil
}

func (s *Store) GetGroup(ctx context.Context, id string) (groups.Definition, error) {
	items, err := s.ListGroups(ctx)
	if err != nil {
		return groups.Definition{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return groups.Definition{}, sql.ErrNoRows
}

func (s *Store) SaveGroup(ctx context.Context, definition groups.Definition, expectedRevision *int64) (groups.Definition, error) {
	definition.Slug = groups.NormalizeSlug(definition.Slug)
	if definition.ID == "" {
		definition.ID = ids.New()
	}
	now := time.Now().UTC()
	definition.UpdatedAt = now
	if definition.Revision <= 0 {
		definition.Revision = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return groups.Definition{}, err
	}
	defer tx.Rollback()
	var currentRevision int64
	var created string
	err = tx.QueryRowContext(ctx, `SELECT revision,created_at FROM routing_groups WHERE id = ?`, definition.ID).Scan(&currentRevision, &created)
	if err == sql.ErrNoRows {
		if expectedRevision != nil {
			return groups.Definition{}, fmt.Errorf("group_revision_conflict")
		}
		definition.Revision = 1
		definition.CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO routing_groups(id,name,slug,description,enabled,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, definition.ID, definition.Name, definition.Slug, definition.Description, boolInt(definition.Enabled), definition.Revision, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	} else if err == nil {
		if expectedRevision == nil || *expectedRevision != currentRevision {
			return groups.Definition{}, fmt.Errorf("group_revision_conflict")
		}
		definition.Revision = currentRevision + 1
		definition.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		_, err = tx.ExecContext(ctx, `UPDATE routing_groups SET name=?,slug=?,description=?,enabled=?,revision=?,updated_at=? WHERE id=? AND revision=?`, definition.Name, definition.Slug, definition.Description, boolInt(definition.Enabled), definition.Revision, now.Format(time.RFC3339Nano), definition.ID, currentRevision)
	} else {
		return groups.Definition{}, err
	}
	if err != nil {
		return groups.Definition{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM routing_group_stages WHERE group_id=?`, definition.ID); err != nil {
		return groups.Definition{}, err
	}
	for i, stage := range definition.Stages {
		stage.Position = i
		if stage.ID == "" {
			stage.ID = ids.New()
		}
		if stage.Selection == "" {
			stage.Selection = groups.SelectionLowestExpectedCost
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO routing_group_stages(id,group_id,position,name,selection_strategy,maximum_input_pico_usd_per_token,maximum_output_pico_usd_per_token,maximum_expected_cost_pico_usd,same_route_retries,try_retries) VALUES(?,?,?,?,?,?,?,?,?,?)`, stage.ID, definition.ID, stage.Position, stage.Name, stage.Selection, nullableGroupInt(stage.MaximumInputPicoUSDPerToken), nullableGroupInt(stage.MaximumOutputPicoUSDPerToken), nullableGroupInt(stage.MaximumExpectedCostPicoUSD), nullableGroupRetry(stage.SameRouteRetries), nullableGroupRetry(stage.TryRetries)); err != nil {
			return groups.Definition{}, err
		}
		billing := stage.BillingClasses
		if len(billing) == 0 {
			billing = groups.AllBillingClasses
		}
		for j, source := range stage.Sources {
			if source.Kind == "" {
				if source.GroupID != "" {
					source.Kind = groups.SourceGroup
				} else {
					source.Kind = groups.SourceModel
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO routing_group_sources(id,stage_id,position,source_kind,model_id,nested_group_id,provider_name,retries,maximum_official_price_percent) VALUES(?,?,?,?,?,?,?,?,?)`, ids.New(), stage.ID, j, source.Kind, nullableStringGroup(source.ModelID), nullableStringGroup(source.GroupID), nullableStringGroup(strings.ToLower(strings.TrimSpace(source.ProviderName))), nullableGroupRetry(source.Retries), nullableGroupRetry(source.MaximumOfficialPricePercent)); err != nil {
				return groups.Definition{}, err
			}
		}
		for _, provider := range stage.ProviderNames {
			if _, err = tx.ExecContext(ctx, `INSERT INTO routing_group_stage_providers(stage_id,provider_name) VALUES(?,?)`, stage.ID, strings.ToLower(strings.TrimSpace(provider))); err != nil {
				return groups.Definition{}, err
			}
		}
		for _, class := range billing {
			if _, err = tx.ExecContext(ctx, `INSERT INTO routing_group_stage_billing_classes(stage_id,billing_class) VALUES(?,?)`, stage.ID, class); err != nil {
				return groups.Definition{}, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return groups.Definition{}, err
	}
	return definition, nil
}

func (s *Store) DeleteGroup(ctx context.Context, id string, expectedRevision int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM routing_groups WHERE id=? AND revision=?`, id, expectedRevision)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return fmt.Errorf("group_revision_conflict")
	}
	return nil
}

func (s *Store) loadGroupStages(ctx context.Context, groupID string) ([]groups.Stage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,position,name,selection_strategy,maximum_input_pico_usd_per_token,maximum_output_pico_usd_per_token,maximum_expected_cost_pico_usd,same_route_retries,try_retries FROM routing_group_stages WHERE group_id=? ORDER BY position`, groupID)
	if err != nil {
		return nil, err
	}
	result := []groups.Stage{}
	for rows.Next() {
		var stage groups.Stage
		var in, out, total, retries, tryRetries sql.NullInt64
		if err := rows.Scan(&stage.ID, &stage.Position, &stage.Name, &stage.Selection, &in, &out, &total, &retries, &tryRetries); err != nil {
			return nil, err
		}
		stage.MaximumInputPicoUSDPerToken = nullGroupInt(in)
		stage.MaximumOutputPicoUSDPerToken = nullGroupInt(out)
		stage.MaximumExpectedCostPicoUSD = nullGroupInt(total)
		if retries.Valid {
			value := int(retries.Int64)
			stage.SameRouteRetries = &value
		}
		if tryRetries.Valid {
			value := int(tryRetries.Int64)
			stage.TryRetries = &value
		}
		result = append(result, stage)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		stage := &result[index]
		sourceRows, err := s.db.QueryContext(ctx, `SELECT source_kind,COALESCE(model_id,''),COALESCE(nested_group_id,''),COALESCE(provider_name,''),retries,maximum_official_price_percent FROM routing_group_sources WHERE stage_id=? ORDER BY position`, stage.ID)
		if err != nil {
			return nil, err
		}
		for sourceRows.Next() {
			var source groups.Source
			var retries, percent sql.NullInt64
			if err := sourceRows.Scan(&source.Kind, &source.ModelID, &source.GroupID, &source.ProviderName, &retries, &percent); err != nil {
				sourceRows.Close()
				return nil, err
			}
			if retries.Valid {
				value := int(retries.Int64)
				source.Retries = &value
			}
			if percent.Valid {
				value := int(percent.Int64)
				source.MaximumOfficialPricePercent = &value
			}
			stage.Sources = append(stage.Sources, source)
		}
		sourceRows.Close()
		providerRows, err := s.db.QueryContext(ctx, `SELECT provider_name FROM routing_group_stage_providers WHERE stage_id=? ORDER BY provider_name`, stage.ID)
		if err != nil {
			return nil, err
		}
		for providerRows.Next() {
			var value string
			if err := providerRows.Scan(&value); err != nil {
				providerRows.Close()
				return nil, err
			}
			stage.ProviderNames = append(stage.ProviderNames, value)
		}
		providerRows.Close()
		billingRows, err := s.db.QueryContext(ctx, `SELECT billing_class FROM routing_group_stage_billing_classes WHERE stage_id=? ORDER BY billing_class`, stage.ID)
		if err != nil {
			return nil, err
		}
		for billingRows.Next() {
			var value groups.BillingClass
			if err := billingRows.Scan(&value); err != nil {
				billingRows.Close()
				return nil, err
			}
			stage.BillingClasses = append(stage.BillingClasses, value)
		}
		billingRows.Close()
	}
	return result, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func nullableGroupInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableGroupRetry(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableStringGroup(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
func nullGroupInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
