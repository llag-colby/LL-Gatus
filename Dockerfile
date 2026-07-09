# syntax=docker/dockerfile:1
# Build the go application into a binary
FROM golang:alpine AS builder
RUN apk --update add ca-certificates
WORKDIR /app
# Download modules first (cached unless go.mod/go.sum change) so source-only
# changes don't re-download the world.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . ./
# GIT_SHA is stamped into the binary so /api/v1/version reports the exact build.
ARG GIT_SHA=dev
# Cache mounts make incremental rebuilds fast (only changed packages recompile).
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X github.com/TwiN/gatus/v5/api.Version=${GIT_SHA}" -o gatus .

# Run the binary on an empty container
FROM scratch
COPY --from=builder /app/gatus .
COPY --from=builder /app/config.yaml ./config/config.yaml
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV GATUS_CONFIG_PATH=""
ENV GATUS_LOG_LEVEL="INFO"
ENV PORT="8080"
EXPOSE ${PORT}
ENTRYPOINT ["/gatus"]
