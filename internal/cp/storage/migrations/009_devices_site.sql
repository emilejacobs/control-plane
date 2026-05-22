-- +goose Up
-- Ties a device to the site it lives at, so site-scoped authorization can
-- filter device reads (Issue 06). Nullable: Phase 1 enrollment does not yet
-- capture a site, and every Phase 1 operator is staff (unfiltered), so a null
-- site_id is harmless until non-staff operators arrive.
ALTER TABLE devices ADD COLUMN site_id uuid REFERENCES sites(id);

-- +goose Down
ALTER TABLE devices DROP COLUMN site_id;
