FROM node:24.18.0-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS web

WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/package.json
RUN corepack enable && pnpm install --frozen-lockfile --filter @manyrouter/web
COPY web web
RUN pnpm --dir web build

FROM golang:1.26.1-alpine@sha256:2389ebfa5b7f43eeafbd6be0c3700cc46690ef842ad962f6c5bd6be49ed82039 AS builder

ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown
ARG GO_PROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=${GO_PROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/ ./internal/transport/http/ui_dist/
RUN go build -trimpath \
    -ldflags "-s -w -X github.com/evepupil/ManyRouter/internal/platform/buildinfo.Version=${BUILD_VERSION} -X github.com/evepupil/ManyRouter/internal/platform/buildinfo.Commit=${BUILD_COMMIT}" \
    -o /out/manyrouter ./cmd/manyrouter

FROM alpine:3.23.3@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659

ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown
LABEL org.opencontainers.image.source="https://github.com/evepupil/ManyRouter" \
      org.opencontainers.image.version="${BUILD_VERSION}" \
      org.opencontainers.image.revision="${BUILD_COMMIT}"
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S manyrouter \
    && adduser -S -G manyrouter manyrouter
WORKDIR /app
COPY --from=builder --chown=manyrouter:manyrouter /out/manyrouter /manyrouter
COPY --from=builder --chown=manyrouter:manyrouter /src/deploy/compatibility.yaml /app/deploy/compatibility.yaml
USER manyrouter
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/api/v1/healthz || exit 1
ENTRYPOINT ["/manyrouter"]
CMD ["all"]
