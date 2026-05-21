# Phase 1 — Registry, presence, and enrollment

Scratch directory for Phase 1 work. The phase itself is defined in [`docs/roadmap.md` § Phase 1](../../docs/roadmap.md). Estimate: 4–6 weeks.

## Objective (from roadmap)

Replace the device spreadsheet (`uknomi-macmini-devices.xlsx`) with a live online/offline view of the whole fleet.

## Deliverables (from roadmap)

- AWS infra in Terraform/CDK: VPC, ALB, RDS Postgres (multi-AZ), Fargate cluster, IoT Core CA, S3 buckets, KMS keys, Tailscale subnet router task.
- API service skeleton with auth (NextAuth Entra ID + Credentials — superseded by ADR-010, now local credentials only), enrollment endpoints, device list/detail endpoints, WebSocket for live updates.
- Next.js dashboard: login, fleet view (online/offline, by client/site), per-device view (read-only).
- New `mac-mini-rollout/modules/11-cp-agent.sh` that installs the agent and enrolls.
- One-page Linux install script for Pi/Radxa enrollment.
- Roll out to all 63 devices, read-only.

## Where this PRD-vs-issues split lives

Phase 0's PRD (`.scratch/phase-0-agent-spike/PRD.md`) anchored a single tight scope. Phase 1 is broader — a full PRD will land before significant work starts. For now this directory holds discrete starter issues that are unambiguously Phase 1 work, beginning with the infra-as-code track.

## Issues so far

- [Issue 01 — Terraform infra: bring Phase 0 IoT Core resources under code](./issues/01-terraform-iot-core.md)
