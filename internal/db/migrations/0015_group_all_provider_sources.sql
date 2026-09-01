ALTER TABLE routing_group_sources ADD COLUMN provider_names TEXT NOT NULL DEFAULT '[]';
ALTER TABLE routing_group_sources ADD COLUMN include_new_providers INTEGER NOT NULL DEFAULT 1;
