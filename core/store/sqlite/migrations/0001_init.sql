CREATE TABLE resources (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL,
    name             TEXT NOT NULL,
    labels           TEXT NOT NULL DEFAULT '{}',
    owner_refs       TEXT NOT NULL DEFAULT '[]',
    relationships    TEXT NOT NULL DEFAULT '[]',
    spec             TEXT NOT NULL DEFAULT '{}',
    status           TEXT NOT NULL DEFAULT '{}',
    resource_version INTEGER NOT NULL DEFAULT 1,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX idx_resources_kind ON resources (kind);

CREATE TABLE edges (
    from_kind  TEXT NOT NULL,
    from_id    TEXT NOT NULL,
    to_kind    TEXT NOT NULL,
    to_id      TEXT NOT NULL,
    edge_type  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (from_kind, from_id, to_kind, to_id, edge_type)
);

CREATE INDEX idx_edges_from ON edges (from_id);
CREATE INDEX idx_edges_to ON edges (to_id);

CREATE TABLE audit_log (
    id              TEXT PRIMARY KEY,
    actor           TEXT NOT NULL,
    interface       TEXT NOT NULL,
    kind            TEXT NOT NULL,
    resource_id     TEXT NOT NULL,
    before          TEXT,
    after           TEXT,
    at              TEXT NOT NULL,
    policy_decision TEXT
);

CREATE INDEX idx_audit_log_resource ON audit_log (kind, resource_id);
