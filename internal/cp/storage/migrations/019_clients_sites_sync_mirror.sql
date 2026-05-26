-- +goose Up
-- ADR-033: clients and sites are mirrored from an upstream HTTP API
-- (api.uknomi.com) via the daily cmd/taxonomy-sync Fargate task. The
-- mirror columns let the syncer upsert by external_id, stamp sync
-- runs, and soft-delete rows that have disappeared upstream. Brand is
-- captured per Site as flat metadata; CP does not gain a brands table.
ALTER TABLE clients
    ADD COLUMN external_id    text,
    ADD COLUMN active         boolean NOT NULL DEFAULT true,
    ADD COLUMN last_synced_at timestamptz;
CREATE UNIQUE INDEX clients_external_id_uq ON clients (external_id);

ALTER TABLE sites
    ADD COLUMN external_id       text,
    ADD COLUMN active            boolean NOT NULL DEFAULT true,
    ADD COLUMN last_synced_at    timestamptz,
    ADD COLUMN brand_name        text,
    ADD COLUMN brand_external_id text;
CREATE UNIQUE INDEX sites_external_id_uq ON sites (external_id);

-- +goose Down
DROP INDEX sites_external_id_uq;
ALTER TABLE sites
    DROP COLUMN brand_external_id,
    DROP COLUMN brand_name,
    DROP COLUMN last_synced_at,
    DROP COLUMN active,
    DROP COLUMN external_id;

DROP INDEX clients_external_id_uq;
ALTER TABLE clients
    DROP COLUMN last_synced_at,
    DROP COLUMN active,
    DROP COLUMN external_id;
