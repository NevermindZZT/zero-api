# zero-api 架构文档

> 本文档供其他 Agent 使用，包含完整的架构细节、关键设计决策和开发指南。
> 面向 AI 编码代理的速查版见 [AGENTS.md](./AGENTS.md)，MCP 使用说明见 [MCP.md](./MCP.md)。

## 项目概述

zero-api 是基于 Go + Vue 3 的个人大模型 API 中转站，集 **API 中转**、**MITM 代理**、**MCP 技能管理** 三大功能于一体。

### 三大核心功能

1. **API 中转**（端口 8080）：兼容 **OpenAI** `/v1/chat/completions`、**Anthropic** `/v1/messages`、**OpenAI Responses** `/v1/responses` 三种下游协议，按模型名自动路由到四种上游协议渠道（OpenAI 兼容 / Anthropic / Gemini / Responses），协议转换全组合支持，协议一致时原样透传
2. **MITM 代理**（端口 8800，配置项 `proxy.port`）：拦截 HTTPS 流量，智能识别 LLM 推理请求，非 LLM 流量直接透传
3. **MCP 技能管理**（挂载于主 API 端口 `/mcp`）：提供 AI Agent Skill 的发现、安装、组合能力

> MCP 工具详情及 Agent 配置方式请参见 [MCP.md](./MCP.md)。

---

## 技术栈

| 层 | 技术 | 说明 |
|---|---|---|
| 后端 | Go + Gin | 1.26+ |
| 数据库 | SQLite (modernc.org/sqlite) | 纯 Go，零 CGO 依赖 |
| 前端 | Vue 3 + TypeScript + Naive UI | Vite 构建 |
| MCP | mark3labs/mcp-go | Streamable HTTP 协议 |
| 证书 | Go crypto/x509 标准库 | 自签发根 CA |
| 部署 | Docker + Docker Compose | |

### 关键 Go 依赖
- `github.com/gin-gonic/gin` — HTTP 框架
- `modernc.org/sqlite` — 纯 Go SQLite 驱动（不依赖 CGO）
- `gopkg.in/yaml.v3` — YAML 配置解析
- `github.com/mark3labs/mcp-go` — MCP Streamable HTTP 服务器

---

## 目录结构

```
zero-api/
├── cmd/server/main.go              # 程序入口：路由注册 + 双服务启动
├── internal/
│   ├── adapter/                    # ⭐ 协议适配层（最核心）
│   │   ├── adapter.go             #   Adapter 上游接口 + modelDB 内置模型库 + NewAdapter()
│   │   ├── openai.go              #   OpenAI 兼容上游（规范格式，基准实现）
│   │   ├── anthropic.go           #   Anthropic Messages 上游
│   │   ├── gemini.go              #   Google Gemini 上游
│   │   ├── responses.go           #   OpenAI Responses API 上游（/v1/responses）
│   │   ├── downstream.go          #   DownstreamAdapter 下游接口 + StreamConverter + 透传适配器
│   │   ├── downstream_anthropic.go #   下游 Anthropic ↔ 规范格式
│   │   ├── downstream_responses.go #   下游 Responses ↔ 规范格式
│   │   └── upstream_stream.go     #   上游 SSE → OpenAI 规范格式 SSE（含 Gemini 流）
│   ├── config/config.go           # 配置加载 + ModelPresets 预设文件管理
│   ├── handler/                   # HTTP 处理器
│   │   ├── proxy.go              #   ⭐ 核心转发：handleCompletion / streamResponse / PassthroughEndpoint
│   │   ├── circuit_breaker.go    #   渠道熔断器（冷却+探测恢复）
│   │   ├── channel.go            #   渠道 CRUD + 启停
│   │   ├── model.go              #   模型管理 + 批量操作 + 导入导出
│   │   ├── sync.go               #   上游同步 + 测试连通 + 预设热重载
│   │   ├── usage.go              #   使用统计查询（按日/模型/年度热力图）
│   │   ├── api_key.go            #   API 密钥管理
│   │   ├── auth.go               #   登录认证
│   │   ├── proxy_config.go       #   代理配置 API + 证书下载
│   │   ├── mcp_config.go         #   MCP 状态 + GitHub Token 配置
│   │   ├── skill.go              #   技能管理（GitHub 导入/上传/更新检查）
│   │   ├── skill_combination.go  #   技能组合管理
│   │   └── database.go           #   数据库备份/恢复
│   ├── mcp/server.go             # MCP Streamable HTTP 服务器 + 工具注册
│   ├── middleware/
│   │   ├── auth.go               # Bearer Token 认证（仅拦截 /api/ 路径）
│   │   └── cors.go               # CORS 中间件
│   ├── pricing/                  # 定价规则引擎
│   │   ├── rule.go              #   规则类型定义 + 校验（time_range / token_tier）
│   │   └── engine.go            #   匹配引擎（first-match-wins）
│   ├── proxy/                    # MITM 代理服务器
│   │   ├── cert.go              #   根 CA 生成 + 域名证书签发
│   │   ├── server.go            #   CONNECT 隧道 + TLS 拦截 + 透传
│   │   ├── router.go            #   域名匹配 + LLM 请求识别（IsLLMRequest）
│   │   └── adapter.go           #   代理层适配器（模型映射+参数注入+响应重写+计费）
│   ├── store/                    # SQLite 数据层
│   │   ├── db.go               #   建表 + 增量迁移 + usage_daily 回填
│   │   ├── service.go          #   Repository 聚合
│   │   ├── channel.go model.go usage.go api_key.go proxy_config.go
│   │   ├── skill.go skill_fs.go skill_combination.go   # MCP 技能存储
│   │   └── usage.go            #   用量批量写入缓冲 + 日聚合
│   └── upstream/
│       ├── client.go            # HTTP 客户端（TLS 兼容 + 出站代理 + 超时）
│       └── syncer.go            # 上游模型列表同步（三优先级合并）
├── web/src/views/               # Vue 页面：Dashboard/Channels/Models/APIKeys/Usage/
│                                #   ProxySettings/Settings/Database/MCPSettings/Skills/
│                                #   SkillCombinations/ChatTest/ForwardProxy
├── configs/config.yaml           # 默认配置
├── data/model-presets.json       # 模型预设文件（可热重载）
├── certs/                        # 运行时生成的证书（.gitignore）
├── data/                         # SQLite 数据库（.gitignore）
├── cmd/server/web/dist/          # Go embed 的前端产物
├── Dockerfile
├── docker-compose.yml
└── Makefile
```

---

## ⭐ 核心架构：协议适配体系

### 规范格式（Canonical Format）

zero-api 内部统一使用 **OpenAI Chat Completions 格式**作为规范格式。所有协议转换围绕它进行：

```
客户端协议 (下游)  ──RequestToCanonical──▶  规范格式  ──ConvertRequest──▶  上游协议 (渠道)
客户端协议 (下游)  ◀──ResponseToDownstream──  规范格式  ◀──ConvertResponse──  上游协议 (渠道)
```

### 上游适配器 `Adapter`（internal/adapter/adapter.go）

每个渠道类型（`openai` / `anthropic` / `gemini` / `responses`）实现一个上游适配器，通过 `NewAdapter(channelType)` 工厂创建。注意 `openrouter` 是历史遗留值，数据库迁移时已统一并入 `openai`。接口方法：

- `GetModelsURL` / `ParseModelsResponse` — 模型列表拉取
- `GetChatURL` — 聊天端点 URL
- `ConvertRequest` / `ConvertResponse` — 请求/响应体转换
- `ExtractUsage` — 用量提取
- `NewStreamConverter` — 返回上游 SSE → 规范格式 SSE 的转换器；**上游本身是 OpenAI 兼容格式时返回 nil（原样透传）**

### 下游适配器 `DownstreamAdapter`（internal/adapter/downstream.go）

- `Protocol()` — 下游协议名（openai / anthropic / responses）
- `IsPassthrough()` — 是否透传模式
- `RequestToCanonical` — 下游请求 → 规范格式
- `ResponseToDownstream` — 规范格式响应 → 下游（非流式）
- `NewStreamConverter` — 创建流式 SSE 转换器（规范格式 → 下游格式）
- **透传模式**：当下游协议命中**模型支持的协议**时，用 `NewPassthroughDownstreamAdapter(downstreamProtocol)` 返回的适配器原样转发，**不做任何转换**（避免丢失 Anthropic 的 tools/thinking 等原生特性）。透传判断在 `tryForward` 中完成：`matchedModel.SupportsProtocol(downstreamProtocol, ch.Type)`

### 模型级协议支持（Model-level Protocols）

- `models.protocols` JSON 列（如 `["openai","responses"]`），空数组 = 继承渠道 type（存量数据零迁移）
- `Model.SupportsProtocol(protocol, channelType)` 判断模型是否支持某协议（`EffectiveProtocols` 声明优先，否则继承渠道）
- **核心原则：下游请求使用的如果是模型支持的 API 接口，一定直接转发（透传），不过协议转换**
- 模型不支持下游协议时走规范格式转换兜底（不拒绝请求）
- 透传 URL 由**真实下游协议**决定：`adapter.ProtocolURL(base, protocol)` → `/v1/chat/completions` | `/v1/messages` | `/v1/responses`（base_url 以 `/v1` 结尾自动去重）
- **模型级协议 URL 覆盖**：`models.protocol_urls` JSON 列（如 `{"anthropic":"https://host/anthropic/v1/messages"}`，value=完整 URL），`Model.ProtocolURL(protocol, channelBaseURL)` 优先返回模型配置，未配置回退渠道 base_url 拼接——用于同一模型不同接口不在同一地址的场景
- ⚠️ 透传判断与 URL 构造必须使用**替换前**保存的 `downstreamProtocol`（透传后 `passthrough.Protocol()` 会变）
- gemini 渠道无下游协议入口，始终走转换（现有 `ch.Type == "gemini"` 分支不动）
- 功能类接口透传（`/v1/embeddings` 等）候选条件：`m.SupportsProtocol("openai", ch.Type)`
- 代理模块（`internal/proxy/adapter.go`）：从客户端请求路径推导下游协议（`downstreamProtocolFromPath`），模型协议命中时透传（仅模型映射/参数注入，不过协议转换）

### 流式转换 `StreamConverter`

事件级 SSE 转换接口，逐行处理：
- `Convert(line []byte) []byte` — 处理一行 SSE 数据；返回 `nil` 表示该行不转发（如 `event:` 行、`ping` 等）
- `Finish() []byte` — 流结束时补发收尾事件（如 `[DONE]`）；返回 `nil` 表示无需补发

`upstream_stream.go` 实现上游 SSE → 规范格式 SSE 转换（Anthropic 事件状态机、Gemini 流），下游转换器（规范格式 → Anthropic / Responses）在各自的 downstream 文件中。

---

## 请求处理主链路

```
/v1/chat/completions ─┐
/v1/messages ─────────┤  handler/proxy.go  handleCompletion(c, rawBody, downstream)
/v1/responses ────────┘
        │
        ▼
1. 读取 body → 解析 model → resolveAndValidateAPIKey
2. 按 model_id 查找启用模型 → 候选渠道列表（同模型多渠道按 priority 排序）
3. 对每个候选渠道（熔断器检查 + 故障切换）：
   a. 透传判断：matchedModel.SupportsProtocol(downstreamProtocol, ch.Type) → passthrough 或常规适配器
   b. downstream.RequestToCanonical → adapt.ConvertRequest → 构造上游请求（透传时 URL 用 ProtocolURL）
   c. 转发（流式 streamResponse / 非流式）
   d. 响应：adapt.ConvertResponse → downstream.ResponseToDownstream
   e. recordUsage（异步 goroutine）：提取 usage → pricing 引擎计费 → 批量写库
4. 全部失败 → 502；成功 → breaker.RecordSuccess(ch.ID)
```

**功能类接口透传**（`/v1/embeddings`、`/v1/images/*`、`/v1/audio/*`、`/v1/moderations`、`/v1/batches`）：走 `PassthroughEndpoint`，候选条件为 `m.SupportsProtocol("openai", ch.Type)`（模型支持 openai 协议即可，不再限定 openai 渠道），请求体原样转发、响应原样返回，客户端请求路径即上游路径（注意 base_url 以 `/v1` 结尾时要去掉再拼接路径）。

---

## 熔断与故障切换（internal/handler/circuit_breaker.go）

- 请求失败 → 冷却 5 分钟（cooldown 状态）
- 冷却到期 → probing 状态，命中时先发轻量探测请求验证健康（用渠道的 `test_model`，可配置 `probe_api_key`）
- 探测通过 → 恢复 normal 继续当前请求；失败 → 重新冷却
- 连续失败递增冷却：5min → 10min → 20min → 40min（上限）
- 成功请求调用 `RecordSuccess` 重置
- 全局开关 `proxy_config.failover_enabled` + 渠道独立开关 `channels.failover_enabled`
- 故障切换：候选渠道按 priority 依次尝试，`upstream.ShouldFailoverStatus()` 识别可切换的错误状态码（4xx 认证/限流、5xx 等）

---

## 数据库 Schema

SQLite 通过 `CREATE TABLE IF NOT EXISTS` 建表 + `ALTER TABLE ADD COLUMN` 增量迁移（见 `internal/store/db.go`）。完整 schema（含迁移后的列）：

```sql
-- 渠道商
CREATE TABLE channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'openai',  -- openai/anthropic/gemini/responses（openrouter 已并入 openai）
    base_url TEXT NOT NULL,
    api_key TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    priority INTEGER DEFAULT 99,          -- 0=最高优先级，越大优先级越低
    use_proxy INTEGER DEFAULT 0,          -- 是否通过出站代理转发
    failover_enabled INTEGER DEFAULT 1,   -- 渠道熔断开关
    test_model TEXT DEFAULT '',           -- 熔断探测用的测试模型
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
    pricing_cache_read REAL DEFAULT 0,   -- 缓存读取 $/1M tokens
    pricing_cache_write REAL DEFAULT 0,  -- 缓存写入 $/1M tokens
    pricing_rules TEXT DEFAULT '[]',     -- 高级定价规则 JSON（time_range / token_tier）
    protocols TEXT DEFAULT '[]',         -- 支持的协议列表 JSON（空 = 继承渠道 type）
    protocol_urls TEXT DEFAULT '{}',     -- 各协议独立的上游 URL JSON（如 {"anthropic":"https://.../v1/messages"}）
    user_modified INTEGER DEFAULT 0,     -- 用户手动编辑标记，同步不覆盖
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
    total_duration_ms INTEGER DEFAULT 0,  -- 含排队的总耗时
    cost REAL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE SET NULL,
    FOREIGN KEY (model_id) REFERENCES models(id) ON DELETE SET NULL
);

-- 用量日聚合（预聚合表，按 date+api_key_id+request_model 主键）
CREATE TABLE usage_daily (
    date TEXT NOT NULL,
    api_key_id INTEGER,
    request_model TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cache_hit_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    requests INTEGER DEFAULT 0,
    cost REAL DEFAULT 0,
    PRIMARY KEY (date, api_key_id, request_model)
);

-- 代理配置
CREATE TABLE proxy_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    intercept_domains TEXT DEFAULT '[]',       -- 直接拦截（MITM 解密）域名
    smart_intercept_domains TEXT DEFAULT '[]', -- 智能拦截域名
    default_channel_id INTEGER,
    model_mappings TEXT DEFAULT '{}',          -- 模型映射 JSON（代理模块专用）
    mitm_all INTEGER DEFAULT 0,                -- 全量 MITM 开关
    proxy_username TEXT DEFAULT '',            -- 代理认证
    proxy_password TEXT DEFAULT '',
    forward_proxy_url TEXT DEFAULT '',         -- 全局出站代理地址
    forward_proxy_user TEXT DEFAULT '',
    forward_proxy_pass TEXT DEFAULT '',
    probe_api_key TEXT DEFAULT '',             -- 熔断探测用 API Key
    request_timeout_seconds INTEGER DEFAULT 60,-- 上游请求超时
    failover_enabled INTEGER DEFAULT 1,        -- 全局熔断开关
    github_token TEXT DEFAULT '',              -- MCP GitHub 导入认证
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

-- 技能管理（MCP）
CREATE TABLE skills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    type TEXT NOT NULL DEFAULT 'manual',  -- manual / github / repo
    source_url TEXT DEFAULT '',
    base_path TEXT DEFAULT '',
    enabled INTEGER DEFAULT 1,
    commit_sha TEXT DEFAULT '',           -- GitHub 更新追踪
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE skill_tags (skill_id, tag, ...);          -- 技能标签
CREATE TABLE skill_files (skill_id, file_path, file_size, ...);  -- 技能文件索引
CREATE TABLE skill_combinations (name, description, ...);        -- 技能组合
CREATE TABLE skill_combination_items (combination_id, skill_id, sort_order, ...);
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
| GET | `/api/channels/:id` | 渠道详情 |
| POST | `/api/channels` | 创建渠道 |
| PUT | `/api/channels/:id` | 更新渠道 |
| DELETE | `/api/channels/:id` | 删除渠道 |
| POST | `/api/channels/:id/test` | 测试渠道连通性 |
| POST | `/api/channels/:id/sync` | 从上游同步模型列表 |
| POST | `/api/channels/:id/toggle` | 启用/禁用渠道 |

### 模型管理
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/models` | 模型列表（?channel_id= 筛选） |
| GET | `/api/models/:id` | 模型详情 |
| PUT | `/api/models/:id` | 更新模型（定价/规格/规则） |
| DELETE | `/api/models/:id` | 删除模型 |
| POST | `/api/models/:id/toggle` | 启用/禁用 |
| POST | `/api/models/batch` | 批量操作（enable/disable/delete） |
| GET | `/api/models/export` | 导出模型配置 |
| POST | `/api/models/import` | 导入模型配置 |
| POST | `/api/models/reload-presets` | 热重载 model-presets.json |

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
| GET | `/api/stats/by-model` | 按模型聚合统计 |
| GET | `/api/stats/year-heatmap` | 年度热力图数据 |
| GET | `/api/usage/records` | 最近记录（?api_key_id= 筛选） |

### 代理配置
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/proxy/config` | 代理配置 |
| PUT | `/api/proxy/config` | 更新代理配置（更新后通知 ProxyHandler 刷新缓存） |
| GET | `/api/proxy/cert/download` | 下载根 CA 证书（.pem / .crt） |

### MCP 配置
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/mcp/status` | MCP 服务状态 |
| PUT | `/api/mcp/github-token` | 更新 GitHub Token |

### 技能管理
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/skills` | 技能列表（?q=&tag=） |
| GET | `/api/skills/tags` | 技能标签列表 |
| GET | `/api/skills/:id` | 技能详情 |
| GET | `/api/skills/:id/files/*filePath` | 读取技能文件 |
| POST | `/api/skills` | 创建技能 |
| PUT | `/api/skills/:id` | 更新技能 |
| DELETE | `/api/skills/:id` | 删除技能 |
| POST | `/api/skills/import-github` | 从 GitHub 导入技能 |
| POST | `/api/skills/upload` / `upload-folder` | zip / 文件夹上传 |
| POST | `/api/skills/import-repo` / `sync-repo` | 仓库导入 / 同步 |
| GET | `/api/skills/check-updates` | 检查 GitHub 更新 |

### 技能组合
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/skill-combinations` | 组合列表 |
| POST | `/api/skill-combinations` | 创建组合 |
| PUT | `/api/skill-combinations/:id` | 更新组合 |
| DELETE | `/api/skill-combinations/:id` | 删除组合 |
| POST | `/api/skill-combinations/:id/skills` | 添加技能到组合 |
| DELETE | `/api/skill-combinations/:id/skills/:skillId` | 从组合移除技能 |

### 数据库管理
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/database/backup` | 数据库备份下载 |
| POST | `/api/database/restore` | 数据库恢复 |

### 推理接口（/v1，API Key 认证）
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/v1/models` | 模型列表（OpenRouter 格式，无需认证，带缓存） |
| POST | `/v1/chat/completions` | 聊天补全（下游 OpenAI 协议） |
| POST | `/v1/completions` | 文本补全（同 ChatCompletion） |
| POST | `/v1/messages` | Anthropic Messages 协议入口（下游） |
| POST | `/v1/responses` | OpenAI Responses API 协议入口（下游） |
| POST | `/v1/embeddings` | 功能类透传 |
| POST | `/v1/images/generations`、`/v1/images/edits` | 功能类透传 |
| POST | `/v1/audio/speech`、`/v1/audio/transcriptions`、`/v1/audio/translations` | 功能类透传 |
| POST | `/v1/moderations`、`/v1/batches` | 功能类透传 |

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

### 优先级 1：内置数据库 + 模型预设文件

**内置 modelDB**（`internal/adapter/openai.go`）：项目内置 40+ 流行模型的基础元信息。

**模型预设文件**（`data/model-presets.json`，由 `config.NewModelPresets` 管理）：取代 config.yaml 的 `model_defaults`。首次启动时若文件不存在，会从 config.yaml 的 `model_defaults` 迁移生成；之后以 JSON 文件为准，`/api/models/reload-presets` 可热重载。支持 flat 定价（pricing_input/output/cache_read/cache_write）和 `pricing_rules` 高级规则。

### 优先级 2：上游 API

同步模型列表时，如果上游 API 返回元信息（如 OpenRouter 返回 `context_length` 等字段），会覆盖 modelDB 和配置文件默认值。

### 优先级 3：用户手动编辑

用户在管理面板中编辑模型（修改定价、特性等），会标记 `user_modified=1`。之后任何同步操作**不会覆盖**用户已编辑过的模型字段。

### 合并逻辑

合并发生在 `internal/upstream/syncer.go` 的 `mergeModelInfo()` 中，按字段级别合并：

1. 从上游 API 获取模型列表
2. 对于每个模型，优先使用上游返回的值，空白字段从 modelDB 补全
3. 再以配置文件默认值覆盖
4. `protocols` 合并：上游声明 > modelDB > 预设 > 空（空 = 继承渠道 type）
5. `Upsert` 到 DB 时，如果该模型已被用户编辑（`user_modified=1`），保留用户设置，不覆盖
6. `UPDATE` 操作会自动设置 `user_modified=1`

### 恢复上游数据

如需让模型重新接受同步更新，可通过 `DELETE` 删除后重新同步，或通过 API 清除 `user_modified` 标记。

---

## 定价规则引擎（internal/pricing/）

- 支持 `time_range`（时间段定价，如 00:00-08:00 半价）和 `token_tier`（Token 阶梯定价）
- 保留 4 个 flat pricing float64 字段（`pricing_input/output/cache_read/cache_write`），无 `pricing_rules` 时走旧逻辑，**回溯兼容**
- 匹配逻辑：first-match-wins 有序规则列表，支持跨天时间区间、星期过滤、prompt/context 双阈值
- 计费入口：`handler/proxy.go` 和 `proxy/adapter.go` 的 `recordUsage()` 中调用 `ResolvePricing`
- 规则存储：`models.pricing_rules` JSON 列，前端 Models.vue 有可视化编辑器
- 测试：26 个单元测试（`internal/pricing/engine_test.go`），改动必须运行 `go test ./internal/pricing/`

---

## MITM 代理要点（internal/proxy/）

### 架构
- `cert.go`：自动生成根 CA（`.pem` / `.crt` 双格式下载），按域名动态签发证书
- `server.go`：处理 HTTPS CONNECT 隧道，TLS 拦截后解析 HTTP 请求，LLM 请求走转发路径，其余走透传（`forwardDirect` / `forwardHTTP`）
- `router.go`：`ShouldIntercept(hostname)` 域名匹配 + `IsLLMRequest(method, path, headers, body)` 识别请求体 model 字段
- `adapter.go`：`ProxyAdapter` 转发 + 模型映射 + 计费

### 关键设计决策
- **模型映射仅实现于代理模块**（proxy_config 表 `model_mappings` 列）：source_model → target_model + 参数注入（thinking/reasoning_effort）+ 响应 model 字段重写（含 SSE 流式）
- **中转站（/v1/*）不实现模型映射**，client 直接发送实际模型名
- 代理请求同样经过协议适配并记录用量（`recordUsage` 含定价计费）
- **透传路径**：`tryForwardModel` 从客户端请求路径推导下游协议（`downstreamProtocolFromPath`），模型协议命中时透传（仅模型映射/参数注入，不过协议转换，URL 用 `ProtocolURL`）；gemini 渠道始终转换
- `GET /v1/models` 在代理中会被 `HandleModelsRequest` 处理，返回**精确匹配 ModelProxy 的 OpenRouter 格式**模型列表（pricing/supported_parameters/architecture 等字段，Android Studio Copilot 等客户端依赖此格式）
- 透传判断：仅 `IsLLMRequest()` 决定是否拦截（历史上曾因 intercept_domains 内非 LLM 请求被误拦导致 502，见下方陷阱）
- 代理认证：proxy_username/password 用于客户端连接认证，与 API 登录凭据相同

### 与 ModelProxy 兼容性的历史修复
1. **TLS close_notify**：响应写入后调用 `tlsConn.Close()`，避免 OkHttp 视为传输错误
2. **ALPN 协商**：`tls.Config` 设置 `NextProtos: []string{"http/1.1", "h2"}`
3. **GET /v1/models 处理**：handleConnect 中先检测该请求，返回本地模型列表

---

## MCP 技能管理

- `internal/mcp/server.go` 基于 `mark3labs/mcp-go`，Streamable HTTP，挂载 `/mcp`
- 认证：`mcp.token` 为空则无需认证；设置后所有 MCP 客户端必须带 `Authorization: Bearer <token>`
- 技能目录：`data/skills/`，每个技能是含 `SKILL.md`（YAML frontmatter）的文件夹
- 注册的工具：
  - `list_skill_combinations` / `get_skill_combination` — 技能组合查询
  - `list_skills` / `search_skills` — 技能搜索（关键词/标签）
  - `get_skill` / `get_skill_file` — 技能元数据与文件内容读取
  - `install_combination` / `use_skill` — 一键加载组合/技能全部文件到上下文
- Agent 连接配置方式详见 [MCP.md](./MCP.md)

---

## 已知问题 & 待改进

### 已修复的历史 Bug
1. **SQL NULL Scan 错误**：`models LEFT JOIN channels` 时 `c.name` 为 NULL → 使用 `COALESCE(c.name, '')`
2. **前端 SPA 路由 401**：auth 中间件拦截了非 API 路径 → 改为仅拦截 `/api/` 前缀
3. **TLS 握手超时**：Go 默认 Transport 对 Cloudflare 托管站点握手失败 → 使用 `&tls.Config{}` 显式配置
4. **模型同步 TLS 超时**：`http.Client` 默认无 Transport TLS 配置 → 使用 `upstream.NewHTTPClient()`
5. **侧边栏 emoji 乱码**：文件编码问题 → 改用 `String.fromCodePoint()` 渲染
6. **非 LLM 请求误拦截**：intercept_domains 内域名（如 openrouter.ai）的所有请求都被送入 LLM 处理路径，健康检查因缺 model 字段返回 502 → 透传判断仅用 `!IsLLMRequest()`
7. **隧道稳定性**：认证失败日志区分"未携带"/"凭据错误"；`net.DialTimeout` 增至 20s；双向 io.Copy 增加 120s 超时防 goroutine 泄漏
8. **usage_daily 零值日期**：CreatedAt 为零值导致 `0001-01-01` 脏数据 → 迁移时删除 + 幂等回填

### 待实现功能
- **渠道 API Key 测试**：`/api/channels/:id/test` 返回固定成功
- 模型按 ID 查询缓存的 COALESCE 问题未完全验证

### 架构注意事项
- **嵌入前端**：`//go:embed web/dist/index.html web/dist/assets/*` 要求文件必须存在于编译时
- **数据库迁移**：使用 `ALTER TABLE ADD COLUMN` 增量迁移，会静默忽略已存在的列
- **认证白名单**：中间件仅拦截 `/api/` 前缀，`/api/auth/login`、`/assets/*`、`/`、`/v1/*`、`/mcp` 不拦截
- **Go embed 路径**：必须相对于 `cmd/server/` 目录
- **配置热更新**：代理配置更新通过 `proxyConfigH.SetOnUpdate(proxyH.InvalidateProxyConfig)` 通知刷新缓存

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
# Proxy: localhost:8800
# 默认登录: admin / admin123
```

### 测试
```bash
go test ./...   # 重点：internal/adapter、internal/pricing 有完整测试套件
```

### Docker
```bash
docker compose up -d
```

### 新增上游协议渠道类型
1. 在 `internal/adapter/` 创建适配器（实现 `Adapter` 接口 + 上游流转换器）
2. 在 `adapter.go` 的 `NewAdapter()` 注册
3. 在 `internal/upstream/syncer.go` 确认模型列表解析逻辑
4. 前端 `Channels.vue` 的类型下拉框加入新类型

### 新增下游协议入口
1. 创建 `DownstreamAdapter` 实现（`RequestToCanonical` / `ResponseToDownstream` / `NewStreamConverter`）
2. 在 `NewDownstreamAdapter()` 注册
3. 在 `handler/proxy.go` 添加入口 handler（参考 `MessagesCompletion`）
4. 在 `cmd/server/main.go` 的 `/v1` 组注册路由

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
