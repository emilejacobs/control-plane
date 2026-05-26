# Phase 2 — Edge UI rework

The design lives entirely in the ADRs; this directory holds the in-tree pointer + future working notes.

## Design source of truth

- **[ADR-029](../../docs/adr/0029-edge-ui-rework-scope.md)** — Edge UI rework scope: CP-authoritative, rewrite onto CP stack, drop unused features, cross-OS install required.
- **[ADR-030](../../docs/adr/0030-edge-ui-per-feature-surface.md)** — Per-feature surface model: which features stay in Edge UI (camera live preview + audio test), which move to CP (cameras CRUD, PR config, services, logs, device info), which drop entirely (reverse-proxy network browser, setup wizard, services panel, logs page, top-level dashboard). New cmds enumerated. Captures pipeline pattern introduced.
- **[ADR-031](../../docs/adr/0031-webhook-endpoint-registry.md)** — Webhook endpoint registry as the first CP-wide fleet-config primitive.

## Implementation slices

Filed on GitHub Issues. Open list: https://github.com/emilejacobs/control-plane/issues

The 9 slices, in dependency order, are tracked under the `ready-for-agent` label.

## Out of scope (deferred)

- **Setup process redesign** — separate grilling session (own ADR when settled). Phases, Terminal vs CP-pushed, asset-number auto-assignment from CP, Mosyle relationship, cross-OS install module shape.
- **Audio-level diagnostics** (RMS / peak / clipping) — nice-to-have, defer to followup after audio-test slice lands.
- **Per-device log allow-list override** — existing Phase 2 followup.
- **Fleet camera gallery view** — dropped (not deferred).
