# ADR-015: Postgres multi-AZ from day one

**Status:** Accepted (2026-05-18)

**Context.** RDS Postgres holds the source-of-truth records (clients, sites, devices, services, commands, audit log, operators, notification targets). The CP has a 24/7 uptime requirement. Single-AZ Postgres has minutes-to-hours of unplanned downtime during AZ events; multi-AZ has automatic failover in ~60 seconds.

**Decision.** Postgres is multi-AZ from Phase 1. Configuration: `db.t4g.micro` initially (right-sized for fleet), GP3 storage, multi-AZ standby in a different AZ within the chosen region.

**Consequences.**
- (+) AZ failures do not take down the CP.
- (+) Backup, snapshot, and point-in-time recovery are first-class from day one.
- (-) +$13/mo for the standby instance + storage replica.
- (-) Multi-AZ Postgres lives in private subnets, which requires a NAT Gateway (+$33/mo) for outbound Fargate connectivity.

**Verification.** N/A — environmental/infra decision. Verified by Terraform/CDK config asserting `multi_az = true` and a manual failover smoke test as a Phase 1 acceptance step.
