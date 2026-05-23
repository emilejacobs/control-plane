# syntax=docker/dockerfile:1.7
# cp-api container image. Multi-stage: build with the Go toolchain, ship a
# static binary on distroless/static so the final image carries no shell,
# no package manager, and runs as a non-root user. Build context is the
# repo root because cmd/cp-api imports internal/cp/*.

FROM golang:1.26-alpine AS builder
WORKDIR /src
# Cache go.mod download separately so source-only changes do not bust the
# module cache layer.
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# TARGETOS / TARGETARCH are populated automatically by buildx; defaults keep
# classic `docker build` working too.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/cp-api ./cmd/cp-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/cp-api /usr/local/bin/cp-api
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/usr/local/bin/cp-api"]
