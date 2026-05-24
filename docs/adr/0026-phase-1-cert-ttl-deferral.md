# ADR-026: Phase 1 ships ~23-year device certs; ADR-013's 1-year TTL deferred to Phase 3

**Status:** Accepted (2026-05-24)

**Amends:** [ADR-013](./0013-agent-self-update-phase-3.md)

**Context.** ADR-013 specified `Phase 1 cert TTL = 1 year` to create a visible deadline that prevents cert-rotation work from being silently deferred forever. Wave 0's smoke test (per `docs/runbooks/phase-1-wave-0-bench.md` § 5d) surfaced that this is not what's actually being minted: the per-device view shows the deployed Mac's cert expiring in **8622 days** (~23.6 years), and the underlying AWS IoT default is around **January 2050** — both well outside the ADR-013 intent.

Root cause: AWS IoT's `CreateKeysAndCertificate` API does **not** accept a validity period. Every cert minted through this API gets AWS's default (multi-decade) lifetime. cp-api's `internal/cp/iotprovisioner.AWS.ProvisionDevice` uses this API. The 1-year intent in ADR-013 was a design goal, not a behaviour verified at implementation time, and the `Verification` field of that ADR remained `TBD — added at implementation`.

Real options for closing the gap:

1. **Self-signed CA inside cp-api.** Store a CA private key in Secrets Manager (KMS-protected), accept a CSR from the device at enrolment time, sign a 1-year leaf cert, register it with IoT Core via `RegisterCertificateWithoutCA`. Multi-day implementation: CA bootstrap, CSR generation in `mac-mini-rollout` module 11, key-rotation runbook, integration tests. Adds a load-bearing secret to operate.
2. **AWS Private CA / ACM PCA.** Pay AWS to host the CA. ~$400/month base cost per CA plus per-cert charges. cp-api requests AWS to sign with the validity it chooses. Less code than option 1; $4.8k/year fixed cost for a 63-device fleet that pays itself off only at much larger scale.
3. **Defer the 1-year intent.** Keep the existing `CreateKeysAndCertificate` flow. Make the divergence from ADR-013 explicit. Lean on manual revocation (`aws iot update-certificate --new-status REVOKED` plus `delete-thing` — exactly what Wave 0 used to clean three orphan certs) as the operational backstop until Phase 3 introduces a real cert-rotation flow on top of the ADR-013 self-update primitive.

The cost/benefit at current scale: 63 devices, all behind Tailscale, MQTT broker not exposed to the public internet, no third-party access. The 1-year TTL's defensive value is limiting blast radius of a leaked cert. At this size, manual revocation gives equivalent protection — slower, but the leakage rate is bounded by physical access to a known small inventory. Spending multi-day implementation + a critical secret + an ongoing key-rotation discipline on a 23×-factor TTL reduction is not the highest-leverage place to invest right now.

**Decision.** Phase 1 continues to ship ~23-year certs minted via `CreateKeysAndCertificate`. ADR-013's `Phase 1 cert TTL = 1 year` clause is **deferred to Phase 3**, where the agent self-update flow already introduces the machinery (signed manifests, supervised reload) that cert rotation will piggyback on. Phase 3's cert-rotation work picks up both the TTL reduction and the rotation primitive in one pass.

This is an amendment, not a supersession. ADR-013's agent self-update + auto-rollback decision and the Phase 3/4 phasing are unchanged. Only the "Phase 1 cert TTL = 1 year" sub-clause moves.

**Consequences.**
- (+) Zero implementation work in Phase 1. The Wave 0 deployment stays as-is; cert TTL is a documented divergence, not a defect to fix before Wave 1.
- (+) No new critical secret to operate (CA private key + rotation policy + KMS protection of the same).
- (+) Aligns with the Phase 1 fleet-size posture: small fleet + Tailscale + manual revocation = adequate.
- (-) A leaked device cert is usable for ~23 years instead of ~1 year. At current fleet size this is mitigated operationally, but the absolute risk grows with the fleet.
- (-) ADR-013's "visible deadline" goal evaporates: there's no longer an emergent fleet-wide expiry to force Phase 4 not to slip. Phase 3 self-update + cert rotation is now the only pressure to ship rotation; the team has to track it deliberately.
- (-) If the fleet grows materially (e.g. past ~200 devices) or starts handling materially more sensitive data before Phase 3 lands, this ADR needs revisiting.

**Verification.**
- `internal/cp/iotprovisioner/aws_test.go::TestAWSProvisionerCreatesThingAndCert` (the moto integration test) asserts `cert.ExpiresAt.After(time.Now())` — it deliberately does not pin a 1-year-from-now value, codifying that the long TTL is accepted as-is for Phase 1.
- The defect is also recorded in `docs/runbooks/phase-1-wave-0-bench.md` § 5d field notes for the 2026-05-24 Wave 0 smoke, so future readers see the gap, the rationale, and the Phase 3 pickup point in one trail.
