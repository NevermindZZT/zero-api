# CPA / Codex 中转可靠性修复与异常日志

本次改动针对 DSH → zero-api HTTP `/v1/chat/completions`、`/v1/responses`、`/v1/messages` → CPA / 模型上游链路。修复前证据见 [原始审计](audits/codex-cpa-relay-audit-2026-10-28.md)，该审计的行号是修复前快照。

## 本次已实施

- **Responses 原生保真**：不缓存/拼接历史 reasoning、function_call；不改变 input、previous_response_id；补齐 `session_id` 等会话关联头。兼容 no-op 函数保留，但不再维护 replay 状态。
- **工具转换**：正确关联 call_id / item_id / 并行 index，保留工具名称、参数、完整完成事件和最终 output；支持对象形式 tool_choice 与 reasoning。
- **终止语义**：缺少终止事件、显式失败或转换失败不伪造成功；`incomplete`/长度限制保留语义，不当作传输故障熔断。
- **HTTP 流错误分类**：成功、客户端取消、上游 HTTP 错误、超时、流截断、下游写入失败分开处理；已输出后绝不换渠道重放，但也不记成功。非 2xx 的 JSON 错误不会强制标记 SSE。非流式 HTTP 200 中的 `failed` / `incomplete` / 顶层 error 也会记录语义异常，原生响应保持原样，转换失败不伪造成功。
- **取消/资源**：客户端取消传播到上游；流式空闲与下游写入有截止时间；SSE 以 frame 聚合，原生字节不改，多 data 行/CRLF/无空格 `data:` 能正确转换与提取 usage。
- **熔断**：冷却不再被普通请求当作成功清除；到期只有一个探测请求；探测使用实际协议、模型 URL 和正确渠道凭据，并校验有效推理响应，401/429 不再被当健康。
- **底层**：新库先建表再执行有错误检查的增量迁移；非流式响应头超时与配置一致；复用有界连接池、代理密码变更生效；CPA 重启追加日志而非清空旧日志。

## 异常日志设计

### 默认配置

无需给已有配置补字段，默认即启用；配置示例见 [默认配置](../configs/config.yaml)。

```yaml
request_log:
  enabled: true
  path: ""             # 空 = 数据库同目录/logs/request-errors.jsonl
  max_size_mb: 10
  max_backups: 5
  max_age_days: 14
```

数据库默认 `data/zero-api.db` 时，日志默认写入 `data/logs/request-errors.jsonl`。Docker 使用既有 `/app/data` 持久卷即可保存，不另开端口。

- 一个活动文件与最多 5 份编号备份（`.1` 最新），默认总量不超过约 60 MiB。
- 大小/天数先到先清理；过期清理在启动、异常写入和每小时维护时执行，不是精确到秒的 TTL。
- 日志重启追加，运行中轮转，单条最多 2 KiB。
- Linux/macOS 目录 0700、文件 0600，拒绝符号链接。Windows 仍应由部署者设置账户/目录 ACL。
- 自定义目录必须为专用私有目录，不会擅自收紧含其他文件的共享目录。
- 初始化/写盘/轮转失败只打印受控 stderr 告警，不阻断推理；因此日志不是保证不丢的审计账本。请监控 `[request-log] WARNING`。
- 一个日志路径只允许一个进程持有；多实例应使用独立日志路径或后续接入集中日志系统。

### 保存哪些信息

只记录异常/取消，不给正常成功请求写完整访问日志。两种 stage：

1. `attempt`：转发尝试，记录模型、渠道 ID/类型、协议、尝试序号、耗时、上下游状态、是否已开始输出、已输出字节及能否安全重试。
2. `request`：HTTP 请求结束时的 4xx/5xx、取消或 panic 元数据，不读取请求体，所以此层的 model/stream 可能为空/false；同一次失败可有两种 stage，不代表重复发送上游请求。

每次 `/v1` 请求生成新的 `X-Request-Id`，返回客户端并发送上游；不信任客户端传入的 ID。上游返回的另一个 ID 单独记入 `upstream_request_id`，不会覆盖本地 ID。若 CPA 自己也记录或透传该头，可跨层关联；不保证任意 CPA 版本默认日志都含该字段。

**HTTP 200 不代表流成功。** 已开始 SSE 后无法更改 HTTP 状态；此时应看 `category`，不能只筛选 status >= 400。

示例（演示值，不来自真实用户请求）：

```json
{"timestamp":"2026-10-28T08:00:00Z","request_id":"req_example","method":"POST","path":"/v1/responses","protocol":"responses","model":"example-model","channel_id":7,"channel_type":"responses","attempt":1,"stream":true,"stage":"attempt","category":"stream_truncated","status":200,"upstream_status":200,"duration_ms":1200,"response_started":true,"bytes_written":512,"retryable":false}
```

常用分类：

| category | 含义 |
|---|---|
| `upstream_http_error` | 上游非 2xx，结合 upstream_status 和 upstream_error_code |
| `upstream_timeout` | 连接/响应头/非流式总超时 |
| `stream_idle_timeout` | 流中途超过空闲超时 |
| `stream_truncated` | 上游异常 EOF、缺少终止事件或帧超限 |
| `stream_failed` | 上游显式失败事件 |
| `stream_incomplete` | 协议明确报告未完整生成，不自动当渠道故障 |
| `conversion_error` | 本地协议请求/响应转换未完成 |
| `client_cancelled` | DSH/客户端取消，不惩罚渠道健康 |
| `downstream_write` | 向客户端写入/刷新失败 |
| `circuit_open` / `probe_failed` | 渠道冷却 / 健康探测失败 |

`upstream_error_code` 仅保留已知固定类别，例如上下文过长、额度不足、限流、认证失败、工具调用不匹配、previous response 不存在。上游原始 message 只可在内存中匹配已知特征，不落盘。

### 不保存的信息与边界

不保存 Authorization、API Key、OAuth Token、Cookie、请求/响应 body、工具参数、提示词、session ID、previous_response_id、完整 URL/query 或任意 err.Error / panic 内容。模型和请求 ID 有格式/长度限制，动态路径会归为固定 unknown 路由。

因此这份日志适合确认**哪层/哪渠道/哪种失败、是否已输出、是否重试**，不用于恢复整段对话，也不能完整展示未知上游校验错误。未知分类仍需结合时间、CPA 日志及经授权的最小复现；不要为排障长期打开原始 body 日志。

### 异常发生后如何分析

1. 记录 DSH 的报错时间（带时区），如能获得响应头则记录 `X-Request-Id`。
2. 按 request_id 查活动文件和编号备份：

```bash
jq -c 'select(.request_id == "req_example")' data/logs/request-errors.jsonl*
```

3. 查看 attempt：
   - `response_started=false` 且 `retryable=true`：允许尝试其他候选渠道；相同 request_id 下 attempt 增加。
   - `response_started=true`：禁止跨渠道重放；即使 status=200，也可能是 `stream_truncated`。
   - `upstream_status=400`、`upstream_error_code=tool_call_mismatch`：优先排查协议/工具状态链，而非网络超时。
   - `client_cancelled`：先确认 DSH 是否主动停止/重试。
   - `circuit_open`：不是本次已访问 CPA；结合之前的失败和 Retry-After 查看。
4. 如需分享，优先只分享对应 request_id 的 JSONL 行；仍应人工复核模型名、ID 和部署信息后再发出。

## 验收与部署

测试使用本地 httptest / 临时数据库，不调用真实模型上游。新增回归覆盖原生保真、并行工具、failed/incomplete/截断、取消传播、错误格式、熔断、全新数据库、日志脱敏/权限/追加/轮转/保留/并发、活动文件被移走后安全重建，以及 HTTP 200 中断/语义失败的真实日志集成。

已通过 `go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...`、前端 `npm run build`、本机 `go build ./...` 和 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...`、`CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`。前端依赖安装有既有 inflight/glob 弃用提示，构建有大 chunk 提示；它们不影响本轮构建通过，也不在本次后端修复范围。

SSE 单帧最大 4 MiB，用量提取采用有界的头尾窗口（约 8 MiB）；超限会分类为异常而不是无限占用内存。这不是对整个生成总时长或总输出长度的限制；如果未来要经 Responses 传输超大单帧图片/音频，需单独评估并调整该上限。

部署前备份数据库，正常构建新二进制/镜像并通过既有发布流程重启。已有线上进程不会因为修改源码而自动更新。本次未修改真实运行配置、没有重启任何服务。

建议上线后用无副作用的最小工具调用，比较直连 CPA 与经过 zero-api 的多轮结果；不要自动重放真实有副作用的工具。

## 明确未包含

- WebSocket 的出站代理、回落与多轮权限/计费/日志，需要按其连接级语义单独实施、握手和多轮测试；本次不把 HTTP 修复描述为 WS 已修复。
- MITM 跨协议流式响应转换仍需独立整改；原生 Responses 的 no-op normalize / 会话头改进会共享受益，但不能覆盖 MITM 所有问题。
- 未新增日志 Web UI / 下载 API，避免在没有权限/脱敏设计时把日志暴露给浏览器；目前通过服务端文件或容器持久卷分析。
- CPA 原始 stdout 日志只修复重启截断和权限；新 JSONL 异常日志的运行时大小/保留策略不等于 CPA 自身原始日志已有同等策略。
