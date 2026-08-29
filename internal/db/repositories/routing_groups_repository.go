package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/ids"
)

// RoutingGroupsRepository owns the complete aggregate write/read path for a
// routing group. Keeping the aggregate transaction here prevents handlers and
// runtime code from reaching into database/sql directly.
type RoutingGroupsRepository struct{ database *sql.DB }

// ListGroups implements groups.Loader while List keeps the repository API
// consistent with the other collection repositories.
func (r *RoutingGroupsRepository) ListGroups(ctx context.Context) ([]groups.Definition, error) {
	return r.List(ctx)
}

func (r *RoutingGroupsRepository) List(ctx context.Context) ([]groups.Definition, error) {
	if r == nil || r.database == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	rows, err := r.database.QueryContext(ctx, `SELECT id,name,slug,description,enabled,revision,created_at,updated_at FROM routing_groups ORDER BY name, id`)
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
	for i := range result {
		stages, err := r.loadStages(ctx, result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].Stages = stages
	}
	return result, nil
}

func (r *RoutingGroupsRepository) Get(ctx context.Context, id string) (groups.Definition, error) {
	items, err := r.List(ctx)
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

func (r *RoutingGroupsRepository) Save(ctx context.Context, definition groups.Definition, expectedRevision *int64) (groups.Definition, error) {
	if r == nil || r.database == nil {
		return groups.Definition{}, fmt.Errorf("database unavailable")
	}
	definition.Slug = groups.NormalizeSlug(definition.Slug)
	if definition.ID == "" {
		definition.ID = ids.New()
	}
	now := time.Now().UTC()
	definition.UpdatedAt = now
	if definition.Revision <= 0 {
		definition.Revision = 1
	}
	tx, err := r.database.BeginTx(ctx, nil)
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
		_, err = tx.ExecContext(ctx, `INSERT INTO routing_groups(id,name,slug,description,enabled,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, definition.ID, definition.Name, definition.Slug, definition.Description, groupBoolInt(definition.Enabled), definition.Revision, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	} else if err == nil {
		if expectedRevision == nil || *expectedRevision != currentRevision {
			return groups.Definition{}, fmt.Errorf("group_revision_conflict")
		}
		definition.Revision = currentRevision + 1
		definition.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		_, err = tx.ExecContext(ctx, `UPDATE routing_groups SET name=?,slug=?,description=?,enabled=?,revision=?,updated_at=? WHERE id=? AND revision=?`, definition.Name, definition.Slug, definition.Description, groupBoolInt(definition.Enabled), definition.Revision, now.Format(time.RFC3339Nano), definition.ID, currentRevision)
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
		if _, err = tx.ExecContext(ctx, `INSERT INTO routing_group_stages(id,group_id,position,name,selection_strategy,maximum_input_pico_usd_per_token,maximum_output_pico_usd_per_token,maximum_expected_cost_pico_usd,same_route_retries,try_retries) VALUES(?,?,?,?,?,?,?,?,?,?)`, stage.ID, definition.ID, stage.Position, stage.Name, stage.Selection, nullableInt(stage.MaximumInputPicoUSDPerToken), nullableInt(stage.MaximumOutputPicoUSDPerToken), nullableInt(stage.MaximumExpectedCostPicoUSD), nullableRetry(stage.SameRouteRetries), nullableRetry(stage.TryRetries)); err != nil {
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
			if source.Kind == groups.SourceModel && source.ProviderName == "" && len(source.ProviderNames) == 0 {
				source.IncludeNewProviders = true
			}
			providerNames, marshalErr := json.Marshal(source.ProviderNames)
			if marshalErr != nil {
				return groups.Definition{}, marshalErr
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO routing_group_sources(id,stage_id,position,source_kind,model_id,nested_group_id,provider_name,provider_names,include_new_providers,retries,maximum_official_price_percent) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, ids.New(), stage.ID, j, source.Kind, groupNullableString(source.ModelID), groupNullableString(source.GroupID), groupNullableString(strings.ToLower(strings.TrimSpace(source.ProviderName))), string(providerNames), groupBoolInt(source.IncludeNewProviders), nullableRetry(source.Retries), nullableRetry(source.MaximumOfficialPricePercent)); err != nil {
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

func (r *RoutingGroupsRepository) Delete(ctx context.Context, id string, expectedRevision int64) error {
	if r == nil || r.database == nil {
		return fmt.Errorf("database unavailable")
	}
	result, err := r.database.ExecContext(ctx, `DELETE FROM routing_groups WHERE id=? AND revision=?`, id, expectedRevision)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return fmt.Errorf("group_revision_conflict")
	}
	return nil
}

func (r *RoutingGroupsRepository) loadStages(ctx context.Context, groupID string) ([]groups.Stage, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT id,position,name,selection_strategy,maximum_input_pico_usd_per_token,maximum_output_pico_usd_per_token,maximum_expected_cost_pico_usd,same_route_retries,try_retries FROM routing_group_stages WHERE group_id=? ORDER BY position`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []groups.Stage{}
	for rows.Next() {
		var stage groups.Stage
		var in, out, total, retries, tryRetries sql.NullInt64
		if err := rows.Scan(&stage.ID, &stage.Position, &stage.Name, &stage.Selection, &in, &out, &total, &retries, &tryRetries); err != nil {
			return nil, err
		}
		stage.MaximumInputPicoUSDPerToken = intPointer(in)
		stage.MaximumOutputPicoUSDPerToken = intPointer(out)
		stage.MaximumExpectedCostPicoUSD = intPointer(total)
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
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range result {
		sources, providers, billing, err := r.loadStageChildren(ctx, result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].Sources, result[i].ProviderNames, result[i].BillingClasses = sources, providers, billing
	}
	return result, nil
}

func (r *RoutingGroupsRepository) loadStageChildren(ctx context.Context, stageID string) ([]groups.Source, []string, []groups.BillingClass, error) {
	sourceRows, err := r.database.QueryContext(ctx, `SELECT source_kind,COALESCE(model_id,''),COALESCE(nested_group_id,''),COALESCE(provider_name,''),COALESCE(provider_names,'[]'),COALESCE(include_new_providers,1),retries,maximum_official_price_percent FROM routing_group_sources WHERE stage_id=? ORDER BY position`, stageID)
	if err != nil {
		return nil, nil, nil, err
	}
	sources := []groups.Source{}
	for sourceRows.Next() {
		var source groups.Source
		var providerNames string
		var includeNewProviders int
		var retries, percent sql.NullInt64
		if err := sourceRows.Scan(&source.Kind, &source.ModelID, &source.GroupID, &source.ProviderName, &providerNames, &includeNewProviders, &retries, &percent); err != nil {
			return nil, nil, nil, err
		}
		if err := json.Unmarshal([]byte(providerNames), &source.ProviderNames); err != nil {
			return nil, nil, nil, fmt.Errorf("decode group source provider names: %w", err)
		}
		source.IncludeNewProviders = includeNewProviders != 0
		if retries.Valid {
			value := int(retries.Int64)
			source.Retries = &value
		}
		if percent.Valid {
			value := int(percent.Int64)
			source.MaximumOfficialPricePercent = &value
		}
		sources = append(sources, source)
	}
	if err := sourceRows.Err(); err != nil {
		sourceRows.Close()
		return nil, nil, nil, err
	}
	if err := sourceRows.Close(); err != nil {
		return nil, nil, nil, err
	}
	providerRows, err := r.database.QueryContext(ctx, `SELECT provider_name FROM routing_group_stage_providers WHERE stage_id=? ORDER BY provider_name`, stageID)
	if err != nil {
		return nil, nil, nil, err
	}
	providers := []string{}
	for providerRows.Next() {
		var value string
		if err := providerRows.Scan(&value); err != nil {
			providerRows.Close()
			return nil, nil, nil, err
		}
		providers = append(providers, value)
	}
	if err := providerRows.Err(); err != nil {
		providerRows.Close()
		return nil, nil, nil, err
	}
	if err := providerRows.Close(); err != nil {
		return nil, nil, nil, err
	}
	billingRows, err := r.database.QueryContext(ctx, `SELECT billing_class FROM routing_group_stage_billing_classes WHERE stage_id=? ORDER BY billing_class`, stageID)
	if err != nil {
		return nil, nil, nil, err
	}
	billing := []groups.BillingClass{}
	for billingRows.Next() {
		var value groups.BillingClass
		if err := billingRows.Scan(&value); err != nil {
			billingRows.Close()
			return nil, nil, nil, err
		}
		billing = append(billing, value)
	}
	if err := billingRows.Err(); err != nil {
		billingRows.Close()
		return nil, nil, nil, err
	}
	return sources, providers, billing, billingRows.Close()
}

func groupBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableRetry(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func groupNullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func intPointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
