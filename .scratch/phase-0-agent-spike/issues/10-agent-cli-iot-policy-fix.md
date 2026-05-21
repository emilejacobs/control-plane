# Issue 10 — agent-cli ↔ IoT policy: variable substitution + cmd-result subscribe

Status: ready-for-agent

## Parent

PRD: [`../PRD.md`](../PRD.md)

Discovered during the macOS smoke session ([results](../results-mac.md)) — the IoT Core provisioning runbook ([`docs/runbooks/phase-0-iot-core-provisioning.md`](../../../docs/runbooks/phase-0-iot-core-provisioning.md)) ships a policy that the `agent-cli` cannot use, despite the runbook claiming it can.

## What's wrong

The runbook's policy scopes:

- `iot:Connect` → `arn:aws:iot:*:*:client/${iot:Connection.Thing.ThingName}`
- `iot:Subscribe` → `arn:aws:iot:*:*:topicfilter/devices/${iot:Connection.Thing.ThingName}/cmd`

The runbook's section §8 (the heartbeat smoke test) tells the developer to run `agent-cli` with the device's own cert. It claims "the CLI shows up on IoT Core as the device itself, which is fine for Phase 0 testing." This is incorrect.

Two distinct problems:

1. **`${iot:Connection.Thing.ThingName}` only resolves when the connecting `client_id` equals the thing name.** AWS IoT uses the MQTT `client_id` to identify which thing's name to substitute. The `agent-cli` source ([`cmd/agent-cli/main.go`](../../../cmd/agent-cli/main.go)) sets `ClientID: "agent-cli-" + newID()[:8]` — a random per-invocation value. The variable resolves to empty, so the policy's `Connect` resource becomes literally `arn:aws:iot:*:*:client/`, which matches nothing. Result: implicit deny, connection rejected.

2. **`iot:Subscribe` does not include `cmd-result`.** Even after fixing problem (1), the CLI subscribes to `devices/<thing>/cmd-result` (so it can see command responses). The policy only allows Subscribe on `devices/<thing>/cmd`. Result: AWS IoT severs the connection during the Subscribe step.

Verified during the smoke session: with the policy as shipped, the CLI failed with `"connection lost before Subscribe completed"`. Broadening `Connect` to `client/*` and Subscribe to all three thing-scoped topic filters made it work end-to-end. Pub/sub remain thing-scoped via the topic ARN, so the cert principal still cannot operate outside its own topics.

## What to fix

Choose one of two paths (this issue is to pick + implement):

### Path A — Update the runbook + ship the broader policy

Lowest-risk, smallest change. Update `docs/runbooks/phase-0-iot-core-provisioning.md` §1 to ship the broader policy (Connect=`client/*`, Subscribe over all three thing-scoped topic filters). The cert principal is still doing real work — only the session-label (client_id) restriction is loosened, and only Subscribe gains `cmd-result` / `telemetry` (which Phase 0 needs for the CLI and the operator's eventual real-time view). Remove the misleading "the CLI shows up as the device" line.

### Path B — Issue a separate "controller" cert + policy for developer tooling

Cleaner long-term, more work. Mint a developer cert + a separate `UknomiControllerPolicy` with broader pub/sub (e.g. `devices/*/cmd` for sending commands across many devices, `devices/*/cmd-result` and `devices/*/telemetry` for observing). `agent-cli` uses that cert, not the device's. Device-side policy stays tight. This is the same pattern as the runbook's own Phase 1 note ("we'll issue a separate 'controller' cert").

Recommendation: **Path B is the right shape for Phase 1+**, but A is sufficient and faster for Phase 0 follow-up. Suggest doing A now (so the runbook is correct on its own terms) and tracking B as a Phase 1 task that the Terraform infra work owns.

## Acceptance criteria

- [ ] Decision recorded (A vs B) — likely A as the immediate fix.
- [ ] Runbook §1 policy document updated to reflect what actually works.
- [ ] Runbook §8 ("the CLI shows up as the device itself") text removed / replaced.
- [ ] If Path B chosen: a separate `UknomiControllerPolicy` is documented and the runbook §8 references it instead of the device cert.
- [ ] A fresh execution of the runbook from a clean account produces a CLI that connects on the first try, end-to-end.

## Blocked by

None — independent runbook fix.

## Related

- [Phase 0 smoke results](../results-mac.md) — where this was discovered.
- [`cmd/agent-cli/main.go`](../../../cmd/agent-cli/main.go) — the CLI's random client_id behaviour.
