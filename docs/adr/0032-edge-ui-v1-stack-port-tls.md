# ADR-032: Edge UI v1 — Next.js+Go from the start, port 5051, plain HTTP, parallel install with old Flask

**Status:** Accepted (2026-05-26)

**Context.**

[ADR-029](./0029-edge-ui-rework-scope.md) settled the scope: Edge UI rewrites onto the CP stack with a deliberately reduced surface. [ADR-030 § 8](./0030-edge-ui-per-feature-surface.md) describes the final shape ("a Next.js app served by a small Go binary") and § 1 sketches the URL as `https://<host>.<tailnet>/preview/<camera_id>`. Implementing the first slice ([issue #4](https://github.com/emilejacobs/control-plane/issues/4) — camera live preview + scaffolding) surfaced several v1-vs-final tradeoffs that the prior ADRs left implicit.

Five questions had to be answered before any code could land:

1. **Stack at the v1 boundary** — Next.js from the start, or Go-only HTML/MJPEG for the single-route slice with Next.js layered when the audio-test interactive UI lands (#10)?
2. **Port** — reuse 5050 (the old Flask Edge UI's port) or pick a new one?
3. **TLS** — honor § 1's `https://` URL with `tailscale cert` machinery, or plain HTTP on the new port?
4. **Tailscale interface fail-mode** — refuse to start when the tailnet interface isn't detectable, or fail-open (loopback always; tailnet best-effort)?
5. **LAN-IP fallback hint** — implement the § 1 hint now (requires new telemetry + DB column) or defer?

**Decisions.**

1. **Stack: Next.js + Go from the start.** The Go binary embeds the statically-exported Next.js bundle (`output: 'export'`) via `//go:embed`, serves the SPA at `/`, and owns the `/preview/<camera_id>/stream` route. Rationale: even though the v1 slice is one MJPEG route, the audio test in #10 needs interactive UI, and committing to Next.js now avoids a stack flip when that lands. The cost — one extra build step (`npm run build` before `go build`) — is a one-time Makefile concern.

2. **Port: 5051.** Old Flask Edge UI stays on 5050 throughout the rewrite. New plist (`com.uknomi.edge-ui`) lives alongside the old one (`com.uknomi.webui`) — both `KeepAlive=true`, no collision. The Flask app is removed in a follow-up cleanup slice once every operator-relevant feature is verified on the new app.

3. **Plain HTTP on `:5051` for v1.** Tailnet membership is already the perimeter; ADR-030 § 8's "no auth" framing comes from that same premise. `tailscale cert` would add scope (cert provisioning at install time, renewal scaffolding, error handling for cert lookup) without changing the perimeter story. TLS lands as its own slice when the operational benefit (e.g. avoiding browser HTTP warnings) outweighs the maintenance cost.

4. **Fail-open Tailscale interface detection.** The binary binds `127.0.0.1` unconditionally and the tailnet interface address best-effort, logging a warning if none is found. Rationale: launchd starts the binary early; tailscaled may not be up yet. Refusing-to-start would make startup-ordering bugs into outages, and KeepAlive would spin. A late-binding watcher goroutine is a follow-up.

5. **LAN-IP fallback hint deferred.** [`devices` has no `lan_ip` column today](../../internal/cp/registry/) and telemetry doesn't publish one. The hint described in ADR-030 § 1 ("can't reach over tailnet? Try `http://192.168.x.y:5051/preview/<cam_id>`") requires new telemetry plumbing — a separate slice. v1 surfaces only the tailnet-hostname URL; operators on the same L2 as the device can type the LAN IP themselves until the followup lands.

**Out of scope for this ADR (covered by follow-ups):**

- Audio test (issue #10).
- Old Flask Edge UI cleanup (separate cleanup slice once new app is verified across features).
- Real LAN-IP telemetry + DB column.
- `tailscale cert`-based TLS (separate slice).
- Late-binding tailnet watcher.

**Consequences.**

- (+) One stack across CP dashboard and Edge UI — same Next.js/React tooling, same component conventions, same vitest harness.
- (+) Port-decoupled coexistence means the rewrite ships incrementally without breaking the bench Mac's current operator workflows. The migration window is bounded by feature-by-feature parity, not a flag-day cutover.
- (+) Fail-open Tailscale detection means startup ordering quirks don't cascade into operator-visible outages.
- (-) ADR-030 § 1's `https://<host>.<tailnet>/...` URL shape is narrowed to `http://<host>:5051/...` for v1. CONTEXT.md updated accordingly; ADR-030 § 1 stays as the long-term target with a footnote pointing here.
- (-) `npm run build` → `go build` ordering creates a build-pipeline asymmetry vs the other Go binaries (which build directly). Mitigated by a Makefile target that owns the dependency.
- (-) The "no LAN-IP hint in v1" decision means operators on a misbehaving tailnet have to know the device's LAN IP through other means until the follow-up lands.

**Verification.**

- The first slice ([issue #4](https://github.com/emilejacobs/control-plane/issues/4)) ships a working `/preview/<camera_id>` MJPEG stream on port 5051 alongside the old Flask app on 5050 on the bench Mac.
- ADR-030 § 8's "Next.js app served by a small Go binary" wording reads accurately against the shipped binary.
- A follow-up issue tracks the LAN-IP telemetry hint with explicit acceptance criteria.
