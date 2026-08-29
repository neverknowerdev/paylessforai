package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/dialect/sqlite/um"
)

// RoutingGroupsRepository owns the complete aggregate write/read path for a
// routing group. Bob executes every table operation, including aggregate
// operations that span several related tables in one transaction.
type RoutingGroupsRepository struct {
	bobRepository
	database bob.DB
}

func (r *RoutingGroupsRepository) ListGroups(ctx context.Context) ([]groups.Definition, error) {
	return r.List(ctx)
}

func (r *RoutingGroupsRepository) List(ctx context.Context) ([]groups.Definition, error) {
	if r == nil || r.exec == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	rows, err := bobmodels.RoutingGroups.Query(
		sm.OrderBy(bobmodels.RoutingGroups.Columns.Name),
		sm.OrderBy(bobmodels.RoutingGroups.Columns.ID),
	).All(ctx, r.exec)
	if err != nil {
		return nil, err
	}
	result := make([]groups.Definition, 0, len(rows))
	for _, row := range rows {
		item := groupDefinitionFromBob(row)
		item.Stages, err = r.loadStages(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (r *RoutingGroupsRepository) Get(ctx context.Context, id string) (groups.Definition, error) {
	if r == nil || r.exec == nil {
		return groups.Definition{}, fmt.Errorf("database unavailable")
	}
	row, err := bobmodels.FindRoutingGroup(ctx, r.exec, id)
	if err != nil {
		return groups.Definition{}, err
	}
	item := groupDefinitionFromBob(row)
	item.Stages, err = r.loadStages(ctx, id)
	if err != nil {
		return groups.Definition{}, err
	}
	return item, nil
}

func (r *RoutingGroupsRepository) Save(ctx context.Context, definition groups.Definition, expectedRevision *int64) (groups.Definition, error) {
	if r == nil || r.exec == nil {
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
	tx, err := r.database.Begin(ctx)
	if err != nil {
		return groups.Definition{}, err
	}
	defer tx.Rollback(ctx)

	current, err := bobmodels.FindRoutingGroup(ctx, tx, definition.ID)
	if err == sql.ErrNoRows {
		if expectedRevision != nil {
			return groups.Definition{}, fmt.Errorf("group_revision_conflict")
		}
		definition.Revision = 1
		definition.CreatedAt = now
		createdAt := now.Format(time.RFC3339Nano)
		enabled := groupBoolInt(definition.Enabled)
		revision := definition.Revision
		id, name, slug, description := definition.ID, definition.Name, definition.Slug, definition.Description
		_, err = bobmodels.RoutingGroups.Insert(&bobmodels.RoutingGroupSetter{ID: &id, Name: &name, Slug: &slug, Description: &description, Enabled: &enabled, Revision: &revision, CreatedAt: &createdAt, UpdatedAt: &createdAt}).One(ctx, tx)
	} else if err == nil {
		if expectedRevision == nil || *expectedRevision != current.Revision {
			return groups.Definition{}, fmt.Errorf("group_revision_conflict")
		}
		definition.Revision = current.Revision + 1
		definition.CreatedAt, _ = time.Parse(time.RFC3339Nano, current.CreatedAt)
		enabled := groupBoolInt(definition.Enabled)
		revision := definition.Revision
		updatedAt := now.Format(time.RFC3339Nano)
		result, updateErr := bobmodels.RoutingGroups.Update(
			(&bobmodels.RoutingGroupSetter{Name: &definition.Name, Slug: &definition.Slug, Description: &definition.Description, Enabled: &enabled, Revision: &revision, UpdatedAt: &updatedAt}).UpdateMod(),
			um.Where(sqlite.And(bobmodels.RoutingGroups.Columns.ID.EQ(sqlite.Arg(definition.ID)), bobmodels.RoutingGroups.Columns.Revision.EQ(sqlite.Arg(current.Revision)))),
		).Exec(ctx, tx)
		if updateErr == nil {
			affected := result
			if affected == 0 {
				updateErr = fmt.Errorf("group_revision_conflict")
			}
		}
		err = updateErr
	} else {
		return groups.Definition{}, err
	}
	if err != nil {
		return groups.Definition{}, err
	}
	if _, err = bobmodels.RoutingGroupStages.Delete(dm.Where(bobmodels.RoutingGroupStages.Columns.GroupID.EQ(sqlite.Arg(definition.ID)))).Exec(ctx, tx); err != nil {
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
		id, groupID, position, name, selection := stage.ID, definition.ID, int64(stage.Position), stage.Name, stage.Selection
		in, out, total := nullableInt64FromPointer(stage.MaximumInputPicoUSDPerToken), nullableInt64FromPointer(stage.MaximumOutputPicoUSDPerToken), nullableInt64FromPointer(stage.MaximumExpectedCostPicoUSD)
		sameRetries, tryRetries := nullableInt64FromInt(stage.SameRouteRetries), nullableInt64FromInt(stage.TryRetries)
		if _, err = bobmodels.RoutingGroupStages.Insert(&bobmodels.RoutingGroupStageSetter{ID: &id, GroupID: &groupID, Position: &position, Name: &name, SelectionStrategy: &selection, MaximumInputPicoUsdPerToken: &in, MaximumOutputPicoUsdPerToken: &out, MaximumExpectedCostPicoUsd: &total, SameRouteRetries: &sameRetries, TryRetries: &tryRetries}).One(ctx, tx); err != nil {
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
			sourceID, stageID, sourcePosition, sourceKind, providerNamesJSON := ids.New(), stage.ID, int64(j), string(source.Kind), string(providerNames)
			modelID, nestedGroupID, providerName := groupNullableString(source.ModelID), groupNullableString(source.GroupID), groupNullableString(strings.ToLower(strings.TrimSpace(source.ProviderName)))
			retries, maxPrice := nullableInt64FromInt(source.Retries), nullableInt64FromInt(source.MaximumOfficialPricePercent)
			includeNew := groupBoolInt(source.IncludeNewProviders)
			if _, err = bobmodels.RoutingGroupSources.Insert(&bobmodels.RoutingGroupSourceSetter{ID: &sourceID, StageID: &stageID, Position: &sourcePosition, SourceKind: &sourceKind, ModelID: &modelID, NestedGroupID: &nestedGroupID, ProviderName: &providerName, ProviderNames: &providerNamesJSON, IncludeNewProviders: &includeNew, Retries: &retries, MaximumOfficialPricePercent: &maxPrice}).One(ctx, tx); err != nil {
				return groups.Definition{}, err
			}
		}
		for _, provider := range stage.ProviderNames {
			stageID, providerName := stage.ID, strings.ToLower(strings.TrimSpace(provider))
			if _, err = bobmodels.RoutingGroupStageProviders.Insert(&bobmodels.RoutingGroupStageProviderSetter{StageID: &stageID, ProviderName: &providerName}).One(ctx, tx); err != nil {
				return groups.Definition{}, err
			}
		}
		for _, class := range billing {
			stageID, billingClass := stage.ID, string(class)
			if _, err = bobmodels.RoutingGroupStageBillingClasses.Insert(&bobmodels.RoutingGroupStageBillingClassSetter{StageID: &stageID, BillingClass: &billingClass}).One(ctx, tx); err != nil {
				return groups.Definition{}, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return groups.Definition{}, err
	}
	return definition, nil
}

func (r *RoutingGroupsRepository) Delete(ctx context.Context, id string, expectedRevision int64) error {
	if r == nil || r.exec == nil {
		return fmt.Errorf("database unavailable")
	}
	result, err := bobmodels.RoutingGroups.Delete(dm.Where(sqlite.And(bobmodels.RoutingGroups.Columns.ID.EQ(sqlite.Arg(id)), bobmodels.RoutingGroups.Columns.Revision.EQ(sqlite.Arg(expectedRevision))))).Exec(ctx, r.exec)
	if err != nil {
		return err
	}
	count := result
	if count == 0 {
		return fmt.Errorf("group_revision_conflict")
	}
	return nil
}

func (r *RoutingGroupsRepository) loadStages(ctx context.Context, groupID string) ([]groups.Stage, error) {
	rows, err := bobmodels.RoutingGroupStages.Query(sm.Where(bobmodels.RoutingGroupStages.Columns.GroupID.EQ(sqlite.Arg(groupID))), sm.OrderBy(bobmodels.RoutingGroupStages.Columns.Position)).All(ctx, r.exec)
	if err != nil {
		return nil, err
	}
	result := make([]groups.Stage, 0, len(rows))
	for _, row := range rows {
		stage := stageFromBob(row)
		sources, providers, billing, err := r.loadStageChildren(ctx, stage.ID)
		if err != nil {
			return nil, err
		}
		stage.Sources, stage.ProviderNames, stage.BillingClasses = sources, providers, billing
		result = append(result, stage)
	}
	return result, nil
}

func (r *RoutingGroupsRepository) loadStageChildren(ctx context.Context, stageID string) ([]groups.Source, []string, []groups.BillingClass, error) {
	sourcesRows, err := bobmodels.RoutingGroupSources.Query(sm.Where(bobmodels.RoutingGroupSources.Columns.StageID.EQ(sqlite.Arg(stageID))), sm.OrderBy(bobmodels.RoutingGroupSources.Columns.Position)).All(ctx, r.exec)
	if err != nil {
		return nil, nil, nil, err
	}
	sources := make([]groups.Source, 0, len(sourcesRows))
	for _, row := range sourcesRows {
		var providerNames []string
		if err := json.Unmarshal([]byte(row.ProviderNames), &providerNames); err != nil {
			return nil, nil, nil, fmt.Errorf("decode group source provider names: %w", err)
		}
		sources = append(sources, groups.Source{Kind: groups.SourceKind(row.SourceKind), ModelID: stringValue(row.ModelID), GroupID: stringValue(row.NestedGroupID), ProviderName: stringValue(row.ProviderName), ProviderNames: providerNames, IncludeNewProviders: row.IncludeNewProviders != 0, Retries: intPointer(row.Retries), MaximumOfficialPricePercent: intPointer(row.MaximumOfficialPricePercent)})
	}
	providerRows, err := bobmodels.RoutingGroupStageProviders.Query(sm.Where(bobmodels.RoutingGroupStageProviders.Columns.StageID.EQ(sqlite.Arg(stageID))), sm.OrderBy(bobmodels.RoutingGroupStageProviders.Columns.ProviderName)).All(ctx, r.exec)
	if err != nil {
		return nil, nil, nil, err
	}
	providers := make([]string, 0, len(providerRows))
	for _, row := range providerRows {
		providers = append(providers, row.ProviderName)
	}
	billingRows, err := bobmodels.RoutingGroupStageBillingClasses.Query(sm.Where(bobmodels.RoutingGroupStageBillingClasses.Columns.StageID.EQ(sqlite.Arg(stageID))), sm.OrderBy(bobmodels.RoutingGroupStageBillingClasses.Columns.BillingClass)).All(ctx, r.exec)
	if err != nil {
		return nil, nil, nil, err
	}
	billing := make([]groups.BillingClass, 0, len(billingRows))
	for _, row := range billingRows {
		billing = append(billing, groups.BillingClass(row.BillingClass))
	}
	return sources, providers, billing, nil
}

func groupDefinitionFromBob(row *bobmodels.RoutingGroup) groups.Definition {
	created, _ := time.Parse(time.RFC3339Nano, row.CreatedAt)
	updated, _ := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	return groups.Definition{ID: row.ID, Name: row.Name, Slug: row.Slug, Description: row.Description, Enabled: row.Enabled != 0, Revision: row.Revision, CreatedAt: created, UpdatedAt: updated}
}

func stageFromBob(row *bobmodels.RoutingGroupStage) groups.Stage {
	return groups.Stage{ID: row.ID, Position: int(row.Position), Name: row.Name, Selection: row.SelectionStrategy, MaximumInputPicoUSDPerToken: int64Pointer(row.MaximumInputPicoUsdPerToken), MaximumOutputPicoUSDPerToken: int64Pointer(row.MaximumOutputPicoUsdPerToken), MaximumExpectedCostPicoUSD: int64Pointer(row.MaximumExpectedCostPicoUsd), SameRouteRetries: intPointer(row.SameRouteRetries), TryRetries: intPointer(row.TryRetries)}
}

func nullableInt64FromPointer(value *int64) sql.Null[int64] {
	if value == nil {
		return sql.Null[int64]{}
	}
	return sql.Null[int64]{V: *value, Valid: true}
}

func nullableInt64FromInt(value *int) sql.Null[int64] {
	if value == nil {
		return sql.Null[int64]{}
	}
	return sql.Null[int64]{V: int64(*value), Valid: true}
}

func groupNullableString(value string) sql.Null[string] {
	if strings.TrimSpace(value) == "" {
		return sql.Null[string]{}
	}
	return sql.Null[string]{V: value, Valid: true}
}

func stringValue(value sql.Null[string]) string {
	if !value.Valid {
		return ""
	}
	return value.V
}

func intPointer(value sql.Null[int64]) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.V)
	return &result
}

func groupBoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
