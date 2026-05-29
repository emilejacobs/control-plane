# Control Plane — Architecture Diagram

A wiring diagram of the uKnomi Control Plane (CP) and the AWS technologies it
runs on, reflecting the **currently deployed** Phase-1/2 system (single US
region, account `523612763411`). It complements the narrative in
[`architecture.md`](./architecture.md); the source of truth for the
infrastructure is the Terraform under [`infra/`](../infra).

> The diagram is [Mermaid](https://mermaid.live) — it renders on GitHub, and
> you can paste it into mermaid.live to export a PNG/SVG for slides.

## System wiring

```mermaid
flowchart TB
    operator["Operator browser<br/>(dashboard) · future mobile app"]
    taxsrc["Upstream taxonomy<br/>HTTP API (clients/sites)"]
    gha["GitHub Actions<br/>(CI/CD, OIDC — no static keys)"]

    subgraph edge["Edge site — Mac mini / Pi / Radxa"]
        agent["uknomi-agent (Go)<br/>heartbeat · service-status · health-probes · cmd executor"]
        edgeui["Edge UI (Flask, Mac)<br/>127.0.0.1:5050"]
        tsd["Tailscale daemon"]
        agent --> edgeui
    end

    subgraph aws["AWS — us-east-1"]
        r53["Route 53<br/>control.uknomi.com"]
        acm["ACM (TLS cert)"]
        iot["AWS IoT Core<br/>MQTT over mTLS (X.509)"]
        rules["IoT Rules<br/>heartbeat · lifecycle · service-status · health-probes"]
        sqs["SQS ingest queues<br/>+ dead-letter queues"]
        ecr["ECR<br/>(cp-api · cp-ingest · dashboard images)"]
        eventbridge["EventBridge<br/>daily cron 00:05 UTC"]

        subgraph vpc["VPC"]
            subgraph pub["Public subnets"]
                alb["Application Load Balancer<br/>host-based routing"]
                nat["NAT gateway"]
            end
            subgraph priv["Private subnets (multi-AZ)"]
                subgraph fargate["ECS Fargate cluster"]
                    api["cp-api<br/>enrollment · auth (JWT) · device reads · operators · cmd publish"]
                    dash["dashboard<br/>(Next.js standalone)"]
                    ingest["cp-ingest<br/>SQSConsumer → presence/services/probes"]
                    tsr["tailscale-subnet-router"]
                    taxjob["taxonomy-sync<br/>(scheduled task)"]
                    auditjob["audit-mirror<br/>(scheduled task)"]
                end
                rds[("RDS PostgreSQL<br/>devices · operators · sites · services · probes · audit")]
            end
            vpce["VPC endpoints<br/>(IoT data · Secrets · ECR · S3 · logs)"]
        end

        s3[("S3<br/>audit-mirror · command-output · agent-dist")]
        kms["KMS<br/>(cmd-signing + secret encryption)"]
        sm["Secrets Manager<br/>bootstrap key · JWT key · TOTP key · DB DSN · TS auth"]
        cw["CloudWatch<br/>logs · metric filters · alarms"]
        sns["SNS<br/>(alarm notifications)"]
    end

    %% Operator / web path
    operator -->|HTTPS + JWT| r53 --> alb
    acm -. terminates TLS .- alb
    alb -->|control.uknomi.com| dash
    alb -->|api.control.uknomi.com| api
    dash -->|REST + bearer JWT| api

    %% Device telemetry path (asynchronous)
    agent -->|"publish: telemetry / service-status / health-probes"| iot
    iot -->|connect / disconnect| rules
    rules --> sqs --> ingest --> rds

    %% Command + enrollment path
    api -->|"provision thing + X.509 cert (enroll)"| iot
    api -->|"publish signed cmd → devices/{id}/cmd"| iot
    iot -->|"devices/{id}/cmd"| agent
    api --> rds

    %% Edge UI reverse proxy over tailnet
    api -->|Edge UI proxy| tsr
    tsr -. tailnet .-> tsd

    %% Scheduled jobs
    eventbridge --> taxjob
    eventbridge --> auditjob
    taxjob -->|daily pull| taxsrc
    taxjob -->|mirror clients/sites| rds
    auditjob -->|export prior day| rds
    auditjob --> s3

    %% Shared AWS dependencies
    api -.-> kms
    api -.-> sm
    ingest -.-> sm
    api --> s3
    api -.-> cw
    ingest -.-> cw
    dash -.-> cw
    cw --> sns
    priv -.->|egress| nat

    %% CI/CD
    gha -->|push images| ecr
    gha -->|update services| fargate
    fargate -.->|pull images| ecr
```

## AWS technologies and what each does

| AWS service | Role in CP |
|---|---|
| **ECS Fargate** | Runs every CP workload serverlessly (no EC2): the `cp-api`, `dashboard`, `cp-ingest`, and `tailscale-subnet-router` long-lived services, plus the `taxonomy-sync` and `audit-mirror` scheduled tasks. |
| **Application Load Balancer** | Public entry point. Host-based routing: `control.uknomi.com` → dashboard, `api.control.uknomi.com` → cp-api. Health checks `GET /healthz`. |
| **Route 53 + ACM** | DNS zone `control.uknomi.com`; ACM issues the DNS-validated TLS cert the ALB terminates. |
| **AWS IoT Core** | The device command/telemetry broker — MQTT over per-device X.509 mTLS. Issues each device's thing + certificate at enrollment. IoT **Rules** route inbound topics to SQS; lifecycle (connect/disconnect) events drive fast online/offline. |
| **SQS (+ DLQs)** | Buffers the asynchronous device→CP data path (heartbeat, lifecycle, service-status, health-probes). `cp-ingest` long-polls; poison messages route to dead-letter queues. |
| **RDS PostgreSQL** | System of record: devices, operators, sites/clients mirror, services, health probes, audit log, idempotency. Migrations (goose) run on service startup. |
| **S3** | Three buckets: daily audit-log mirror, command stdout/stderr, and signed agent binaries for self-update. |
| **KMS** | Customer-managed key (`alias/uknomi-cp`) for command signing and encrypting secret material. |
| **Secrets Manager** | Bootstrap (enrollment) key, JWT signing key, TOTP encryption key, RDS DSN, Tailscale auth key. |
| **CloudWatch + SNS** | All logs (per-service log groups), metric filters, and alarms (ALB 5xx, RDS CPU/storage, service running-count, SQS DLQ depth, per-probe red counts, job-staleness). Alarms notify via SNS. |
| **EventBridge** | Cron (00:05 UTC daily) that triggers the `taxonomy-sync` and `audit-mirror` ECS tasks. |
| **ECR** | Holds the `cp-api`, `cp-ingest`, and `dashboard` container images, tagged by git SHA + `latest`. |
| **VPC (subnets, IGW, NAT, VPC endpoints)** | Public subnets host the ALB + NAT; private subnets host Fargate + RDS. VPC endpoints keep IoT-data/Secrets/ECR/S3/logs traffic on the AWS backbone. |
| **IAM + OIDC** | Per-task roles (no shared secrets between tasks); GitHub Actions authenticates via OIDC federation (scoped to the repo's `main`) to push images and update services — no long-lived AWS keys. |
| **Tailscale subnet router** | A Fargate task on the tailnet; cp-api reverse-proxies operator traffic to each device's localhost Edge UI through it, so clients never join the tailnet. |

## Key flows (numbered to the diagram)

1. **Operator access** — Browser → Route 53 → ALB (TLS via ACM). Dashboard (Next.js) is a thin client; every data/action call goes to `cp-api` as REST with a bearer JWT. Auth is local: Argon2id password + mandatory TOTP, issued by cp-api (ADR-010) — no external IdP.
2. **Enrollment** — Install script calls `POST /enrollments` with the bootstrap key; cp-api provisions an IoT thing + X.509 cert and inserts the device row. (See the enrollment sequence in `architecture.md`.)
3. **Telemetry ingest (async)** — The agent publishes heartbeat/service-status/health-probes over MQTT; IoT Rules drop them onto SQS; `cp-ingest` consumes and writes Postgres (presence, service states, probe results). IoT connect/disconnect events give fast online/offline edges.
4. **Commands (Phase 3 design)** — cp-api records a pending command, signs it (Ed25519 via KMS), and publishes to `devices/{id}/cmd`; the agent verifies and executes; results return on `devices/{id}/cmd-result` and are ingested.
5. **Scheduled jobs** — EventBridge fires `taxonomy-sync` (pull clients/sites from the upstream HTTP API → mirror into Postgres, ADR-033) and `audit-mirror` (export the prior UTC day's audit log to S3) daily.
6. **CI/CD** — GitHub Actions builds images, authenticates via OIDC, pushes to ECR, and rolls the Fargate services.

## Notes / scope

- **Built and deployed:** everything in the diagram except the command channel (#4), which is designed but Phase 3.
- **Edge UI proxy** runs over Tailscale, a separate path from the IoT command/telemetry channel (ADR-003).
- Time-series metrics (CPU/mem/disk history) via **Timestream** are planned (ADR-016) and not yet wired; presence today is the `last_seen`/`is_online` columns in Postgres.

---

*A pre-rendered image of the diagram above lives at
[`img/cp-architecture.png`](./img/cp-architecture.png) for slides/exports. It
is generated from the Mermaid source with
`mmdc -i <this file's mermaid block> -o docs/img/cp-architecture.png -b white -s 2`
(`@mermaid-js/mermaid-cli`); regenerate it when the diagram changes.*
