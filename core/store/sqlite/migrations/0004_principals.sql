-- Principals serendipd has seen authenticate (spec 017 plan.md decision 6),
-- so a person can be picked when assigning a role. There is no directory API
-- to ask instead: the identity provider's management API isn't reachable
-- through the Kubernetes Service proxy.
CREATE TABLE principals (
    type         TEXT NOT NULL,
    id           TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    groups       TEXT NOT NULL DEFAULT '[]',
    first_seen   TEXT NOT NULL,
    last_seen    TEXT NOT NULL,
    PRIMARY KEY (type, id)
);
