# ADR-029: Edge UI rework — CP-authoritative, rewrite onto CP stack, drop unused features

**Status:** Accepted (2026-05-25)

**Context.**

The Edge UI is the Flask app at `mac-mini-rollout/webui/` running on every Mac Mini at `localhost:5050` (renamed from "Talon" to "uKnomi Edge"). It exists because the original [Project Requirements](../../../mac-mini-rollout/Project%20Requirements.md) listed a "Pie-in-the-sky nice-to-have" web UI for headless devices, and that nice-to-have shipped. Its architectural place was never first-principles designed — it grew to fill the gap of "we have headless Macs and someone on-site needs *some* way to interact with them."

Two things have since changed the framing:

1. **The Control Plane exists.** Phase 1 (registry + presence + dashboard + auth) shipped; Phase 2 (per-device service-status + allow-list overrides + log-tail) is live. CP can now answer most questions that Edge UI was created to answer (device state, service status, recent logs), and it does so for the whole fleet, behind one auth model, with audit.

2. **Track C scoping revealed concrete bugs and dead code in Edge UI.** The [§ Edge UI recon](../../mac-mini-rollout/webui/) walkthrough surfaced a 2108-line `app.py` with substantial surface that the operator never uses (the setup wizard) or that never worked (the reverse-proxy network browser, the audio test's 3× playback speed). The maintenance cost is real; the value delivered is much narrower than the surface implies.

The forcing question: **what should Edge UI be in the world where CP exists, and is it worth keeping the existing implementation or rewriting?**

Alternatives considered:

1. **Proxy Edge UI through CP** (the original Track C slice 5/6 plan). CP iframes / forwards / surfaces parts of Edge UI's existing pages. Lowest engineering cost short-term, but keeps two auth models, two design languages, two state stores, and the Python/Flask maintenance burden indefinitely. Doesn't fix the bugs.
2. **Delete Edge UI entirely.** Move every operator-facing function to CP. Tempting, but loses the hardware-bound functions that genuinely need to terminate on the device (RTSP live preview for camera angle verification, audio recording for transcriber permissions check). Those cannot move to CP without unacceptable bandwidth/latency cost.
3. **Rewrite Edge UI on the CP stack, scope it to hardware-bound functions only, drop everything else.** Higher one-time cost, but produces a small, focused, internally-consistent surface. Single auth, single design language, single deployment story.

**Decision.**

Edge UI is rewritten onto the CP stack with a deliberately reduced surface. CP becomes the source of truth for all persistent state; Edge UI is a thin, hardware-bound complement that surfaces or invokes CP state where relevant.

Concretely:

1. **Source of truth: CP.** All persistent state (cameras inventory, PR config, setup state, service allow-list, asset number, site assignment, etc.) lives in CP Postgres + IoT shadow. Edge UI does not own any persistent state. Where Edge UI displays data, it fetches from CP at request time.

2. **Stack: same as CP.** Next.js dashboard (matching `web/`'s design language and components), Go API (extend `cp-api` or a sibling service — to be decided alongside the repo-extraction work). The Flask + Jinja templates + Python venv all go away.

3. **Features dropped entirely (no CP equivalent needed):**
   - **Reverse-proxy network browser** (`/proxy/<target_ip>/<path>` + `browser.html`). Never worked reliably; the operator confirms the tab was hidden and is not used.
   - **Setup wizard UI** (`/setup`, `/activate`, `setup.html`, `activate.html`, and the PID/log polling for `setup-phase{N}.{log,pid}` + `activation.{log,pid}`). The operator never used the UI for setup — installs are run via Terminal. Removing the UI does not remove the underlying scripts (`setup.sh`, `activate.sh`, `modules/`); their redesign is the subject of a separate grilling session (see *Out of scope* below).

4. **Features moved to CP** (Edge UI surfaces a read view or hardware-bound action where useful, but does not own the data):
   - **Cameras inventory** — `cameras.json` retires; CP owns the table. Edge UI's role narrows to RTSP live preview during install (camera angle verification) — see ADR-030 for the per-feature split.
   - **Plate Recognizer config** — CP edits, pushes a `config.update`-shaped command to the agent, agent applies + restarts the container.
   - **Service control** — CP already owns read; `service.restart` lands in Phase 3 as a signed command. Edge UI's services panel does not survive.
   - **Logs viewer** — CP's `log.tail` (Phase 2 slice 3) covers file-based logs. Docker logs become a new agent log-kind. Edge UI's `/logs` page does not survive.
   - **Device info / dashboard** — already in CP at `/devices/{id}`. Edge UI's top-level dashboard does not survive.

5. **Features that stay local (hardware-bound)** — exact list and behavior to be finalized in the ADR-030 grilling; the candidates are: RTSP live preview for camera angle verification, audio test (rebuilt with the sample-rate bug fixed), and possibly a local network-scan trigger UI.

6. **Cross-OS install requirement.** The install process (separate from Edge UI but adjacent in the same sister repo) must support both macOS and Linux as a first-class target. Today's `modules/` are macOS-only (Homebrew, `launchctl`, AnyDesk, Mosyle). This is a relaxation of the prior "no Linux-specific investment" posture: the explicit framing is **optionality, not commitment** — we want the install path to remain portable so future hardware choices (away from Macs) don't require a from-scratch rewrite, but we are not investing in Linux-only features. Pi/Radxa devices remain operationally out of scope per [fleet direction](../../../.claude/memory/) memory.

7. **Repository extraction.** Edge UI moves out of `mac-mini-rollout/webui/` into its own GitHub repository, separate from the install scripts. Done **after** the per-feature split is settled (ADR-030), not before — extracting at today's surface would commit us to code we are about to delete.

8. **Auth.** With persistent operations moving to CP and Edge UI's surface shrinking to hardware-bound LAN-only operations, the Basic-auth model can be reconsidered. Concrete decision deferred to ADR-030.

**Out of scope for this ADR (to be decided in follow-up sessions):**

- Per-feature stay-local-vs-move-to-CP decisions for cameras, audio test, network scan, PR config, services, logs (→ ADR-030 grilling).
- Setup process redesign — Terminal-vs-CP-pushed, phase structure, asset-number auto-assignment from CP, Mosyle relationship, cross-OS install module shape (→ separate grilling, big enough to deserve its own ADR).
- Audio test sample-rate bug fix — separate diagnose track, ported into the new stack rather than fixed in Flask first.
- The PR config update wire format — likely re-uses ADR-028's `config.update` envelope shape, but the field whitelist will need an explicit ADR amendment if PR config goes through it.

**Consequences.**

- (+) One stack across CP and Edge UI. Operators see one design language; engineers maintain one auth model, one component library, one test framework, one deployment pipeline.
- (+) Persistent state has one home (CP Postgres). No more reconciling `cameras.json` on each device against any central record; the central record *is* the record.
- (+) Dropping the reverse proxy + setup wizard + services panel + dashboard + logs page eliminates ~1500 LoC of Python + Jinja and a class of bugs we have been carrying.
- (+) Cross-OS portability gets locked in at design time rather than retrofitted later if/when we move away from Macs.
- (-) The rewrite has up-front cost. Estimated at multiple weeks across the per-feature slices (camera RTSP component, audio test rebuild, install-time setup UI replacement). Mitigated by the operator only using ~5 features today — the actual operational surface is smaller than `app.py`'s line count suggests.
- (-) During the migration window, the operator must use *both* the old Edge UI (for hardware-bound functions until the rewrite catches up) and CP (for everything else). We will not run a Flask + Next.js dual-stack permanently; the carve-out is sequenced so each feature flips in one slice.
- (-) The install module (`mac-mini-rollout/modules/09-webui.sh`) and the launchd plist (`launchd/com.uknomi.webui.plist`) will be rewritten when the new Edge UI lands. The launchd-as-root + Python venv pattern goes away.

**Verification.**

- ADR-030 (Edge UI per-feature surface model) lands as the next ADR; references this one as the parent scope decision.
- The setup-process redesign ADR lands as a separate document and references this one for the dropped UI piece.
- When the new Edge UI ships, `webui/` is removed from `mac-mini-rollout` and the `09-webui.sh` install module is rewritten (or removed if the new app installs out-of-band).
- The dashboard's design language (component library, color tokens, typography) is reused verbatim in the new Edge UI — verified by a manual visual compare of the first slice's first screen against `web/app/devices/[id]/page.tsx`.
