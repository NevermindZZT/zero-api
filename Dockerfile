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
COPY --from=frontend-builder /build/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/zero-api ./cmd/server/

# ===== Final Stage: 运行镜像 =====
FROM alpine:3.21
WORKDIR /app

# CA 证书和时区数据
RUN apk add --no-cache ca-certificates tzdata

COPY --from=go-builder /build/zero-api .
COPY configs/ ./configs/

EXPOSE 8080 8520
CMD ["./zero-api"]

# 复制默认配置
COPY --from=go-builder /build/configs/config.yaml ./configs/config.yaml

# 数据卷
VOLUME ["/app/data", "/app/certs"]

EXPOSE 8080 8520

ENTRYPOINT ["/app/zero-api"]
