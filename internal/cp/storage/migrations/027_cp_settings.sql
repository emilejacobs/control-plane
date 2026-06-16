-- +goose Up

-- CP-singleton settings — a small key/value store for account-wide config that
-- staff set from the dashboard (not Terraform/Secrets Manager, which are manual
-- apply). First use (#84, ADR-036 §5): the account-wide Plate Recognizer token
-- under key 'plate_recognizer_token', pushed to devices at Commission. Values
-- may be secret — the API never returns them raw (reads expose only is_set) and
-- never logs them.
CREATE TABLE cp_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE cp_settings;
