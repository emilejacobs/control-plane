# Issue 24 — mac-mini-rollout repo review and cleanup

Status: needs-triage
Type: AFK

## Parent

- User request, 2026-05-22, recorded during #11: "go through the
  mac-mini-rollout project, review and potentially rewrite (as we no
  longer use Zabbix and Teamviewer — or whatever was installed for
  remote desktop login)."
- CLAUDE.md: "Don't propose Zabbix integrations — explicitly de-prioritized."
- Architecture: the CP supersedes ad-hoc per-device remote desktop access
  (the whole point — "Replaces per-device SSH/Tailscale access").

## What to build

The `../mac-mini-rollout` repo has accumulated install modules tied to
tooling the project no longer uses. Now that #11 has put the repo under
git (baseline `2d7eeaf`), review and rewrite it against the current
direction:

- Drop `modules/04-zabbix.sh` — CP telemetry (#07/#08, future Timestream
  per ADR-016) replaces Zabbix.
- Drop `modules/05-anydesk.sh` — the CP (and Tailscale where strictly
  needed) replaces ad-hoc remote desktop.
- Audit the rest of the modules for staleness — at minimum: `09-webui`
  (the "Edge UI" lives on, being renamed per CLAUDE.md from "Talon" to
  "uKnomi Edge"); `10-s3-register` (does CP's registry obsolete this?);
  the `50-*`/`51-*`/`52-*` workload modules (transcriber, raven,
  plate-recognizer — keep, they are workloads, not infra).
- Remove the dropped modules' surfaces from:
  - `setup.sh` `get_phase_map`.
  - `.env.example` (drop `ZABBIX_API_TOKEN`, `ANYDESK_PASSWORD`).
  - `lib/config.sh` — `validate_config` and `validate_phase1_config`
    currently `fatal` if `ZABBIX_API_TOKEN` / `ANYDESK_PASSWORD` are not
    set; both are now dead-letters that block any deployment.
- Review and update `README.md` — the install flow now ends with the CP
  agent (#11) instead of Zabbix/AnyDesk.
- Consider extending `.gitignore` further once the dead artefacts are
  triaged (`audio-test/`, `audio-test-s3/`, `lpr-snapshots*` were
  excluded at `git init`; the wallpaper PNG, xlsx etc. were left in).

The big binary directories (`audio-test/`, `lpr-snapshots/`,
`lpr-snapshots.zip`) are already `.gitignore`'d but still on disk —
decide whether to delete them outright or keep as local-only artefacts.

## Acceptance criteria

*To be filled during triage. Suggested seed:*

- [ ] `04-zabbix.sh`, `05-anydesk.sh` removed; no remaining references in
  `setup.sh`, `.env.example`, `lib/config.sh`, `README.md`.
- [ ] Removed env vars are no longer required by Phase 1 / Phase 2
  validators — a deployment with only `TAILSCALE_AUTH_KEY` + AWS creds +
  CP config completes Phase 1.
- [ ] Each remaining module has a one-line comment in `setup.sh`'s phase
  map matching its current purpose.
- [ ] `README.md` describes the install flow ending in the CP agent
  (#11) and the Edge UI; Zabbix and AnyDesk are not mentioned.
- [ ] **Documentation updated.** CP `docs/architecture.md` /
  `CONTEXT.md` / ADRs unchanged unless the cleanup itself uncovers a CP
  architectural change (unlikely — this is sister-repo hygiene).

## Blocked by

- None structurally. Can run in parallel with the Wave issues (#12–#15).
  Best done before Wave 0 (#12) so the bench Mac install does not
  require dead env vars.

## Notes

- Cross-repo work: lives entirely in `../mac-mini-rollout`. Each step
  should be a separate commit in that repo so the cleanup is reviewable.
- The hostname-convention mismatch surfaced in #11 (CP regex
  `^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$` vs rollout's
  `macmini-staging-*` / `STORE-CHAIN-LOCATION-macmini`) is worth
  resolving as part of this cleanup — either repo can change, but only
  one should.
