-- 0007_catalog_phase4 down — drop Phase 4 catalog additions. Reversible:
-- reviews/snapshot rows are derived data; vendors.eta reverts with the column.
BEGIN;

DROP TABLE IF EXISTS top_vendors_snapshot;
DROP TABLE IF EXISTS reviews;
ALTER TABLE vendors DROP COLUMN IF EXISTS eta;

COMMIT;
