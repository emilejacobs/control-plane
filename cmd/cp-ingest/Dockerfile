# syntax=docker/dockerfile:1.7
# cp-ingest container image. Same multi-stage pattern as cp-api: Go static
# build → distroless/static runtime, non-root. Build context is the repo
# root because cmd/cp-ingest imports internal/cp/*.

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/cp-ingest ./cmd/cp-ingest

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/cp-ingest /usr/local/bin/cp-ingest
USER nonroot
ENTRYPOINT ["/usr/local/bin/cp-ingest"]
