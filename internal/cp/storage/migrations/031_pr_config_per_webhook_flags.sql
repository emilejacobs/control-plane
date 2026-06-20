-- +goose Up

-- Plate Recognizer's image/caching settings are PER-WEBHOOK (each [webhooks]
-- entry carries its own image=/caching=), not global — issue #5 modelled them
-- as global columns before the real config.ini schema was known. Drop the two
-- columns; image/caching now ride inside each object of the enabled_webhooks
-- jsonb (prconfig.Webhook). Safe to drop: the feature isn't live yet.
ALTER TABLE device_pr_config DROP COLUMN image;
ALTER TABLE device_pr_config DROP COLUMN caching;

-- +goose Down

ALTER TABLE device_pr_config ADD COLUMN caching boolean NOT NULL DEFAULT false;
ALTER TABLE device_pr_config ADD COLUMN image   boolean NOT NULL DEFAULT true;
