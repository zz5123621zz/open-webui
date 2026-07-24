# syntax=docker/dockerfile:1.7

FROM node:22.23.1-alpine AS web-build
WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
RUN --mount=type=cache,target=/root/.npm \
    cd web && npm ci --no-audit --no-fund
COPY web/ ./web/
RUN cd web && npm run check && npm run build

FROM golang:1.26.5-alpine AS go-build
ARG VERSION=dev
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web-build /src/internal/httpapi/static ./internal/httpapi/static
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-build --chown=65532:65532 /out/server /app/server
COPY --chown=65532:65532 LICENSE /licenses/open-webui/LICENSE
COPY --chown=65532:65532 LICENSE_NOTICE /licenses/open-webui/LICENSE_NOTICE
COPY --chown=65532:65532 LICENSE_HISTORY /licenses/open-webui/LICENSE_HISTORY
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/server"]
CMD ["serve"]
