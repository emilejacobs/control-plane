# syntax=docker/dockerfile:1.7
# Dashboard container image. Server-rendered Next.js per ADR-020 (paradigm
# parsimony with the other Fargate services; not a static S3+CloudFront
# export). Multi-stage: deps → build → runtime. The runtime stage uses
# Next's "standalone" output so node_modules are not shipped wholesale.
#
# NEXT_PUBLIC_API_URL is baked into the JS bundle at build time — Next
# inlines NEXT_PUBLIC_* env vars during `next build`. The image-build
# workflow passes the canonical CP API hostname.

ARG NODE_VERSION=22-alpine

FROM node:${NODE_VERSION} AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

FROM node:${NODE_VERSION} AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ARG NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_API_URL=${NEXT_PUBLIC_API_URL}
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:${NODE_VERSION} AS runtime
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=3000
ENV HOSTNAME=0.0.0.0
# .next/standalone includes a minimal server.js + the production deps the
# build actually used; .next/static carries the hashed asset bundle.
COPY --from=builder --chown=node:node /app/.next/standalone ./
COPY --from=builder --chown=node:node /app/.next/static ./.next/static
USER node
EXPOSE 3000
CMD ["node", "server.js"]
