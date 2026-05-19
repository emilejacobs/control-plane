# ADR-003: Hybrid command/data split — IoT Core + Tailscale

**Status:** Accepted (2026-05-05)

**Context.** Two distinct workloads need to cross the NAT boundary: (a) commands, telemetry, signed events, and (b) bulk data — Edge UI HTTP, camera snapshots, file pulls.

**Decision.** Use IoT Core for the control plane (commands, telemetry, heartbeat) and Tailscale for the data plane (Edge UI proxy, camera snapshots). The CP API service joins the tailnet via a Tailscale subnet-router Fargate task; clients (web, mobile) never join the tailnet themselves.

**Consequences.**
- (+) Each path is used for what it's good at — IoT Core for durable, audited control flow; Tailscale for streaming HTTP/binary data.
- (+) Doesn't reimplement HTTP/streaming over MQTT (which would be miserable).
- (+) Mobile clients work without tailnet enrollment.
- (-) Two paths to reason about, monitor, and document.
