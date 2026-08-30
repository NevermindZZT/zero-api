# AGENTS.md — zero-api 开发指南

> 本文件供 AI 编码代理（GitHub Copilot / Claude Code 等）使用，提供项目架构概览、代码约定与常见任务的操作指南。
> 详细架构文档见 [ARCHITECTURE.md](./ARCHITECTURE.md)，MCP 使用说明见 [MCP.md](./MCP.md)。

---

## 项目概述

zero-api 是一个基于 **Go + Gin + SQLite（纯 Go，零 CGO）+ Vue 3 + Naive UI** 的个人大模型 API 中转站，集三大功能于一体：

1. **API 中转**（端口 8080）：兼容 OpenAI `/v1/chat/completions`、Anthropic `/v1/messages`、OpenAI Responses `/v1/responses` 三种下游协议，自动路由到四种上游协议渠道
2. **MITM 代理**（端口 8520，配置项 `proxy.port`）：HTTPS 流量劫持，智能识别并拦截 LLM 推理请求，非 LLM 流量直接透传
3. **MCP 技能管理**（挂载于主 API 端口 `/mcp`）：提供 AI Agent Skill 的发现、安装、组合能力

## 常用命令

```bash
# 前端构建（产物复制到 embed 目录）
cd web && npm install && npm run build
Remove-Item -Recurse -Force cmd/server/web/dist/
Copy-Item -Recurse web/dist cmd/server/web/

# 后端编译
go build -o zero-api.exe ./cmd/server/

# 运行
./zero-api.exe          # API: :8080  Proxy: :8520  默认登录 admin/admin123

# 指定配置文件
$env:ZERO_API_CONFIG="configs/config.yaml"; ./zero-api.exe

# 测试
go test ./...            # 重点：internal/adapter、internal/pricing 有完整测试套件

# Docker
docker compose up -d
```

## 目录结构（核心）

```
cmd/server/main.go          # 入口：双服务启动 + 全部路由注册
internal/
├── adapter/                # ⭐ 协议适配层（最核心）
│   ├── adapter.go          #   Adapter 上游接口 + modelDB 内置模型库 + NewAdapter()
│   ├── openai.go           #   OpenAI 兼容上游（规范格式，基准实现）
│   ├── anthropic.go        #   Anthropic Messages 上游
│   ├── gemini.go           #   Google Gemini 上游
│   ├── responses.go        #   OpenAI Responses API 上游（/v1/responses）
│   ├── downstream.go       #   DownstreamAdapter 下游接口 + StreamConverter + 透传适配器
│   ├── downstream_anthropic.go  # 下游 Anthropic ↔ 规范格式
│   ├── downstream_responses.go  # 下游 Responses ↔ 规范格式
│   └── upstream_stream.go  #   上游 SSE → OpenAI 规范格式 SSE（含 Gemini 流）
├── config/config.go        # 配置加载 + ModelPresets 预设
├── handler/
│   ├── proxy.go            # ⭐ 核心转发：handleCompletion / streamResponse / PassthroughEndpoint
│   ├── circuit_breaker.go  # 渠道熔断器（冷却+探测恢复）
│   ├── channel.go model.go sync.go usage.go api_key.go auth.go
│   ├── proxy_config.go mcp_config.go
│   ├── skill.go skill_combination.go database.go
├── mcp/server.go           # MCP Streamable HTTP 服务器 + 工具注册
├── middleware/             # AuthMiddleware（仅拦截 /api/）+ CORS
├── pricing/                # 定价规则引擎（time_range / token_tier 阶梯定价）
├── proxy/                  # MITM：cert.go / server.go / router.go / adapter.go
├── store/                  # SQLite 数据层：db.go service.go + 各 Repository
│   ├── skill.go skill_fs.go skill_combination.go   # MCP 技能存储
│   └── usage.go            # 用量批量写入缓冲 + 日聚合
└── upstream/               # client.go（TLS 兼容）+ syncer.go（模型同步+三优先级合并）
web/src/views/              # Vue 页面：Dashboard/Channels/Models/APIKeys/Usage/ProxySettings/
                            # Settings/Database/MCPSettings/Skills/SkillCombinations/ChatTest/ForwardProxy
configs/config.yaml         # 默认配置
data/model-presets.json     # 模型预设文件（可热重载）
```

## ⭐ 核心架构：协议适配体系

### 规范格式（Canonical Format）

zero-api 内部统一使用 **OpenAI Chat Completions 格式**作为规范格式。所有协议转换围绕它进行：

```
客户端协议 (下游)  ──RequestToCanonical──▶  规范格式  ──ConvertRequest──▶  上游协议 (渠道)
客户端协议 (下游)  ◀──ResponseToDownstream──  规范格式  ◀──ConvertResponse──  上游协议 (渠道)
```

### 上游适配器 `Adapter`（internal/adapter/adapter.go）

每个渠道类型（`openai` / `anthropic` / `gemini` / `responses`）实现一个上游适配器，通过 `NewAdapter(channelType)` 工厂创建。注意 `openrouter` 是历史遗留值，已并入 openai。接口方法：

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

## 请求处理主链路

```
/v1/chat/completions ─┐
/v1/messages ─────────┤  handler/proxy.go  handleCompletion(c, rawBody, downstream)
/v1/responses ────────┘
        │
        ▼
1. 读取 body → resolveAndValidateAPIKey → 解析 model
2. 按 model_id 查找启用模型 → 候选渠道列表（同模型多渠道按 priority 排序）
3. 对每个候选渠道（熔断器检查 + 故障切换）：
   a. 透传判断：matchedModel.SupportsProtocol(downstreamProtocol, ch.Type) → passthrough 或常规适配器
   b. downstream.RequestToCanonical → adapt.ConvertRequest → 构造上游请求（透传时 URL 用 ProtocolURL）
   c. 转发（支持流式 streamResponse / 非流式）
   d. 响应：adapt.ConvertResponse → downstream.ResponseToDownstream
   e. recordUsage（异步）：提取 usage → pricing 引擎计费 → 写库
4. 全部失败 → 502；成功 → breaker.RecordSuccess(ch.ID)
```

**功能类接口透传**（`/v1/embeddings`、`/v1/images/*`、`/v1/audio/*`、`/v1/moderations`、`/v1/batches`）：走 `PassthroughEndpoint`，候选条件为 `m.SupportsProtocol("openai", ch.Type)`（模型支持 openai 协议即可，不再限定 openai 渠道），请求体原样转发、响应原样返回，客户端请求路径即上游路径（注意 base_url 以 `/v1` 结尾时要去掉再拼接路径）。

## 熔断与故障切换（internal/handler/circuit_breaker.go）

- 请求失败 → 冷却 5 分钟（cooldown）
- 冷却到期 → probing 状态，命中时先发轻量探测请求验证健康
- 探测通过 → 恢复 normal 继续当前请求；失败 → 重新冷却
- 连续失败递增冷却：5min → 10min → 20min → 40min（上限）
- 成功请求调用 `RecordSuccess` 重置

## 模型数据管理（三优先级合并）

模型元数据（context_window / max_output_tokens / 特性 / protocols / 定价）来源按优先级：

1. **内置 modelDB**（`internal/adapter/openai.go`）+ **model-presets.json 预设**（`data/model-presets.json`，由 config.yaml 的 `model_defaults` 持久化生成，`/api/models/reload-presets` 可热重载）
2. **上游 API**（同步时覆盖；`protocols` 字段可从上游 `/v1/models` 响应解析）
3. **用户手动编辑**（`user_modified=1`，之后任何同步都不覆盖）

合并逻辑在 `internal/upstream/syncer.go` 的 `mergeModelInfo()`，按字段级合并。**注意**：模型列表同步时会自动设置 `user_modified=1`，如需恢复上游数据需 DELETE 后重新同步。

## 定价规则引擎（internal/pricing/）

- 支持 `time_range`（时间段定价，如 00:00-08:00 半价）和 `token_tier`（Token 阶梯定价）
- 保留 4 个 flat pricing float64 字段（`pricing_input/output` 等），无 `pricing_rules` 时走旧逻辑，**回溯兼容**
- 匹配逻辑：first-match-wins 有序规则列表，支持跨天时间区间、星期过滤、prompt/context 双阈值
- 计费入口：`handler/proxy.go` 和 `proxy/adapter.go` 的 `recordUsage()` 中调用 `ResolvePricing`
- 修改定价逻辑后必须运行 `go test ./internal/pricing/`（26+ 用例）

## MITM 代理要点（internal/proxy/）

- **模型映射仅实现于代理模块**（proxy_config 表 `model_mappings` 列）：source_model → target_model + 参数注入（thinking/reasoning_effort）+ 响应 model 字段重写（含 SSE）
- **中转站（/v1/*）不实现模型映射**，client 直接发送实际模型名
- 代理请求同样经过协议适配（`proxy/adapter.go`）并记录用量
- **透传路径**：`tryForwardModel` 从客户端请求路径推导下游协议（`downstreamProtocolFromPath`），模型协议命中时透传（仅模型映射/参数注入，不过协议转换，URL 用 `ProtocolURL`）；gemini 渠道始终转换
- `GET /v1/models` 在代理中会被 `HandleModelsRequest` 处理，返回本地模型列表（Android Studio Copilot 等客户端依赖此行为）
- 透传判断：仅 `IsLLMRequest()` 决定是否拦截（历史上曾因 `intercept_domains` 内非 LLM 请求被误拦导致 502，见下方陷阱）

## MCP 技能管理

- `internal/mcp/server.go` 基于 `mark3labs/mcp-go`，Streamable HTTP，挂载 `/mcp`
- 能力：技能列表/详情/文件读取、创建/更新/删除、GitHub 仓库导入（`/skills/import-github`、`/skills/import-repo`）、zip 上传、技能组合（`skill-combinations`）
- 认证：`mcp.token` 为空则无需认证；设置后所有 MCP 客户端必须带 `Authorization: Bearer <token>`
- 技能目录：`data/skills/`，每个技能是含 `SKILL.md`（YAML frontmatter）的文件夹

## CLIProxyAPI Sidecar

- `internal/cpa/` 管理 CLIProxyAPI 二进制下载、配置生成、OAuth 登录和进程生命周期
- 数据目录：`data/cliproxyapi/`；Windows 二进制名为 `CLIProxyAPI.exe`，Linux/macOS 为 `CLIProxyAPI`
- Windows release 使用 ZIP，官方 ZIP 内 `.exe` 条目可能没有 Unix 执行权限位，不能用 `mode&0111` 判断可执行文件
- 下载使用 `.part` 临时文件 + HTTP Range 断点续传，完成后原子改名；下载失败必须清理残留，避免把半截归档当作已下载
- 旧版本 Windows 临时文件 `CLIProxyAPI.tmp` 会迁移为当前临时下载路径继续下载
- 损坏 ZIP/GZip 不能回退为裸二进制复制，否则会安装不可执行的压缩包
- CLIProxyAPI 安装接口是长耗时操作，`handler.CPAHandler.InstallBinary` 必须清除主 API `WriteTimeout`，否则下载成功后客户端可能收到 `Response ended prematurely`
- **Codex 额度**：zero-api 自动生成独立 Management Key，写入 sidecar `remote-management.secret-key`；通过 `/v0/management/auth-files` + `/v0/management/api-call` 查询 Codex `wham/usage`，只对已登录 Codex OAuth 账号执行
- **Codex 窗口归类**：必须优先按响应窗口的 `limit_window_seconds` 判断 5h/7d（18000=5h，604800=7d），不能只按 `primary_window`/`secondary_window` 字段名；前端只渲染实际返回的窗口
- **额度扩展**：`internal/cpa.QuotaProvider` 是 provider 抽象，首期实现 `CodexQuotaProvider`，后续 Claude/Grok/Kimi provider 不应直接耦合 handler
- 改动下载/解压逻辑后运行 `go test ./internal/cpa/`，额度/配置改动需验证 `go test ./...`、`go build ./...` 与 Linux 交叉构建

## 常见任务指南

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

编辑 `internal/adapter/openai.go` 的 `modelDB` map：

```go
"model-id": {ID: "model-id", ContextWindow: 128000, MaxOutputTokens: 16384, SupportsVision: true, SupportsThinking: true, SupportsTools: true},
```

### 添加新 API 端点

1. `internal/handler/` 创建/扩展处理器
2. `cmd/server/main.go` 注册路由
3. `web/src/api/index.ts` 添加 API 调用
4. `web/src/views/` 创建页面 + `web/src/router/index.ts` 添加路由

### 修改前端后重新构建

前端产物必须复制到 `cmd/server/web/dist/` 才会被 `//go:embed` 打包（**编译时文件必须存在**，缺失会导致构建失败）。

## ⚠️ 关键陷阱与约定

1. **SQL NULL Scan**：`LEFT JOIN` 联表查询必须用 `COALESCE` 兜底 NULL 列（如 `COALESCE(c.name, '')`）
2. **认证中间件只拦截 `/api/` 前缀**：前端 SPA 路由、`/assets/*`、`/v1/*`、`/mcp` 不拦截；不要把非 API 路径误加进认证白名单逻辑
3. **上游 TLS 兼容**：所有上游 HTTP 客户端必须用 `upstream.NewHTTPClient*()` 创建（显式 `&tls.Config{}`，解决 Cloudflare 托管站点握手超时）；不要在 handler 里裸用 `http.Client{}`
4. **模型同步 user_modified**：`UPDATE` 会自动设置 `user_modified=1`，同步逻辑会跳过该模型，注意区分
5. **代理透传判断**：拦截逻辑只依赖 `IsLLMRequest()`（识别请求体 model 字段），不要依赖"域名在 intercept_domains 中就要拦截"——否则健康检查等非 LLM 请求会 502
6. **前端编码**：emoji 等特殊字符用 `String.fromCodePoint()` 渲染，避免文件编码损坏（侧边栏历史 bug）
7. **ECharts 生命周期**：Dashboard/Usage 页的图表实例在 `onUnmounted` 时 dispose，刷新复用实例，避免内存泄漏
8. **流式响应**：改造流式逻辑时保持 `StreamConverter` 的行级协议（`Convert` 返回 nil 表示丢弃该行，`Finish` 补发收尾），不要破坏 Anthropic 上游事件到 OpenAI chunk 的状态机
9. **Go embed 路径**：必须相对 `cmd/server/` 目录；嵌入文件在编译时不存在会直接报错
10. **配置热更新**：代理配置更新通过 `proxyConfigH.SetOnUpdate(proxyH.InvalidateProxyConfig)` 通知刷新缓存，新增配置项时记得同步失效逻辑
11. **透传 URL 陷阱**：透传判断与 URL 构造必须使用**替换前**保存的 `downstreamProtocol`（透传后 `passthrough.Protocol()` 会变——当模型声明了渠道之外的协议时，若用替换后的 `Protocol()` 会得到错误 URL）。`NewPassthroughDownstreamAdapter` 的参数应传真实下游协议而非 `ch.Type`
12. **透传用量提取**：透传请求不经过协议转换，统计必须按真实下游协议选择用量适配器（如 openai 渠道透传 Responses 时使用 `ResponsesAdapter`，不能只按 `ch.Type` 使用 `OpenAIAdapter`）
13. **Responses 原始状态链不得改写**：`previous_response_id`、`input`、`reasoning`、`function_call` 和 `function_call_output` 属于 Responses 原生状态链；透传时必须完整保留，禁止在 zero-api 内部缓存、拼接或自动 replay
14. **Responses 会话头必须透传**：CLIProxyAPI/Codex 请求可能使用下划线形式的 `session_id`；Responses 透传时必须保留 `session_id`、`x-codex-session-id`、`openai-conversation-id` 等会话关联头，否则上游无法关联前一轮 tool call
15. **测试纪律**：协议转换、定价引擎、熔断器改动必须补充/运行单元测试（`go test ./internal/adapter/ ./internal/pricing/`）
