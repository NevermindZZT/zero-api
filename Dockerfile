# ===== Build Stage 1: 前端 =====
FROM node:20-alpine AS frontend-builder
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# ===== Build Stage 2: Go 后端 =====
FROM golang:1.26-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /build/web/dist ./cmd/server/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/zero-api ./cmd/server/

# ===== Final Stage: 运行镜像 =====
# CLIProxyAPI 官方 Linux 二进制依赖 glibc（/lib64/ld-linux-x86-64.so.2）。
# 使用 Debian slim 而非 Alpine/musl，确保 sidecar 下载后可直接执行。
FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go-builder /build/zero-api .
COPY --from=go-builder /build/configs/config.yaml ./configs/config.yaml

VOLUME ["/app/data", "/app/certs"]

EXPOSE 8080 8520

ENTRYPOINT ["/app/zero-api"]
