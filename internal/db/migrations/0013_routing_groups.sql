CREATE TABLE IF NOT EXISTS routing_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    revision INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS routing_group_stages (
    id TEXT PRIMARY KEY,
    group_id TEXT NOT NULL REFERENCES routing_groups(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    selection_strategy TEXT NOT NULL DEFAULT 'lowest_expected_cost',
    maximum_input_pico_usd_per_token INTEGER,
    maximum_output_pico_usd_per_token INTEGER,
    maximum_expected_cost_pico_usd INTEGER,
    same_route_retries INTEGER,
    UNIQUE(group_id, position)
);
CREATE TABLE IF NOT EXISTS routing_group_sources (
    id TEXT PRIMARY KEY,
    stage_id TEXT NOT NULL REFERENCES routing_group_stages(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    source_kind TEXT NOT NULL,
    model_id TEXT,
    nested_group_id TEXT REFERENCES routing_groups(id) ON DELETE RESTRICT,
    UNIQUE(stage_id, position)
);
CREATE TABLE IF NOT EXISTS routing_group_stage_providers (
    stage_id TEXT NOT NULL REFERENCES routing_group_stages(id) ON DELETE CASCADE,
    provider_name TEXT NOT NULL,
    PRIMARY KEY(stage_id, provider_name)
);
CREATE TABLE IF NOT EXISTS routing_group_stage_billing_classes (
    stage_id TEXT NOT NULL REFERENCES routing_group_stages(id) ON DELETE CASCADE,
    billing_class TEXT NOT NULL,
    PRIMARY KEY(stage_id, billing_class)
);
ALTER TABLE proxy_requests ADD COLUMN resolved_group_id TEXT;
ALTER TABLE proxy_requests ADD COLUMN resolved_group_revision INTEGER;
ALTER TABLE proxy_requests ADD COLUMN resolved_plan_json TEXT;
ALTER TABLE proxy_requests ADD COLUMN selected_logical_model TEXT;
ALTER TABLE proxy_attempts ADD COLUMN group_stage_id TEXT;
ALTER TABLE proxy_attempts ADD COLUMN group_stage_path TEXT;
ALTER TABLE proxy_attempts ADD COLUMN credential_id TEXT;
