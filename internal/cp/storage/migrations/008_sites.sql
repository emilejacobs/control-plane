-- +goose Up
-- The site model behind site-scoped authorization (Issue 06 / PRD § AuthZ).
-- A client owns sites; operator_sites grants a non-staff operator access to
-- specific sites. Staff need no rows here — operators.is_staff is the
-- full-fleet grant.
CREATE TABLE clients (
    id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL
);

CREATE TABLE sites (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id uuid NOT NULL REFERENCES clients(id),
    name      text NOT NULL
);

CREATE TABLE operator_sites (
    operator_id uuid        NOT NULL REFERENCES operators(id),
    site_id     uuid        NOT NULL REFERENCES sites(id),
    granted_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (operator_id, site_id)
);

-- +goose Down
DROP TABLE operator_sites;
DROP TABLE sites;
DROP TABLE clients;
