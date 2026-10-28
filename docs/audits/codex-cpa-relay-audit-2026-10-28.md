# DSH → zero-api → CPA → Codex 中转链路审计

> 本文保留修复前的审计证据与当时行号，不代表修复后的当前状态。已实施范围、异常日志和仍待处理事项见 [可靠性修复与异常日志说明](../relay-reliability-and-request-logs.md)。

- 审计日期：2026-10-28
- 源码基线：`8bcb531`（当前工作树；版本提交 v1.5.9）
- 范围：本地源码、已有测试、隔离 mock / 协议实验；未修改业务源码或运行配置、未调用真实模型上游。
- 边界：当前工作区没有运行数据目录或生产日志，本机未发现 zero-api / CPA 进程或默认端口监听。以下区分“代码行为已证实”和“线上根因尚待日志确认”。不能据此断言全部故障都来自 zero-api，更不能排除 CPA、Codex 额度和网络因素。

## 1. 实际链路

```text
DSH（实际选用的 provider 协议）
  → zero-api /v1/responses 或 /v1/chat/completions
  → API Key / 模型权限校验
  → 虚拟模型规则（若命中）
  → 候选模型 / 渠道优先级 / 熔断
  → 同协议透传或 Chat canonical 格式转换
  → CPA sidecar（独立进程；zero-api 负责生成配置、登录和生命周期）
  → Codex OAuth 账号 / 模型服务
```

- HTTP 入口：[proxy.go:580–735](../../internal/handler/proxy.go#L580-L735)。
- 协议/URL/认证/HTTP 客户端：[proxy.go:888–1017](../../internal/handler/proxy.go#L888-L1017)。
- CPA 默认配置端口 8317、request-retry=3；这是默认值，不代表用户正在运行的配置：[config.go:38–47](../../internal/cpa/config.go#L38-L47)。
- WebSocket 是另一套链路，不能默认认为 DSH 正在使用它：[responses_websocket.go](../../internal/handler/responses_websocket.go)。

## 2. 最高优先级：原生 Responses 透传也存在的隐患

### P1-A：HTTP 丢失 `session_id` 会话关联头（离线复现）

[responses_headers.go:20–27](../../internal/adapter/responses_headers.go#L20-L27) 的白名单遗漏 `session_id` / `session-id`，HTTP 转发唯一复制入口为 [proxy.go:958–960](../../internal/handler/proxy.go#L958-L960)。WebSocket 白名单则包含这些字段。

**确定行为：** DSH 发出的该头不会到达 CPA。**条件性影响：** 若 CPA/Codex 用它关联会话，后续工具结果可能无法关联，表现为工具调用关联错误。没有线上报文，不能断言已发生。

**修复方向：** HTTP/WS 共用有针对性的非敏感会话头规则，保留 `session_id`；测试大小写、下划线、多值以及认证头不被复制。

### P1-B：“透传”会本地缓存并 replay 原生状态链（离线复现）

[proxy.go:904–907](../../internal/handler/proxy.go#L904-L907) 仍调用 NormalizeResponsesRequest；[responses_replay.go:67–85](../../internal/adapter/responses_replay.go#L67-L85) 在 input 只有工具输出、存在 previous_response_id 时，自动把缓存的 reasoning/function_call 前缀拼进 input，但保留 previous_response_id。[proxy.go:1227–1228](../../internal/handler/proxy.go#L1227-L1228) 缓存流式输出。

**确定行为：** 一个原本只有一项 function_call_output 的请求，被扩为 reasoning + function_call + function_call_output。若客户端已有部分 function_call，缺另一个时会把整组缓存前缀加入，可能重复。缓存只按 response ID，全局共享，30 分钟过期；命中、未命中、进程重启及前轮是否流式都会改变转发行为。

**影响：** 这是有状态改写，不是透明反代；可能造成重复条目、状态链冲突或难复现的多轮工具错误。与项目 [AGENTS.md:251–252](../../AGENTS.md#L251-L252) 的明确约定不符。

**修复方向：** 移除 native Responses 的缓存 / 拼接 / replay，完整保留原始 input、previous_response_id、reasoning 和工具条目；若特定上游确需兼容策略，应显式隔离启用而非暗中修改所有透传请求。

### P1-C：上游流中断被记作成功（源码与本地 mock 均确认）

1. [proxy.go:1140–1144](../../internal/handler/proxy.go#L1140-L1144) 把来自上游 resp.Body 的 `io.ErrUnexpectedEOF` 当作客户端断开，甚至客户端 context 未取消也返回 nil。
2. [proxy.go:1021–1028](../../internal/handler/proxy.go#L1021-L1028) 在已输出后，把流式读取/idle/写入错误转为 nil；[proxy.go:706–708](../../internal/handler/proxy.go#L706-L708) 因此执行 RecordSuccess。
3. 正常 EOF 不校验协议终止事件；原生透传不会因此自动生成成功帧，但内部仍可能把不完整流当成功。协议转换路径还会主动 Finish，见 P1-E。

**影响：** DSH 收到半截流/缺终止事件，但 zero-api 判断成功、清除熔断，导致坏渠道继续被选中；监控与用户感受不一致。

**修复方向：** 区分 success / client_cancelled / upstream_failed，以及 response_started。已输出后禁止跨渠道重试是正确的，但不等于成功；按协议发错误或异常结束连接，保留上游失败分类，不清除熔断。

### P2-D：客户端取消未传播至 CPA（源码与本地 mock 均确认）

[proxy.go:945–953](../../internal/handler/proxy.go#L945-L953) 使用 context.Background 而非请求 context；流式循环仅在读写错误后检查取消，没有监听取消并关闭上游。

**影响：** DSH 取消/重试后，旧请求可能继续占用 CPA/Codex 并发和额度；上游无帧时等 idle，持续心跳时可能持续运行。不要将其描述为所有断连都会永久泄漏。

**修复方向：** 上游 context 派生自 c.Request.Context，客户端取消及时关闭上游；取消不计渠道故障，避免引发错误熔断。

## 3. 只有实际发生协议转换时才生效的严重问题

### P1-E：流式工具状态机损坏（离线复现）

**Responses → Chat：** [responses.go:386–438](../../internal/adapter/responses.go#L386-L438)

- 第一段输出直接为 function_call 时，不发工具 name / id，只发 arguments。
- 之前有文本时，工具 ID 又使用 `item.id`（fc_*），不是 `call_id`（call_*）。
- 完成原因不是 tool_calls；并行工具增量按最近序号处理，没有按 item_id/output_index 建立映射。

**Chat → Responses：** [downstream_responses.go:418–449](../../internal/adapter/downstream_responses.go#L418-L449)、[480–527](../../internal/adapter/downstream_responses.go#L480-L527)

- 工具 added / arguments.delta 后，没有工具 arguments.done / output_item.done。
- 最终 response.completed.output 恒为空；并行工具增量可能落到最近条目。

**影响：** 纯文本聊天可能正常，但 Agent 在发起工具、工具回传或并行工具阶段失败。原生 Responses 同协议透传不经过这些转换器，不能把本条当作所有 CPA 请求的根因。

**额外：** Responses 上游转换忽略 response.failed 等错误事件，Finish 默认输出 stop + [DONE]；下游 Responses Finish 无条件 completed。上游干净 EOF 但没有真正完成时，转换器可能伪造成功终止。

**修复方向：** 以 call_id / item_id / output_index 建立完整工具状态机；保留终止状态和错误；用纯工具首帧、并行交错工具、显式失败和缺终止事件的测试验收。

### P2-F：合法工具参数在转换中被拒绝或丢弃（离线复现）

[downstream_responses.go:32–57](../../internal/adapter/downstream_responses.go#L32-L57) 将 tool_choice 定义为 string，合法对象 `{"type":"function","name":"read"}` 反序列化报错；Reasoning 和 ToolChoice 即使读入，也未完整写回 canonical。

**影响：** 受影响的合法请求最终可能变成“所有渠道均失败”502，看起来像上游网络故障，实际是本地协议转换失败。原生透传不受此条影响。

## 4. 熔断及 HTTP 错误处理问题

### P1-G：冷却被请求自行清除，可能多出一轮 503（源码与本地 mock 均确认）

[proxy.go:719–730](../../internal/handler/proxy.go#L719-L730) 在候选全部冷却时主动 RecordSuccess 清除熔断，但当前请求仍返回 503。

**单渠道场景：** 上游失败 → 本次 502；下一次不访问上游、先返回 503 并解封；再下一次才访问上游。即使 CPA 已恢复，紧随上次失败的请求也可能先被本地拒绝。持续故障时又破坏了预期 5 分钟冷却。

**修复方向：** 保持明确的 open/half-open/closed 语义；若要立即尝试最后可用渠道，应受控执行，而非宣告成功却让当前请求失败。

### P2-H：健康探测把 401/403/429 当成功，且请求协议不正确（源码与本地 mock 均确认）

[circuit_breaker.go:140–195](../../internal/handler/circuit_breaker.go#L140-L195) 总发 Chat 的 messages/max_tokens，不通过 ConvertRequest；认证只用全局 probeAPIKey，不优先渠道 Key；只有 >=500 才失败。因此 Responses 渠道可能收到错误请求体，未认证返回 401 却被恢复为健康。

**修复方向：** 使用目标协议、正确凭据、模型 URL 和请求 context；只接受可证明推理可用的成功响应。半开探测需要并发门闩，避免并发请求同时探测。

### P2-I：400 等 JSON 错误被错误标记成 SSE（源码与本地 mock 均确认）

[proxy.go:1013–1021](../../internal/handler/proxy.go#L1013-L1021) 只把部分状态作为失败；400/404/422 可以继续进入 streamResponse，并在 [1085](../../internal/handler/proxy.go#L1085) 强制 text/event-stream。

**准确影响：** HTTP 400 状态仍可能保留，不是统一变成 200；但正文为 JSON、Content-Type 却为 SSE，内部又记成功，破坏客户端错误解析与故障分类。

## 5. 条件性与次要事项

- **WebSocket 忽略渠道出站代理，首渠道连接失败无回落：** [responses_websocket.go:65–87](../../internal/handler/responses_websocket.go#L65-L87)。只有 DSH 实际使用 GET Upgrade 时才相关。
- **非流式响应头仍固定受 60 秒限制：** [upstream/client.go:12–23](../../internal/upstream/client.go#L12-L23)、[80–85](../../internal/upstream/client.go#L80-L85) 使用共享 Transport；流式可配置头超时已实现，不应重复报告为未修复。不要把 non-stream 60 秒问题套到所有 stream 请求。
- **慢客户端：** 清除下游写超时后，阻塞 Write 不会被“关闭上游 body”的 idle timer 解阻；需要下游写 deadline / 取消策略。这是资源风险，未做运行复现。
- **配置超时来源不同：** `/v1` HTTP 中转从数据库 proxy_config 读取 timeout；不是简单修改启动 YAML upstream timeout 就会改变此链路。
- **CPA 自身：** 当前仓库主要管理其二进制/配置，不包含正在部署的 CPA 完整运行证据；无法确认实际 CPA 版本、OAuth 账号路由、额度和重试行为。

## 6. 验证记录

- `go test ./...`：adapter、metrics、openrouter、pricing 通过。完整测试未通过 setup：默认依赖源 TLS handshake timeout，且 cmd/server 缺少前端 embed assets。不是已证明的测试断言失败。
- 对当前 adapter 源码的隔离副本运行 6 项“正确行为”测试，6 项均失败并复现：session_id 丢失、原始 input 被 replay 扩写、首工具 name/id 丢失、call_id 错用、工具缺完成事件、对象 tool_choice 被拒绝。隔离副本与测试已清理。
- handler 的 6 个 SSE 主测试（另含 2 个 failure 子用例）全部通过“缺陷行为复现”断言，证实上游 unexpectedEOF 被当成功、部分输出后 idle 错误返回 nil、取消不传播、400 JSON 错标 SSE、失败/缺终止事件伪成功。测试通过表示缺陷被复现，不表示系统健康。
- 2 个熔断 mock 测试通过缺陷复现断言：401 探测被视为健康；单渠道 4 次请求依次返回 `[502,503,502,503]`，上游只被访问 2 次。
- 依赖下载切换为本次命令级 GOPROXY 镜像后，`go test ./internal/adapter ./internal/handler ./internal/store ./internal/upstream ./internal/pricing ./internal/cpa -count=1` 全部通过。未更改系统或项目 Go 代理配置；未调用真实模型上游。现有测试通过不覆盖新发现的断言缺口。
- 所有仓库内临时审计测试最终清理，不改业务源码；报告保留作为交付。

### 额外发现：新数据库首次初始化顺序错误（本地复现，通常不是已有部署的间歇性根因）

[db.go:244–324](../../internal/store/db.go#L244-L324) 先执行 ALTER TABLE 且忽略错误，再在 [326–331](../../internal/store/db.go#L326-L331) 执行 CREATE TABLE。新库首次启动时 ALTER 因表不存在失败，随后建表又缺 priority/use_proxy 等后加列；本地首次 Open 后渠道创建确实报 `table channels has no column named priority`。第二次 Open 才补齐。应改为先建基础表、再有序迁移并检查非“列已存在”错误；不建议用重启作为正式修复。熔断测试用第二次 Open 模拟已初始化部署后才进行复现，以免混淆两个问题。

## 7. 推荐落地顺序

1. 先确认 DSH 实际调用的 URL、协议、stream 以及具体错误文本；确认 CPA 渠道和模型都声明支持 Responses，避免隐式转换。
2. 修 native Responses 会话头与移除隐式 replay，补两轮工具调用保真测试。
3. 修流式 outcome / cancel / 终止校验，禁止把失败记为成功；保留已输出后不 failover 的安全边界。
4. 修熔断冷却与探测；增加单渠道恢复、多渠道回落、客户端取消测试。
5. 单独修工具转换状态机和结构化参数；若必须转换，在完成验收前明确标注限制。
6. 最后做真实链路 A/B：同一个最小工具调用用例，直连 CPA 与经 zero-api 各跑数轮，按 request-id 对齐日志。不要自动重放真实有副作用的工具调用。

## 8. 最小补充证据（均应脱敏）

- DSH 报错全文、发生时间（带时区）、实际模型名、provider 协议、是否流式。
- zero-api 与 CPA 实际版本，以及是否还有 Nginx/CDN 等外层代理。
- 相关时段 zero-api 日志、CPA 日志；只需要 request-id、状态码、耗时、错误信息和结构信息。
- 渠道 type/protocols、模型 protocols、超时/熔断设置，以及是否配置多渠道/多账号；不提供 API Key、OAuth token、Cookie 或完整敏感提示词。
