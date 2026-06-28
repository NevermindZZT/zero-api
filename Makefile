.PHONY: build run clean dev docker-build docker-run

# 构建
build:
	go build -o zero-api.exe ./cmd/server/

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o zero-api-linux ./cmd/server/

# 运行
run:
	./zero-api.exe

# 开发模式（自动重载）
dev:
	@echo "使用: go run ./cmd/server/"

# Docker
docker-build:
	docker compose build

docker-run:
	docker compose up -d

docker-stop:
	docker compose down

# 测试
test:
	go test ./...

# 清理
clean:
	rm -f zero-api.exe zero-api-linux
	rm -rf data/*.db
	rm -rf certs/*

# 前端（Phase 4）
frontend-dev:
	cd web && npm run dev

frontend-build:
	cd web && npm run build
