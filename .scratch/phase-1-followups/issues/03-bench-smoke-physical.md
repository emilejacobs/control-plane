# Issue 03 — §5b smoke: network-drop + power-yank on the next in-hand bench device

Status: ready-for-human
Type: HITL
Estimate: 30 min on the hardware, once available

## Parent

- [`docs/runbooks/phase-1-wave-0-bench.md`](../../../docs/runbooks/phase-1-wave-0-bench.md) § 5b items #2 + #3.
- Wave 0 smoke completed §5a/5b#1/5c/5d/5e/5f against the Arizona field Mac (`07-eegees-mesa-macmini`); §5b#2 and #3 were deferred because that device is only reachable over Tailscale and cannot have its network or power severed without losing access to it.

## What to verify

That offline detection works for **unclean** disconnects (not just the clean `bootout`/`bootstrap` flow §5b#1 already verified):

- **§5b #2 — Network drop.** Take the device's primary interface down for ≥30s (`ifconfig en0 down` on the test Mac; `ip link set eth0 down` on Linux). The dashboard should show the device offline within the configured presence threshold. The lifecycle event path (TCP timeout, no clean FIN) and the 90s sweeper backstop both need to be exercised — note which path actually fired by tailing the cp-ingest logs.
- **§5b #3 — Power yank.** Pull power on the test device. Same expected behavior: offline within threshold; sweeper as backstop if no lifecycle event arrives.

Each case: also verify the device transitions back to online cleanly when restored.

## Acceptance criteria

- [ ] §5b #2 executed on a bench device. Notes in this issue's `## Comments` section recording: time-to-offline, which path fired (lifecycle event vs sweeper), time-to-online-after-restore.
- [ ] §5b #3 executed on a bench device. Same recording.
- [ ] The Wave 0 runbook (`docs/runbooks/phase-1-wave-0-bench.md` § 5b notes) updated with the field-results for both items.
- [ ] If either case behaves unexpectedly, file a separate issue rather than fix in this one.

## Blocked by

- Physical access to a bench device. The next bench candidate is whatever Mac mini is being staged for Wave 1; coordinate with whoever holds it.
