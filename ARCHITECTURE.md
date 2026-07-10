# zero-api 架构文档

> 本文档供其他 Agent 使用，包含完整的架构细节、关键设计决策和开发指南。

## 项目概述

zero-api 是基于 Go + Vue 3 的个人大模型 API 中转站，集 **API 中转** 与 **MITM 代理** 于一体。

### 两大核心功能

1. **API 中转**（端口 8080）：兼容 OpenAI `/v1/chat/completions` 接口，自动按模型名路由到上游渠道
2. **MITM 代理**（端口 8520）：拦截 HTTPS 流量，透明转发 LLM 推理请求
3. **MCP 技能管理**（挂载于主 API 端口 `/mcp`）：提供 AI Agent Skill 的发现、安装和使用能力

> MCP 工具详情及 Agent 配置方式请参见 [MCP.md](./MCP.md)。

---

## 技术栈

| 层 | 技术 | 版本 |
|---|---|---|
| 后端 | Go + Gin | 1.26+ |
| 数据库 | SQLite (modernc.org/sqlite) | 纯 Go，零 CGO 依赖 |
| 前端 | Vue 3 + TypeScript + Naive UI | Vite 构建 |
| 证书 | Go crypto/x509 标准库 | 自签发根 CA |
| 部署 | Docker + Docker Compose | |

### 关键 Go 依赖
- `github.com/gin-gonic/gin` — HTTP 框架
- `modernc.org/sqlite` — 纯 Go SQLite 驱动（不依赖 CGO）
- `gopkg.in/yaml.v3` — YAML 配置解析

---

## 目录结构

```
zero-api/
├── cmd/server/main.go              # 程序入口，双服务启动
├── internal/
│   ├── adapter/                    # 协议适配器
│   │   ├── adapter.go             #   接口定义 + modelDB 内置模型库
│   │   ├── openai.go              #   OpenAI 兼容适配器
│   │   ├── anthropic.go           #   Anthropic Messages API
│   │   └── gemini.go              #   Google Gemini API
│   ├── config/config.go           # 配置加载 + 默认值
│   ├── handler/                   # HTTP 处理器
│   │   ├── auth.go                #   登录认证
│   │   ├── api_key.go             #   API 密钥管理
│   │   ├── channel.go             #   渠道 CRUD
│   │   ├── model.go               #   模型管理 + 批量操作
│   │   ├── proxy.go               #   OpenAI 兼容转发（核心）
│   │   ├── proxy_config.go        #   代理配置 API
│   │   ├── sync.go                #   上游同步 + 测试连通
│   │   └── usage.go               #   使用统计查询
│   ├── middleware/
│   │   ├── auth.go                # Bearer Token 认证（仅拦截 /api/ 路径）
│   │   └── cors.go               # CORS 中间件
│   ├── proxy/                     # MITM 代理服务器
│   │   ├── cert.go               #   根 CA 生成 + 域名证书签发
│   │   ├── server.go             #   CONNECT 隧道 + TLS 拦截
│   │   ├── router.go             #   域名匹配 + LLM 请求识别
│   │   └── adapter.go            #   代理层适配器（转发+记录）
│   ├── store/                     # 数据层
│   │   ├── db.go                 #   SQLite 初始化 + 自动迁移
│   │   ├── service.go            #   Repository 聚合
│   │   ├── channel.go            #   渠道 Repository
│   │   ├── model.go              #   模型 Repository
│   │   ├── usage.go              #   用量 Repository
│   │   ├── api_key.go            #   API 密钥 Repository
│   │   └── proxy_config.go       #   代理配置 Repository
│   └── upstream/
│       ├── client.go             # HTTP 客户端（含 TLS 兼容修复）
│       └── syncer.go             # 上游模型列表同步
├── web/src/                       # Vue 3 前端
│   ├── api/index.ts              #   Axios API 客户端（含拦截器）
│   ├── components/Sidebar.vue    #   侧边栏导航
│   ├── router/index.ts           #   路由 + Auth Guard
│   ├── views/                    #   页面组件
│   │   ├── Login.vue             #     登录页
│   │   ├── Dashboard.vue         #     仪表盘（ECharts 图表）
│   │   ├── Channels.vue          #     渠道管理
│   │   ├── Models.vue            #     模型管理（批量操作）
│   │   ├── APIKeys.vue           #     API 密钥管理
│   │   ├── ProxySettings.vue     #     代理设置
│   │   └── Usage.vue             #     使用统计
│   └── App.vue                   #   根组件（暗色主题）
├── configs/config.yaml            # 默认配置
├── certs/                         # 运行时生成的证书（.gitignore）
├── data/                          # SQLite 数据库（.gitignore）
├── cmd/server/web/dist/           # Go embed 的前端产物
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── README.md
```

---

## 数据库 Schema

```sql
-- 渠道商
CREATE TABLE channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'openai',  -- openai/anthropic/gemini/openrouter
    base_url TEXT NOT NULL,
    api_key TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    priority INTEGER DEFAULT 99,          -- 0=最高优先级，越大优先级越低
    use_proxy INTEGER DEFAULT 0,          -- 是否通过出站代理转发
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 模型
CREATE TABLE models (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL,
    model_id TEXT NOT NULL,              -- 上游模型标识符
    display_name TEXT DEFAULT '',
    context_window INTEGER DEFAULT 0,
    max_output_tokens INTEGER DEFAULT 0,
    supports_vision INTEGER DEFAULT 0,
    supports_thinking INTEGER DEFAULT 0,
    supports_tools INTEGER DEFAULT 0,
    pricing_input REAL DEFAULT 0,        -- $/1M tokens
    pricing_output REAL DEFAULT 0,       -- $/1M tokens
    status TEXT NOT NULL DEFAULT 'active',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
    UNIQUE(channel_id, model_id)
);

-- 使用记录
CREATE TABLE usage_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER,
    model_id INTEGER,
    api_key_id INTEGER,                   -- 关联 API 密钥
    request_model TEXT NOT NULL,
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cache_hit_tokens INTEGER DEFAULT 0,   -- 缓存命中 tokens
    total_tokens INTEGER DEFAULT 0,
    latency_ms INTEGER DEFAULT 0,
    cost REAL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE SET NULL,
    FOREIGN KEY (model_id) REFERENCES models(id) ON DELETE SET NULL
);

-- 代理配置
CREATE TABLE proxy_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    intercept_domains TEXT DEFAULT '[]',
    smart_intercept_domains TEXT DEFAULT '[]',
    default_channel_id INTEGER,
    model_mappings TEXT DEFAULT '{}',
    mitm_all INTEGER DEFAULT 0,
    proxy_username TEXT DEFAULT '',
    proxy_password TEXT DEFAULT '',
    forward_proxy_url TEXT DEFAULT '',     -- 全局出站代理地址
    forward_proxy_user TEXT DEFAULT '',    -- 出站代理用户名
    forward_proxy_pass TEXT DEFAULT '',    -- 出站代理密码
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- API 密钥
CREATE TABLE api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL UNIQUE,           -- sk-xxx 格式
    name TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

---

## API 端点

### 认证
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/auth/login` | 登录（无需认证），返回 Bearer Token |

### 渠道管理
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/channels` | 渠道列表 |
| POST | `/api/channels` | 创建渠道 |
| PUT | `/api/channels/:id` | 更新渠道 |
| DELETE | `/api/channels/:id` | 删除渠道 |
| POST | `/api/channels/:id/test` | 测试渠道连通性 |
| POST | `/api/channels/:id/sync` | 从上游同步模型列表 |

### 模型管理
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/models` | 模型列表（?channel_id= 筛选） |
| GET | `/api/models/:id` | 模型详情 |
| PUT | `/api/models/:id` | 更新模型（定价等） |
| POST | `/api/models/:id/toggle` | 启用/禁用 |
| POST | `/api/models/batch` | 批量操作（enable/disable/delete） |
| DELETE | `/api/models/:id` | 删除模型 |

### API 密钥
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/api-keys` | 密钥列表 |
| POST | `/api/api-keys` | 创建密钥（返回 sk-xxx） |
| POST | `/api/api-keys/:id/toggle` | 启用/禁用 |
| DELETE | `/api/api-keys/:id` | 删除密钥 |

### 统计
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/stats/overview` | 总览统计（?api_key_id= 筛选） |
| GET | `/api/stats/daily` | 按日统计（?start=&end=） |
| GET | `/api/usage/records` | 最近记录（?api_key_id= 筛选） |

### 代理配置
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/proxy/config` | 代理配置 |
| PUT | `/api/proxy/config` | 更新代理配置 |
| GET | `/api/proxy/cert/download` | 下载根 CA 证书 |

### OpenAI 兼容接口
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/v1/models` | 模型列表（无需认证） |
| POST | `/v1/chat/completions` | 聊天补全（API Key 认证） |
| POST | `/v1/completions` | 文本补全 |

**认证方式**：所有 `/api/` 路径需 `Authorization: Bearer <token>`（登录获取）
**API Key 认证**：`/v1/*` 路径支持 `Authorization: Bearer sk-xxx`（管理面板创建）

---

## 内置模型数据库

`internal/adapter/openai.go` 中的 `modelDB` 包含 40+ 模型的元信息，用于上游 API 不返回元数据时自动填充：

- **DeepSeek**: deepseek-chat, deepseek-v4-flash/pro, deepseek-reasoner
- **OpenAI**: gpt-4o, o1, o3-mini 等
- **Claude**: claude-sonnet-4, claude-opus-4, claude-haiku-3.5
- **MiniMax**: m3, m2.7, m2.5
- **Kimi**: k2.7-code, k2.6, k2.5
- **GLM**: glm-5.2, glm-5.1, glm-5
- **Qwen**: qwen3.7-max/plus, qwen3.6/3.5-plus
- **MiMo**: v2-pro, v2-omni, v2.5-pro, v2.5, v2-flash
- **HY3**: hy3-preview

每个模型记录：`ContextWindow`、`MaxOutputTokens`、`SupportsVision`、`SupportsThinking`、`SupportsTools`

---

## 模型数据管理（三优先级合并）

模型元数据（上下文窗口、最大输出、支持特性、定价等）支持三种来源，按优先级从低到高排列：

### 优先级 1：内置数据库 + 配置文件默认值

**内置 modelDB**（`internal/adapter/openai.go`）：项目内置 40+ 流行模型的基础元信息。

**配置文件默认值**（`configs/config.yaml` 中的 `model_defaults` 段）：用户可在此预填模型的定价和规格：
```yaml
model_defaults:
  deepseek-chat:
    pricing_input: 0.5
    pricing_output: 2.0
    context_window: 65536
  deepseek-reasoner:
    pricing_input: 0.55
    pricing_output: 2.19
```

### 优先级 2：上游 API

同步模型列表时，如果上游 API 返回元信息（如 OpenRouter 返回 `context_length` 等字段），会覆盖 modelDB 和配置文件默认值。

### 优先级 3：用户手动编辑

用户在管理面板中编辑模型（修改定价、特性等），会标记 `user_modified=1`。之后任何同步操作**不会覆盖**用户已编辑过的模型字段。

### 合并逻辑

合并发生在 `internal/upstream/syncer.go` 的 `mergeModelInfo()` 中，按字段级别合并：

1. 从上游 API 获取模型列表
2. 对于每个模型，优先使用上游返回的值，空白字段从 modelDB 补全
3. 再以配置文件默认值覆盖
4. `Upsert` 到 DB 时，如果该模型已被用户编辑（`user_modified=1`），保留用户设置，不覆盖
5. `UPDATE` 操作会自动设置 `user_modified=1`

### 恢复上游数据

如需让模型重新接受同步更新，可通过 `DELETE` 删除后重新同步，或通过 API 清除 `user_modified` 标记。

---

## 已知问题 & 待改进

### 已修复的历史 Bug
1. **SQL NULL Scan 错误**：`models LEFT JOIN channels` 时 `c.name` 为 NULL → 使用 `COALESCE(c.name, '')`
2. **前端 SPA 路由 401**：auth 中间件拦截了非 API 路径 → 改为仅拦截 `/api/` 前缀
3. **TLS 握手超时**：Go 默认 Transport 对 Cloudflare 托管站点握手失败 → 使用 `&tls.Config{}` 显式配置
4. **模型同步 TLS 超时**：`http.Client` 默认无 Transport TLS 配置 → 使用 `upstream.NewHTTPClient()`
5. **侧边栏 emoji 乱码**：文件编码问题 → 改用 `String.fromCodePoint()` 渲染

### 待实现功能
- **流式响应转发**：当前 `/v1/chat/completions` 返回完整响应，不支持 SSE 流式传输
- **代理流式转发**：MITM 代理的 LLM 请求转发也不支持流式
- **渠道 API Key 测试**：`/api/channels/:id/test` 返回固定成功
- **模型按 ID 查询缓存**：`FindByChannelAndModelID` 有 COALESCE 问题未完全验证
- **前端图表**：仪表盘和使用统计页有 ECharts 趋势图和饼图
- **批量操作前端**：模型管理已有复选框 + 批量启用/禁用/删除

### 已实现功能
- **出站代理**：每个渠道可独立开启出站代理，全局配置代理地址/认证。转发请求时通过 `http.Transport.Proxy` 中转到上游 API

### 架构注意事项
- **嵌入前端**：`//go:embed web/dist/index.html web/dist/assets/*` 要求文件必须存在于编译时
- **数据库迁移**：使用 `ALTER TABLE ADD COLUMN` 增量迁移，会静默忽略已存在的列
- **认证白名单**：仅 `/api/auth/login`、`/assets/*`、`/`、`/v1/*` 不需认证
- **Go embed 路径**：必须相对于 `cmd/server/` 目录

---

## 开发指南

### 构建
```bash
# 前端构建
cd web && npm install && npm run build

# 前端产物复制到 embed 目录
Remove-Item -Recurse -Force cmd/server/web/dist/
Copy-Item -Recurse web/dist cmd/server/web/

# 后端编译
go build -o zero-api.exe ./cmd/server/
```

### 运行
```bash
./zero-api.exe
# API: http://localhost:8080
# Proxy: localhost:8520
# 默认登录: admin / admin123
```

### Docker
```bash
docker compose up -d
```

### 添加新模型到内置数据库
编辑 `internal/adapter/openai.go` 中的 `modelDB` map：
```go
"model-id": {ID: "model-id", ContextWindow: 128000, MaxOutputTokens: 16384, SupportsVision: true, SupportsThinking: true, SupportsTools: true},
```

### 添加新 API 端点
1. 在 `internal/handler/` 创建处理器
2. 在 `cmd/server/main.go` 注册路由
3. 前端在 `web/src/api/index.ts` 添加 API 调用
4. 前端在 `web/src/views/` 创建页面
5. 在 `web/src/router/index.ts` 添加路由
