# ADR-001: AWS IoT Core for command channel

**Status:** Accepted (2026-05-05)

**Context.** Devices sit behind NAT at client sites. CP must send commands and receive telemetry reliably. Three options were considered:

1. **Pure Tailscale pull** — CP joins the tailnet and HTTP-calls each device.
2. **Custom WebSocket gateway** — devices dial home to a CP-operated WebSocket server.
3. **Managed MQTT** — AWS IoT Core handles connection, auth, and queuing.

**Decision.** Use AWS IoT Core. Per-device X.509 mTLS, MQTT-over-WSS, device shadow for desired/reported state.

**Consequences.**
- (+) Free, scalable auth via per-device certs.
- (+) Device shadow pattern matches "is service X actually running?" semantics directly.
- (+) Cost negligible at fleet size — under $5/mo for 63 devices.
- (+) Survives Tailscale outages (independent path).
- (-) AWS lock-in for the command channel. Mitigated by MQTT being a portable protocol — agent could be repointed at any MQTT broker with cert re-issuance.
- (-) Small learning curve for operators not used to IoT Core.

Pure Tailscale pull was rejected because every command becomes synchronous, offline devices cause UI timeouts, and durable command queuing has to be re-built. Custom WebSocket gateway was rejected as engineering effort with no advantage at this scale.
