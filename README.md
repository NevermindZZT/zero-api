# ⚡ zero-api

> 个人自用的大模型 API 中转站 — 轻量、美观、全功能

[![Build](https://github.com/NevermindZZT/zero-api/actions/workflows/build.yml/badge.svg)](https://github.com/NevermindZZT/zero-api/actions/workflows/build.yml)

zero-api 是一个基于 Go 的个人大模型 API 中转服务，集 **API 中转** 与 **MITM 代理** 于一体，支持多渠道多模型管理、使用统计与费用预估，开箱即用。

> **v1.0.0** — 首个稳定版本

---

## 功能特性

### 🔀 API 中转
- 兼容 OpenAI `/v1/chat/completions` 接口
- 自动按模型名路由到对应上游渠道
- 支持 **OpenAI 兼容**、**Anthropic**、**Google Gemini** 三种协议适配
- 请求/响应格式自动转换

### 🛡️ MITM 代理
- 集成 ModelProxy 的代理拦截功能
- 自动生成根 CA 证书，支持 HTTPS 流量劫持
- 智能识别 LLM 推理请求，仅拦截 AI 流量
- 非拦截域名直接透传，不影响正常上网

### 📊 渠道 & 模型管理
- 管理多个上游渠道（API Key、Base URL）
- 从上游自动拉取模型列表
- 为每个模型设置定价（输入/输出 $/1M tokens）
- 启用/禁用模型

### 📈 使用统计
- 记录每次请求的 Token 用量
- 自动按定价计算费用
- 按日/按模型/按渠道聚合统计
- 最近请求明细查看

### 🎨 管理面板
- Vue 3 + Naive UI 现代化暗色主题
- 仪表盘总览
- 渠道 / 模型 / 代理 / 统计 全功能页面
- 前端内嵌在 Go 二进制中，单文件部署

---

## 快速开始

### Docker Compose（推荐）

```bash
# 1. 克隆项目
git clone <your-repo-url> zero-api
cd zero-api

# 2. 启动
docker compose up -d

# 3. 访问管理面板
# http://localhost:8080
```

### 手动启动

```bash
# 1. 构建（Linux/macOS）
go build -o zero-api ./cmd/server/
# Windows
go build -o zero-api.exe ./cmd/server/

# 2. 运行
./zero-api        # Linux/macOS
.\zero-api.exe    # Windows

# 3. 可选：指定配置文件
ZERO_API_CONFIG=/path/to/config.yaml ./zero-api
```

### 前端开发

```bash
cd web
npm install
npm run dev    # 开发模式（需要后端在 8080 端口运行）
npm run build  # 构建后复制到 cmd/server/web/dist/
```

---

## 配置

默认配置文件 `configs/config.yaml`：

```yaml
server:
  host: "0.0.0.0"
  port: 8080

proxy:
  enabled: true
  host: "0.0.0.0"
  port: 8520
  intercept_domains:
    - "api.openai.com"
    - "api.anthropic.com"
    - "openrouter.ai"
    - "generativelanguage.googleapis.com"
  smart_intercept_domains: []

database:
  path: "data/zero-api.db"

log_level: "info"
```

可通过环境变量 `ZERO_API_CONFIG` 指定配置文件路径。

---

## 🔐 默认登录

访问管理面板 `http://localhost:8080`，使用以下凭据登录：

| 字段 | 默认值 |
|------|--------|
| 用户名 | `admin` |
| 密码 | `admin123` |

> ⚠️ **首次使用请务必修改密码！** 在配置文件 `configs/config.yaml` 中修改 `auth.password` 字段后重启服务：
> ```yaml
> auth:
>   enabled: true
>   username: admin
>   password: "你的新密码"
>   secret: "改为随机字符串"
> ```

### API 认证

所有 API 请求需在 Header 中携带 Bearer Token：
```
Authorization: Bearer <token>
```

通过登录接口获取 Token：
```bash
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}'
```

### 代理认证

配置系统 HTTP 代理时，如果启用了认证，需在代理设置中输入用户名和密码：
- 代理地址: `your-server:8520`
- 勾选"代理需要密码"
- 用户名/密码: 与 API 登录凭据相同

---

## 使用场景

### 场景 1：API 中转

客户端配置 zero-api 为 OpenAI API 端点：

```
Base URL: http://your-server:8080/v1
API Key: （可选，用于透传到上游）
```

在管理面板中添加渠道并同步模型，设定价后即可使用。

### 场景 2：MITM 代理

1. 将浏览器/系统 HTTP 代理设为 `your-server:8520`
2. 在管理面板 **代理设置** 中下载根 CA 证书
3. 安装 CA 证书到系统受信任根证书颁发机构
4. 之后访问 AI 提供商的请求会自动被拦截并路由到指定渠道

---

## 项目结构

```
zero-api/
├── cmd/server/          # Go 入口（含 embed 前端）
├── internal/
│   ├── adapter/         # 协议适配器（OpenAI/Anthropic/Gemini）
│   ├── config/          # 配置管理
│   ├── handler/         # HTTP 处理器
│   ├── middleware/      # 中间件（CORS）
│   ├── proxy/           # MITM 代理（证书/路由/服务器）
│   ├── store/           # SQLite 数据层
│   └── upstream/        # 上游模型同步
├── web/                 # Vue 3 前端
├── configs/             # 默认配置文件
├── certs/               # 运行时生成的证书
├── data/                # SQLite 数据库
├── Dockerfile
├── docker-compose.yml
└── Makefile
```

---

## 技术栈

| 层 | 技术 |
|---|---|
| 后端语言 | Go 1.22+ |
| HTTP 框架 | Gin |
| 数据库 | SQLite (modernc.org/sqlite) 纯 Go 实现 |
| 前端框架 | Vue 3 + TypeScript |
| UI 组件库 | Naive UI |
| 证书管理 | Go crypto/x509 标准库 |
| 容器化 | Docker + Docker Compose |

---

## 许可证

MIT
