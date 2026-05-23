# syntax=docker/dockerfile:1.7
# audit-mirror container image. Short-lived Fargate task launched daily
# by EventBridge (ADR-023, Issue 28). Same multi-stage shape as cp-api /
# cp-ingest: Go static build → distroless/static runtime, non-root.

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/audit-mirror ./cmd/audit-mirror

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/audit-mirror /usr/local/bin/audit-mirror
USER nonroot
ENTRYPOINT ["/usr/local/bin/audit-mirror"]
