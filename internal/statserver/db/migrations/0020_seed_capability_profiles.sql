INSERT INTO capability_profiles(key, display_name, description)
VALUES ('programming','Programming','Coding, repository repair, and tool-use capability'),
       ('qa','QA','Factuality, structured output, and defect detection capability'),
       ('cto','CTO','Architecture, planning, coding, and review capability')
ON CONFLICT (key) DO NOTHING;

INSERT INTO capability_profile_versions(profile_id, version, state, minimum_coverage, missing_data_policy, change_note)
SELECT id, 1, 'published', 0, 'linear_penalty', 'Initial profile'
FROM capability_profiles p
WHERE NOT EXISTS (SELECT 1 FROM capability_profile_versions v WHERE v.profile_id=p.id);
