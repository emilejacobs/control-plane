# AWS Cost Estimate

Monthly AWS infrastructure cost for the Control Plane at current fleet size (~63 devices), single US region, 24/7 uptime. Figures are list-price estimates as of 2026-05; actual cost will be ±20% depending on traffic patterns.

## Lean configuration (single-AZ, public subnets)

| Component | Configuration | Monthly cost |
|---|---|---:|
| AWS IoT Core | 63 devices × 24/7 connected, ~5M messages/mo | ~$2 |
| RDS Postgres | `db.t4g.micro`, 20 GB GP3, single-AZ | ~$13 |
| ECS Fargate (API) | 0.25 vCPU / 0.5 GB, 730 h | ~$7 |
| ECS Fargate (Dashboard) | 0.25 vCPU / 0.5 GB, 730 h | ~$7 |
| ECS Fargate (Tailscale router) | 0.25 vCPU / 0.5 GB, 730 h | ~$7 |
| ALB | 1 ALB + minimal LCU | ~$17 |
| Timestream | <1 GB ingest/mo, default retention | ~$3 |
| S3 | <50 GB total, low request volume | ~$2 |
| CloudWatch logs | Modest verbosity | ~$5 |
| Route 53 + ACM | 1 hosted zone, public certs | ~$1 |
| KMS | 1 key + minimal API calls | ~$1 |
| Secrets Manager | ~5 secrets | ~$2 |
| **Total** | | **~$67** |

## Recommended configuration (multi-AZ Postgres, private subnets)

| Component | Delta from lean | Monthly cost |
|---|---|---:|
| RDS Postgres multi-AZ | +1 standby instance + storage replica | +$13 |
| NAT Gateway | 1 NAT in single AZ for outbound from private subnets | +$33 |
| **Total** | | **~$113** |

## What scales with the fleet

- **IoT Core** scales linearly with device count and message rate. Even at 10× fleet, this stays under $20/mo.
- **Timestream** scales with telemetry volume. 30-day hot retention is the main knob.
- **S3** scales with command-output volume and snapshot caching. Trivially cheap at this fleet size.
- **Postgres** does not scale with device count meaningfully — workload is small. The next size up (`db.t4g.small`) is ~$26/mo, only needed if dashboard concurrency grows.

## What does **not** change with fleet growth

- ALB, ECS Fargate base, NAT Gateway, KMS, Route 53.

## Skipped on purpose

- **Cognito** — not used (see ADR-006). Would add ~$0 at our scale anyway.
- **WAF** — defer until public surface justifies it; CP behind ALB with auth on every endpoint is acceptable initial posture.
- **Multi-region** — not justified for an internal tool with US-only clients.

## Bottom line

Plan for **~$110/mo** for the always-on infrastructure with a sensible HA shape. That is materially less than one engineer-hour per month spent SSHing into individual devices.
