-- Spec 020 T003: Azure-style environments (plan.md decision 2). A Project
-- is the subscription and an Environment the resource group deployed into:
-- Applications move up from their Environment to its Project, and each of
-- their Components gets a Deployment in that Environment, so what ran
-- there keeps running. A data change by the migration itself, so, like
-- 0006's, it isn't audited as a change by anyone; what it changed for
-- people is written to migration_notes, which Migrate logs.

-- Notes a migration leaves for whoever runs it (logged by Migrate).
CREATE TABLE IF NOT EXISTS migration_notes (
    migration  INTEGER NOT NULL,
    note       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- A re-run (only ever in tests) replaces its own notes.
DELETE FROM migration_notes WHERE migration = 8;

-- The store's fixed-width time format (store.go timeLayout): nine
-- fractional digits, so stored times sort as text.
CREATE TEMP TABLE m8_now AS
SELECT strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.' || substr(strftime('%f', 'now'), 4, 3) || '000000Z' AS at;

-- Each Application owned by an Environment, with that Environment's
-- Project (NULL when the Environment has none).
CREATE TEMP TABLE m8_moves AS
SELECT a.id AS app_id,
       e.id AS env_id,
       (SELECT json_extract(o.value, '$.id') FROM json_each(e.owner_refs) o
         WHERE json_extract(o.value, '$.kind') = 'Project' LIMIT 1) AS project_id
FROM resources a
JOIN json_each(a.owner_refs) ar ON json_extract(ar.value, '$.kind') = 'Environment'
JOIN resources e ON e.id = json_extract(ar.value, '$.id') AND e.kind = 'Environment'
WHERE a.kind = 'Application';

-- An Environment without a Project: its Applications stay where they are.
INSERT INTO migration_notes (migration, note, created_at)
SELECT 8, 'Application ' || app_id || ' stays under Environment ' || env_id
       || ', which has no Project to move it to; give the Environment a Project owner, then move the Application there',
       (SELECT at FROM m8_now)
FROM m8_moves WHERE project_id IS NULL;

DELETE FROM m8_moves WHERE project_id IS NULL;

-- Applications: the Environment owner becomes its Project.
UPDATE resources
SET owner_refs = (
        SELECT json_group_array(
            CASE WHEN json_extract(o.value, '$.kind') = 'Environment' AND json_extract(o.value, '$.id') = m.env_id
                 THEN json_object('kind', 'Project', 'id', m.project_id)
                 ELSE json(o.value) END)
        FROM json_each(resources.owner_refs) o),
    resource_version = resource_version + 1,
    updated_at = (SELECT at FROM m8_now)
FROM m8_moves m
WHERE resources.id = m.app_id;

DELETE FROM edges
WHERE edge_type = 'owns' AND from_kind = 'Environment' AND to_kind = 'Application'
  AND EXISTS (SELECT 1 FROM m8_moves m WHERE m.env_id = edges.from_id AND m.app_id = edges.to_id);

INSERT OR IGNORE INTO edges (from_kind, from_id, to_kind, to_id, edge_type, created_at)
SELECT 'Project', project_id, 'Application', app_id, 'owns', (SELECT at FROM m8_now) FROM m8_moves;

-- Each Component of a moved Application runs on as a Deployment in the
-- Environment it was under, on its latest release, with no overrides.
-- Deterministic IDs keep a re-run from adding a second one.
CREATE TEMP TABLE m8_deployments AS
SELECT 'dpl-' || c.id AS id, c.name AS name, c.id AS component_id, m.env_id AS env_id
FROM resources c
JOIN json_each(c.owner_refs) cr ON json_extract(cr.value, '$.kind') = 'Application'
JOIN m8_moves m ON m.app_id = json_extract(cr.value, '$.id')
WHERE c.kind = 'Component';

INSERT OR IGNORE INTO resources (id, kind, name, labels, owner_refs, relationships, spec, status, resource_version, created_at, updated_at)
SELECT id, 'Deployment', name, '{}',
       json_array(json_object('kind', 'Environment', 'id', env_id)),
       json_array(json_object('type', 'depends-on', 'targetKind', 'Component', 'targetId', component_id)),
       json_object('component', component_id),
       '{}', 1, (SELECT at FROM m8_now), (SELECT at FROM m8_now)
FROM m8_deployments;

INSERT OR IGNORE INTO edges (from_kind, from_id, to_kind, to_id, edge_type, created_at)
SELECT 'Environment', env_id, 'Deployment', id, 'owns', (SELECT at FROM m8_now) FROM m8_deployments;

INSERT OR IGNORE INTO edges (from_kind, from_id, to_kind, to_id, edge_type, created_at)
SELECT 'Deployment', id, 'Component', component_id, 'depends-on', (SELECT at FROM m8_now) FROM m8_deployments;

-- Role assignments at an Environment that had Applications under it: they
-- used to cover those Applications and their Components, and now cover
-- deployments into the Environment instead (the plan's stated cost).
INSERT INTO migration_notes (migration, note, created_at)
SELECT 8, 'role assignment ' || r.id || ' (' || json_extract(r.spec, '$.principal.type') || ' '
       || json_extract(r.spec, '$.principal.id') || ', role ' || json_extract(r.spec, '$.roleDefinitionId')
       || ') at Environment/' || json_extract(r.spec, '$.scope.id')
       || ' no longer covers the Applications that were under it; it now covers deployments into that Environment. '
       || 'Assign it at the Project, or at each Application, if they still need them',
       (SELECT at FROM m8_now)
FROM resources r
WHERE r.kind = 'RoleAssignment'
  AND json_extract(r.spec, '$.scope.kind') = 'Environment'
  AND json_extract(r.spec, '$.scope.id') IN (SELECT env_id FROM m8_moves);

DROP TABLE m8_deployments;
DROP TABLE m8_moves;
DROP TABLE m8_now;
