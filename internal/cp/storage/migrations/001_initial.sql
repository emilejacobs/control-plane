-- +goose Up
CREATE TABLE devices (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hostname        text NOT NULL,
    hardware_uuid   text NOT NULL UNIQUE,
    hardware_kind   text NOT NULL,
    os_version      text NOT NULL,
    agent_version   text NOT NULL,
    iot_thing_arn   text NOT NULL,
    mtls_cert_arn   text NOT NULL,
    last_seen       timestamptz,
    enrolled_at     timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE devices;
