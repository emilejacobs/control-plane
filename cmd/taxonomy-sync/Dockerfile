# syntax=docker/dockerfile:1.7
# taxonomy-sync container image. Short-lived Fargate task fired daily
# by EventBridge at 00:05 UTC (ADR-033, Issue #18) and on demand by
# cp-api's POST /taxonomy/sync. Same multi-stage shape as audit-mirror.

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/taxonomy-sync ./cmd/taxonomy-sync

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/taxonomy-sync /usr/local/bin/taxonomy-sync
USER nonroot
ENTRYPOINT ["/usr/local/bin/taxonomy-sync"]
