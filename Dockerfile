# ===== Build Stage 1: 前端 =====
# 前端构建与 CPU 架构无关；固定在 BuildKit 原生平台，避免 arm64 QEMU 下 npm ci 极慢/挂起。
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-builder
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# ===== Build Stage 2: Go 后端 =====
# 在原生构建平台运行 Go 编译器，通过 TARGETOS/TARGETARCH 交叉编译目标镜像。
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /build/web/dist ./cmd/server/web/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /build/zero-api ./cmd/server/

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

# 1455/54545/51121 为 CLIProxyAPI 远程 OAuth 回调端口。
EXPOSE 8080 8520 1455 54545 51121

ENTRYPOINT ["/app/zero-api"]
