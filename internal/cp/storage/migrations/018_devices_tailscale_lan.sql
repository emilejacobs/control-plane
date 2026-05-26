-- +goose Up

-- Issue #14: surface LAN IP + Tailscale name/IP for the per-device
-- view's Verify-angle deep-link and the LAN-URL fallback hint
-- deferred from issue #4 per ADR-032.
--
-- All three columns are nullable: agents that predate the rollout
-- never populate them, and a heartbeat that omits a field (because
-- the device temporarily lost tailnet visibility) leaves the
-- previously stored value alone — cp-ingest's UPDATE is conditional
-- on field-presence, not blind UPSERT. lan_ip is the bench Mac's
-- primary RFC1918; tailscale_ip is the 100.64/10 CGNAT address;
-- tailscale_name is Self.DNSName from `tailscale status --json`
-- with the trailing dot stripped.
--
-- No indexes — the dashboard reads these per-device, not as part of
-- a search predicate.
ALTER TABLE devices
    ADD COLUMN lan_ip         text,
    ADD COLUMN tailscale_ip   text,
    ADD COLUMN tailscale_name text;

-- +goose Down

ALTER TABLE devices
    DROP COLUMN lan_ip,
    DROP COLUMN tailscale_ip,
    DROP COLUMN tailscale_name;
