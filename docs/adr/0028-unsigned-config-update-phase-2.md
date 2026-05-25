# ADR-028: Unsigned `config.update` command in Phase 2; signing arrives with the Phase 3 envelope

**Status:** Accepted (2026-05-24) — extended (2026-05-24) to cover Phase 2 slice 3's `log.tail` handler on the same blast-radius reasoning. The "fourth handler" framing throughout the ADR reads as "fourth and fifth" once log-tail ships.

**Context.**

Phase 2's [allow-list-overrides slice](../../.scratch/phase-2-allow-list-overrides/PRD.md) needs a downward CP→agent message: the operator edits a per-device service allow-list, and the agent must apply it without SSH.

Phase 0's command spike already established a working downward channel: agents subscribe to `devices/{id}/cmd`, the agent dispatcher (`internal/dispatcher/`) routes by `Command.Type`, and ACKs flow back on `devices/{id}/cmd-result`. Three handlers ship today against that channel: `heartbeat`, `service.status`, `service.restart`. **None are signed.** They were built as a Phase 0 spike to prove the channel works.

[ADR-013](./0013-agent-self-update-phase-3.md) commits Phase 3 to a signed-command pipeline: an API service signs payloads with an Ed25519 KMS key, the agent verifies before executing. ADR-013 explicitly scopes signing to the write-power commands (`service.restart`, `service.start`, `service.stop`, `run-script`, `reboot`, `agent.update`) because those can brick a device. The unsigned Phase 0 handlers are a known wart that Phase 3 absorbs.

The forcing question for Phase 2 slice 2: **does `config.update` (allow-list edits + cadence) need to wait for Phase 3 signing, or can it ship now as another unsigned handler that Phase 3 absorbs alongside the existing three?**

Alternatives considered:

1. **Pull Phase 3 signing forward** — implement Ed25519 KMS key + agent-side verify + key rotation in Phase 2. ~1 week of focused work, plus an irreversible decision on the key-rotation cadence and the manifest shape that today's Phase 3 design has not finalised.
2. **Use a polling model instead of push** — agent fetches `/agents/me/config` on an interval; CP returns the override. No new downward command. But: same TLS+cert auth surface as today (so equivalent attack model), introduces a poll interval as a freshness floor, breaks the "channel already works" reuse, and means Phase 3 has to migrate from poll → push anyway.
3. **Ship `config.update` unsigned now; Phase 3 wraps all downward commands in the signed envelope** — accepts one more unsigned handler temporarily. Phase 3's signed-command implementation already has to absorb the three existing Phase 0 handlers; absorbing a fourth is no additional design work.

Blast-radius analysis for an unsigned `config.update`:

| What an attacker who can publish to `devices/{id}/cmd` can do via this handler | Severity |
|---|---|
| Change which services get *reported* on for status | Low — operator sees the new list on the dashboard immediately; not a privilege escalation |
| Trigger frequent service-status publishes (interval lower bound: 30s per validator) | Low — bandwidth nuisance, surfaces in CloudWatch SQS metrics |
| Force the agent to write to `/var/uknomi/agent-config.json` | Low — handler only updates two whitelisted fields; existing config keys preserved |

The attack model assumes IoT-Core authn is intact (X.509 device cert validation), since the cmd topic is `iot:Subscribe`-restricted to the device's own thing-name. Compromise of *that* is a much bigger problem than slice 2 introduces. The marginal risk slice 2 adds over Phase 0's existing handlers is bounded to "more nuisance reporting"; it does not extend the attack surface meaningfully.

**Decision.**

`config.update` ships in Phase 2 as a fourth unsigned dispatcher handler alongside Phase 0's `heartbeat`, `service.status`, `service.restart`. Constraints:

- The handler is **strictly scoped to two fields**: `service_allow_list` and `service_status_interval`. Adding a third field is an ADR-amend decision, not an implementation decision — to prevent scope creep into authentication-relevant config (broker URL, cert paths) under the unsigned umbrella.
- Phase 3's signed-command pipeline (per ADR-013) wraps this handler in the same signed envelope as the other three. The handler logic stays; only the dispatch-time verification gate changes. No re-write expected.
- An audit-log row is written CP-side for every `config.update` API call (operator identity, target device, new effective list, correlation_id). This is the compensating control for the lack of cryptographic non-repudiation in Phase 2.

**Consequences.**

- (+) Slice 2 ships in days, not weeks. The downward-flow pattern (cmd → handler → cmd-result ACK → CP applied-at update) is established and battle-tested before Phase 3 inherits it.
- (+) Phase 3's signing work has *one* concrete consumer (the four extant handlers) rather than a hypothetical surface; design pressure on the signing layer comes from real usage.
- (-) For the Phase 2 → Phase 3 window, an attacker who can publish to a device's cmd topic can flip its reporting allow-list. Mitigation: the IoT-Core X.509 device-cert authn restricts cmd publish to the device's own thing-name; an external publisher would need stolen IAM credentials with `iot:Publish` on the broader topic space. Operationally bounded.
- (-) Two unsigned handlers turn into four (slice 2) and then five (slice 3's `log.tail`). The Phase 3 absorption work grows ~25% per addition in surface area. Acceptable given the per-handler logic is small and the blast-radius analysis re-applies to each: `log.tail` reads only allow-listed files (no arbitrary path traversal), so an attacker exploiting the unsigned channel can only exfil what the operator could already read via SSH.

**Verification.** TBD — added at implementation. Tests cover:

- API endpoint validates the two-field whitelist (rejects any extra key).
- Agent handler rejects payloads containing fields outside the whitelist.
- Audit-log row is written on every `service-config` PUT, including the operator identity and correlation_id.
- Phase 3 follow-on issue (when filed) explicitly references this ADR in its acceptance criteria.
