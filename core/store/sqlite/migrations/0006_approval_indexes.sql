-- Spec 019 T007a: approvals only grow (every delete records one, and none
-- are deleted until a retention spec), so their reads must never scan them
-- all. The approval indexes are partial (kind = 'Approval') expression
-- indexes; ListApprovals' queries repeat these exact expressions so the
-- planner can use them, and approval_test.go pins the query plans.

-- Newest-first paging by kind, for approvals and any other kind's list.
CREATE INDEX idx_resources_kind_created ON resources (kind, created_at, id);

CREATE INDEX idx_approvals_phase ON resources (json_extract(status, '$.phase'), created_at, id)
	WHERE kind = 'Approval';

CREATE INDEX idx_approvals_target ON resources (json_extract(spec, '$.action.target.id'), created_at, id)
	WHERE kind = 'Approval';

-- Approvals the reconciler still has work on: settled is set once nothing
-- more will happen to one.
CREATE INDEX idx_approvals_unsettled ON resources (created_at, id)
	WHERE kind = 'Approval' AND json_extract(status, '$.settled') IS NULL;

-- Approvals recorded before settled existed: mark the ones nothing more will
-- happen to, so resync stops revisiting them. A data fix by the migration
-- itself, so it isn't audited as a change by anyone.
UPDATE resources
SET status = json_set(status, '$.settled', json('true'))
WHERE kind = 'Approval'
	AND json_extract(status, '$.settled') IS NULL
	AND (json_extract(status, '$.phase') IN ('Rejected', 'Expired', 'Failed')
		OR (json_extract(status, '$.phase') = 'Executed' AND EXISTS (
			SELECT 1 FROM json_each(status, '$.conditions')
			WHERE json_extract(value, '$.type') = 'Verified' AND json_extract(value, '$.status') = 'True')));
