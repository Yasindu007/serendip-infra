-- Spec 019 T009: 0006's newest-first index covered every kind, so every
-- write anywhere paid for it. Approvals are the only kind that pages by it;
-- replace it with a partial index on approvals alone, like 0006's others.
-- (0006 is never edited: a database that applied it must still migrate.)
DROP INDEX IF EXISTS idx_resources_kind_created;

CREATE INDEX idx_approvals_created ON resources (created_at, id)
	WHERE kind = 'Approval';
