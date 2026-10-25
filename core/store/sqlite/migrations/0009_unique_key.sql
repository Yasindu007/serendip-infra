-- Spec 022 T003: store-level uniqueness (plan.md decision 9, RP-033). A
-- typed store may give each new row a key; (kind, unique_key) is unique,
-- so two creates racing past the API's check can't both commit. NULL keys
-- don't collide, so kinds without a key, and rows created before this
-- migration that it leaves unkeyed, are unaffected.

ALTER TABLE resources ADD COLUMN unique_key TEXT;

CREATE UNIQUE INDEX idx_resources_unique_key ON resources (kind, unique_key);

-- A re-run (only ever in tests) replaces its own notes.
DELETE FROM migration_notes WHERE migration = 9;

-- ProviderInstance's key is resource.ProviderInstanceKey: capabilityId,
-- "/", environmentId. Of existing duplicates only the oldest is keyed; the
-- reconciler already marks the rest Duplicate, and removing them goes
-- through an Approval as before.
CREATE TEMP TABLE m9_keys AS
SELECT id,
       key,
       ROW_NUMBER() OVER (PARTITION BY key ORDER BY created_at, id) AS n,
       FIRST_VALUE(id) OVER (PARTITION BY key ORDER BY created_at, id) AS first_id
FROM (SELECT id, created_at,
             json_extract(spec, '$.capabilityId') || '/' || COALESCE(json_extract(spec, '$.environmentId'), '') AS key
      FROM resources
      WHERE kind = 'ProviderInstance' AND COALESCE(json_extract(spec, '$.capabilityId'), '') != '');

UPDATE resources
SET unique_key = (SELECT key FROM m9_keys k WHERE k.id = resources.id)
WHERE id IN (SELECT id FROM m9_keys WHERE n = 1);

INSERT INTO migration_notes (migration, note, created_at)
SELECT 9, 'ProviderInstance ' || id || ' duplicates ' || first_id || ' (' || key
       || '); only the older one installs. Remove this one',
       strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.' || substr(strftime('%f', 'now'), 4, 3) || '000000Z'
FROM m9_keys WHERE n > 1 ORDER BY key, n;

DROP TABLE m9_keys;
