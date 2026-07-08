# 上下文管理:一次 LLM 调用里有什么、怎么裁、怎么看

> 本篇从源码 + 实测两个层面,厘清 trpc-agent-go 在一次 LLM 调用里**组装了什么上下文、接近极限时怎么裁、怎么把上下文结构导出给前端**。
> 与 [claude-code-compact-design.md](claude-code-compact-design.md)(Claude Code 分级流水线对照)和 [TODO.md](TODO.md) T1(上下文压缩升级)互为补充。
>
> 核实日期:2026-07-08,分支 `feat/lightai`。所有结论以源码 + 实测 dump 为准。

## 0. 先找到"真源":最终发给 LLM 的请求长什么样

整个框架做了那么多事,最后落到一次 LLM 调用上的,就是一个 `*model.Request`(`model/request.go:506`):

```go
type Request struct {
    Messages []Message          // 对话历史,核心 —— 顺序就是模型看到的顺序
    GenerationConfig            // stream / temperature / max_tokens / thinking 等
    StructuredOutput *StructuredOutput  // 结构化输出 schema(有则注入)
    ExtraFields map[string]any   // provider 特有顶层字段
    Headers map[string]string    // provider 特有 HTTP 头
    Tools map[string]tool.Tool   // 工具集(不序列化,但会发给 provider)
}
```

每条 `Message`(`model/request.go:64`)就是上下文的一个单元:

```go
type Message struct {
    Role Role              // system / user / assistant / tool
    Content string         // 文本
    ContentParts []ContentPart  // 多模态:text / image / audio / file
    ToolID, ToolName string     // role=tool 时:这是哪个工具的结果
    ToolCalls []ToolCall         // role=assistant 时:模型要调哪些工具
    ReasoningContent string      // thinking 链(deepseek/hunyuan)
    ReasoningSignature string    // thinking 签名(bedrock claude)
}
```

所以"上下文里有什么"= `Request.Messages` 这条 slice 里按顺序排了哪些 message,加上 `Request.Tools` 这个 map 里挂了哪些工具。**这条 slice 就是真源**。

### 怎么拿到真源:BeforeModel 回调

拦截"即将发给 LLM 的完整请求"最直接、最稳的点是 `BeforeModel` 回调(`model/callbacks.go:64`,调用点 `internal/flow/llmflow/llmflow.go:2316`)。它在 preprocess 组装 + 裁剪之后、真正发请求之前触发,拿到的就是投影后的最终态,而且可改写。`examples/context_compaction/main.go:172-184` 已经用这个回调 + `-debug` flag 打印最终请求,注释直说"the callback sees the final request produced by the agent flow, after history projection and context compaction. This is the most direct way to verify what will be sent to the provider."

次选是 OTel trace:`telemetry.TraceChat`(`llmflow.go:1102`)把整个请求体序列化成 span attribute(`gen_ai.input.messages` 等,`internal/telemetry/trace.go:558-584`),适合事后观测,但受 `SpanAttributePolicy` 截断,不适合需要完整原文的场景。

---

## 1. 上下文里会出现什么:RequestProcessor 责任链

一次 LLM 调用前,`llmflow.runOneStep`(`llmflow.go:680`)先跑 `preprocess`(`llmflow.go:716 / :1396`),让一串 `RequestProcessor` 依次处理同一个 `*model.Request`。每个 processor 往 `req.Messages` / `req.Tools` / `req.GenerationConfig` 里塞东西。顺序大致是:

| 顺序 | Processor | 文件 | 往上下文里加什么 |
|------|-----------|------|------------------|
| 1 | BasicRequestProcessor | `processor/basic.go:56` | `GenerationConfig`(stream/temp/...)/ `ExtraFields` / `Headers` / `StructuredOutput` |
| 2 | InstructionRequestProcessor | `processor/instruction.go:158` | **system message 主体**:SystemPrompt(前置)+ Instruction(后置),合并进 system;支持 `{{state.xxx}}` 占位符渲染;有 OutputSchema 时追加 JSON 输出指令 |
| 3 | ContentRequestProcessor | `processor/content.go:640` | **核心**:历史消息拼接(见 1.1);summary / preload memory / recall 注入;reasoning_content 处理;context compaction Pass 0/1/2;当前用户消息 |
| 4 | SkillsRequestProcessor | `processor/skills.go:355` | skill 概览 `Available skills:` + 已加载 skill 正文 `[Loaded] <name>` + 文档,合并进 system(开了 `WithSkillsLoadedContentInToolResults(true)` 则进 tool result) |
| 5 | PostToolRequestProcessor | `processor/posttool.go:27` | `[Tool Prompt]` 工具使用指引(有 tools 就注入:"Analyze the tool result...Reuse successful tool results...Final Answer Requirement...") |
| 6 | (tools 装载) | `llmflow.go:533-536` | `invocation.Agent.Tools()` → `req.Tools`(含 function / MCP / skill / transfer_to_agent / knowledge_search / session_search 等) |
| … | identity / planning / conditional / time / transfer 等 | `processor/*.go` | 身份、规划、条件、时间戳、sub-agent 描述等按需注入 |

**关键机制:system message 始终合并成一个**。instruction / content(summary/memory/recall)/ skills / posttool 都往 system 里塞,但 `injectSystemContextMessage`(`content.go:895`)和 `updateExistingSystemMessage`(`instruction.go:331`)都是「有 system 就 `Content += "\n\n" + ...`,没有就前置插入」。这保证请求里**始终只有一个 system message**,兼容 Qwen 等不支持多个 system 的模型。

### 1.1 ContentRequestProcessor:历史消息怎么拼

`ProcessRequest`(`content.go:640`)的执行顺序:

1. `injectInjectedContextMessages`(:662)—— 调用方 per-run 注入的上下文消息(`RunOptions.InjectedContextMessages`)
2. `injectFewShotMessages`(:663)—— few-shot 示例
3. `appendSessionMessages`(:665)—— 核心,从 session 事件拼历史:
   - 先准备 user-context blocks:preload memory / summary / preload recall(各自可 system 或 user 模式注入)
   - `sessionMessagesAfterCutoff`(:764)取摘要边界之后的事件 → `getIncrementMessagesAfterCutoff`(:1214):
     - `sessionEventsSnapshot` 取 session 全部事件
     - 按 summary cutoff / branch filter / timeline filter 过滤
     - `compactCurrentInvocationEvent`:summary 吸收后当前 invocation 的 tool result 压成占位符
     - `applyToolTranscriptMode`:`omit_previous_completed` 模式删历史完成的 tool 对
     - `rearrangeLatestFuncResp` / `rearrangeAsyncFuncRespHist`:重排 tool_call↔tool_result 配对
     - `compactIncrementEvents`:**context compaction Pass 0/1/2**(见第 2 节)
     - `processReasoningContent`:reasoning_content 处理(见 1.2)
     - `projectEventMessage`:EventMessageProjector 改写
     - `mergeUserMessages`:合并 `For context:` 前缀的外来 agent 消息
     - `applyMaxHistoryRuns`:轮次限制(`AddSessionSummary=false` 时)
4. 当前用户消息(:673,若没被历史包含)
5. `injectLateContextMessages`(:689)—— 插在最新 user 消息前

### 1.2 reasoning_content(thinking)怎么处理

默认 `ReasoningContentModeDiscardPreviousTurns`(`content.go:127`):**丢弃普通历史轮的 reasoning_content,但保留当前请求和有 tool call 的历史轮**。原因是 DeepSeek thinking mode 要求 tool-call 的 reasoning 在后续轮回放。实测验证(见第 4 节):第一轮 assistant 的 `reasoning_bytes=53` 在第二轮历史里仍保留,因为那条带 tool_calls。

可选 `keep_all`(全留,调试用)/ `discard_all`(全丢,省 token)。

### 1.3 sub-agents 描述怎么进来

sub-agent 不是单独一块进 system,而是通过 `transfer_to_agent` 这个框架工具的 description 暴露——该工具由框架自动注册,描述里列出有哪些 sub-agent 可转交。模型调它即转交控制权。`functioncall.go:3010` 还会把模型直接喊 sub-agent 名字的行为映射到 `transfer_to_agent`。

### 1.4 rules(类 CLAUDE.md)怎么进来

框架没有专门的"rules 文件读取"机制。`InstructionRequestProcessor` 的 `Instruction` / `SystemPrompt` 字段就是放规则的地方——调用方自己把 CLAUDE.md 式的内容塞进 `llmagent.WithInstruction(...)` 或 `WithSystemPrompt(...)`。框架只负责渲染 `{{state.xxx}}` 占位符。所以"rules"就是 instruction 的一部分,进 system message。

---

## 2. 接近极限怎么处理:裁剪三件套

一次 LLM 调用前,上下文被三层依次裁剪(时序来自 `llmflow.runOneStep` `:680`):

### 2.1 第一层:context_compact(flow 层,preprocess 阶段)

`compactIncrementEvents`(`processor/context_compact.go:247`),在 `ContentRequestProcessor` 内部对"已筛过的 events"跑,改的是 tool result 的 `Content`。三个 Pass 顺序固定:

| Pass | 触发 | 动作 | 可恢复 | 文件 |
|------|------|------|--------|------|
| Pass 0 | `ForceCleanToolNames` 命中 | 强制替换成 `policyToolResultPlaceholder`(无 event_id) | ❌ | `context_compact.go:345` |
| Pass 1 | 历史 tool result > `ToolResultMaxTokens`(默认 1024) | 全量替换成 `recoverableToolResultPlaceholder`(带 event_id/tool_call_id) | ✅ `session_load` 取回 | `context_compact.go:378` |
| Pass 2 | 任意 tool result > `OversizedToolResultMaxTokens`(默认 0 关) | head+tail 截断,中间插 marker | ⚠️ 部分(marker 带 event_id,中间内容已丢) | `context_compact.go:445` |

**受保护**(:533):当前请求 + 最近 `KeepRecentRequests`(默认 1)个完成的请求 + `SkipRecentFunc` 指定的尾部。`Enabled=false` 是真正的"框架不动 tool result"开关。

占位符格式(实测 dump,见第 4 节):
```
[elided] Previous tool call succeeded and its result was already consumed by the assistant;
payload has been dropped to save context. Do NOT re-invoke the same tool with the same arguments...
event_id: <evt.ID>
tool_call_id: <msg.ToolID>
tool_name: <msg.ToolName>
reason: historical_compaction
Use session_load with event_id and content_offset/content_limit if the full result is needed.
```

### 2.2 第二层:maybeCompactContextBeforeLLM(同步摘要兜底)

`llmflow.go:1479`,在 preprocess 之后、callLLM 之前。**触发条件全满足才跑**(`:1495`):
- `enableContextCompaction==true`
- 有 session + session service
- ContentProcessor 开了 `AddSessionSummary==true && TimelineFilterAll`
- `tokenCount >= contextWindow × 0.7`(下限 2000)
- 连续失败 < 3 次(熔断器,`:1533`)

触发后:`CreateSessionSummary`(:1647,同步调 LLM 生成摘要,唯一 API 成本)→ 若 summary 推进,`rebuildRequestForContextCompaction`(:1734)clone preprocess 前的快照,重跑 ContentRequestProcessor(注入新 summary + Pass 0/1/2 再跑一遍)→ 重跑 tailProcessors(skill 正文重物化)。

### 2.3 第三层:token_tailor(model 层,发请求前最后兜底)

`MiddleOutStrategy.TailorMessages`(`model/token_tailor.go:526`),在 provider 内 `applyTokenTailoring`(如 `model/anthropic/anthropic.go:198`)调用。按 token budget **删中间整轮消息**(Lost-in-the-Middle 理论):保 system 头 + 最后一轮,删中间 user-anchored 轮次。最小保护上下文(system + 末轮)仍超 → 返回 overflow error(不静默丢 system)。**硬删不可恢复**(Message 无 event_id,model 层拿不到 session)。

### 2.4 三层协作时序

```
runOneStep (llmflow.go:680)
│
├─1. preprocess (:716)
│    └─ ContentRequestProcessor → compactIncrementEvents
│       【Pass 0/1/2,作用在 event 投影上,改 tool result Content】
│
├─2. maybeCompactContextBeforeLLM (:720)
│    【条件:超 0.7 阈值 && 熔断未触发】
│    └─ 同步刷 summary + 重建请求(Pass 0/1/2 再跑一遍)
│
└─3. callLLM (:748) → provider applyTokenTailoring
    【MiddleOut 删中间轮次,保 system 头 + 末轮,超则 overflow error】
```

**职责边界**(与 [claude-code-compact-design.md](claude-code-compact-design.md) §5 一致):可恢复归 flow 层(compaction 占位符 + session_load 闭环);硬预算保命归 model 层(tailoring 硬删中间)。两层正交。

---

## 3. 怎么把上下文结构导出给前端

这是用户最关心的"可视化"。现状评估:

| 能力 | 位置 | 暴露内容 | 够不够"顺序+token+原文" |
|------|------|----------|------------------------|
| **BeforeModel 回调** | `model/callbacks.go:64`,调用 `llmflow.go:2316` | 完整 `*model.Request`(Messages 顺序、Tools、GenerationConfig),可改写 | ✅ 原文、顺序都在;token 用 `TokenCounter` 算 |
| OTel trace span | `llmflow.go:1102` → `telemetry/trace.go:558` | 完整请求 JSON + `gen_ai.input.messages` + 工具定义 + 响应 usage | ⚠️ 顺序+原文够(受 policy 截断);token 在响应 `Usage.PromptTokens` |
| preprocessing 事件流 | `processor/*.go` emit `ObjectTypePreprocessing*` | 每个处理器"我处理了什么"的状态信号 | ❌ 不含最终消息序列原文 |
| AG-UI SSE | `server/agui/` | 只翻译 Runner 事件流(user message / text delta / tool call / artifact)给前端 | ❌ **不暴露** request messages / preprocessing 事件 |
| latency diagnostics | `llmflow/diagnostics.go` | `preprocessing.status` + span(只记 messages/tools **数量**) | ❌ 只给计数和耗时 |

**结论:给前端返回"上下文结构化 JSON(顺序、token、原文)",最可行的接入点是 `BeforeModel` 回调**。已有 `examples/context_compaction` 跑通的 `-debug` 模式可直接复刻(把 `printProjectedRequest` 换成"序列化成 JSON 推给前端")。

### 3.1 token 估算能力

`TokenCounter` 接口(`token_tailor.go:71`):
```go
type TokenCounter interface {
    CountTokens(ctx, message Message) (int, error)                    // 单条
    CountTokensRange(ctx, messages []Message, start, end int) (int, error)  // 区间
}
```
内置 `SimpleTokenCounter`(`token_tailor.go:108`,按 UTF-8 rune / 4 估算);`model/tiktoken/` 有真实 tokenizer。要给前端精确预估,注入 tiktoken 实现即可。真实 token 数在 LLM 响应的 `rsp.Usage.PromptTokens`(`telemetry/trace.go:608` 记为 `gen_ai.usage.input_tokens`)。

### 3.2 给前端的推荐方案

1. 写一个 `BeforeModel` 回调:把 `args.Request.Messages` 映射成 `[{index, role, tool_name, tool_id, content, content_parts, tool_calls, estimated_tokens}]`,附 `total_estimated_tokens` / `tools` 列表 / `generation_config`。
2. 回传通道:Runner 事件流目前没有"上下文快照"事件类型。最轻量做法是用 `event.WithExtension(key, payload)`(preprocessing status 事件就这么挂,见 `diagnostics.go:185`)挂一个 `context_snapshot` 自定义事件,agui 已能透传自定义事件,前端按 extension key 取。
3. 次选:若前端已接 OTel collector,直接读 `gen_ai.input.messages.otel` span attribute,零代码改动——但受 `SpanAttributePolicy` 截断,只能事后看。

**真源就是 BeforeModel 看到的那个 `*model.Request`**,没有比它更"真"的了——它已经是组装 + 裁剪后的最终态。

---

## 4. 实测验证(不瞎猜)

### 4.1 真实网关实测(CodeBuddy + glm-5.2,完整 tool 循环)

把 `examples/context_compaction` 临时改成 `model/codebuddy` provider(网关 `copilot.tencent.com/v2/chat/completions`,强制 stream=true),用 `glm-5.2` 跑(非 claude 系、usage 失真轻、成本低)。key 额度恢复后完整跑通两轮 + 三次 LLM 调用:

**第 1 次 LLM 调用**(模型决定调工具):
```
messages=2 tools=[large_log]
[00] role=system  content_bytes=1129  → "Demonstrates prompt-side context compaction...[Tool Prompt] Analyze the tool result..."
[01] role=user    content_bytes=83    → "Call the large_log tool with lines=20, then summarize..."
→ 模型返回 tool_call: large_log args={"lines": 20}
```

**第 2 次 LLM 调用**(工具执行后,tool_result 完整保留——当前请求受保护):
```
messages=4 tools=[large_log]
[00] role=system  (同上,1129 bytes)
[01] role=user    (第一轮用户消息,83 bytes)
[02] role=assistant  tool_calls=[large_log]  content_bytes=0  (真实 tool_call_id: call_306699f56d044f508157e784)
[03] role=tool tool_name="large_log" tool_id="call_306699f56d044f508157e784" content_bytes=2675
     → "{\"summary\":\"generated 20 synthetic log lines\",\"log\":\"line 0000 service=payment...\"}"
→ 模型正确总结了首尾日志行(line 0000 / line 0019)
```

**第 3 次 LLM 调用**(第二轮,`-keep-recent-requests=0` → 历史 tool_result 被压成占位符):
```
messages=6 tools=[large_log]
[00] role=system  (同上)
[01] role=user    (第一轮用户消息)
[02] role=assistant  tool_calls=[large_log]  content_bytes=0  (tool_call_id: call_306699f56d044f508157e784)
[03] role=tool tool_name="large_log" tool_id="call_306699f56d044f508157e784" content_bytes=447
     → "[elided] Previous tool call succeeded and its result was already consumed by the assistant;
        payload has been dropped to save context. Do NOT re-invoke...
        event_id: da4762c2-c10c-4b8...
        tool_call_id: call_306699f56d044f508157e784
        tool_name: large_log
        reason: historical_compaction
        Use session_load with event_id and content_offset/content_limit if the full result is needed."
[04] role=assistant  content_bytes=683  → "Here's the summary of the first and last log lines..." (第一轮的真实总结)
[05] role=user    content_bytes=77  → "What did the previous large_log tool return? Answer from the session history."
→ 模型回答:"20 synthetic diagnostic log lines, payment service, INFO, req-0000 到 req-0019..."
  并自述:"The full tool result was already consumed during processing, so this summary reflects what was retained."
```

**这次真实实测比 mock 强在三点**:
1. **真实模型行为**:glm-5.2 真的调了工具、真的总结了、第二轮真的从历史回答——不是预设的 canned response。
2. **真实压缩 + 实战价值验证**:`[03]` tool_result 从 2675 字节压成 447 字节占位符(带真实 `event_id: da4762c2-...` + `tool_call_id`),模型虽然看不到原始日志了,但**从前面 `[04]` assistant 的总结里拿到了信息,正确回答了第二轮问题**。这证明压缩是"把原始数据移出活动上下文,靠 assistant 总结 + 占位符线索不丢信息"——不是有损丢弃。
3. **可恢复闭环坐实**:占位符明确告诉模型"用 `session_load` with `event_id` 取回完整结果"。

> 之前的旧实测(2026-07-08 上午)因 CodeBuddy 账号额度耗尽(`429 / code:14019`)只 dump 到第 1 次 request、看不到 tool 循环。key 额度恢复后重跑,完整链路验证成功。

### 4.2 离线补充验证(mock model,验证 reasoning_content 保留)

真实网关用 glm-5.2 不返回 `reasoning_content`(只有 deepseek/hunyuan 才有该字段),所以单独用 mock model(`model.Model` 接口,按调用次数返回:tool_call → final → final,并预设 assistant 带 `ReasoningContent`)验证 reasoning 处理:

**第 2 轮第 3 次 LLM 调用**(`-keep-recent-requests=0`):
```
[02] role=assistant tool_calls=[large_log] reasoning_bytes=53  (reasoning 仍保留!有 tool call 的历史轮)
     content(28 bytes): "I'll collect the logs first."
[03] role=tool tool_name="large_log" tool_id="call_mock_1"
     content(429 bytes): "[elided] ... event_id: 500d86a7-... reason: historical_compaction
     Use session_load with event_id..."
```

验证点:① `[02]` 的 `reasoning_bytes=53` 在第二轮历史里仍保留——`ReasoningContentModeDiscardPreviousTurns`(`content.go:127`)默认会丢弃普通历史轮的 reasoning,但**保留有 tool call 的历史轮**(deepseek thinking mode 要求 tool-call reasoning 后续轮回放);② 历史 tool_result 压成占位符带 `event_id`,可恢复闭环坐实。

### 4.3 KeepRecentRequests 保护机制对比

用真实网关(glm-5.2)对照两种配置,看第二轮 `[03]` 历史 tool_result 的命运:

| 配置 | 第 2 轮 [03] tool_result | 原因 |
|------|--------------------------|------|
| `keep-recent=1`(默认) | 完整 2675 字节 | 第一轮是"最近 1 个完成的请求",受保护,Pass 1 不压 |
| `keep-recent=0` | 压成 447 字节占位符(带 event_id) | 只保护当前请求,第一轮变历史,被 Pass 1 压 |

(mock model 实测同此规律,字节数 2201→429,占位符格式一致。)

---

## 5. 关键文件索引

| 关注点 | 文件 | 一句话 |
|--------|------|--------|
| 请求真源结构 | `model/request.go:506` | Request(Messages + GenerationConfig + Tools + ...) |
| Message 结构 | `model/request.go:64` | Role/Content/ToolCalls/ReasoningContent... |
| 拦截真源 | `model/callbacks.go:64` + `llmflow.go:2316` | BeforeModel 回调 |
| 组装主链路 | `internal/flow/llmflow/llmflow.go:680`(runOneStep)、`:716`(preprocess)、`:1417`(processor 循环) | RequestProcessor 责任链 |
| system 主体 | `internal/flow/processor/instruction.go:158` | SystemPrompt + Instruction 合并 |
| 历史拼接 | `internal/flow/processor/content.go:640` | session 事件 → messages |
| reasoning 处理 | `internal/flow/processor/content.go:1805` | discard_previous_turns 默认 |
| skill 注入 | `internal/flow/processor/skills.go:355` | 概览 + 正文进 system |
| [Tool Prompt] | `internal/flow/processor/posttool.go:27` | 工具使用指引 |
| tools 装载 | `internal/flow/llmflow/llmflow.go:533` | invocation.Agent.Tools() → req.Tools |
| Pass 0/1/2 | `internal/flow/processor/context_compact.go:247` | tool result 占位符/截断 |
| 同步摘要兜底 | `internal/flow/llmflow/llmflow.go:1479` | 超 0.7 阈值刷 summary 重建 |
| MiddleOut 裁剪 | `model/token_tailor.go:526` | 删中间轮次保头尾 |
| token 估算 | `model/token_tailor.go:71` | TokenCounter 接口 |
| OTel 请求记录 | `internal/flow/llmflow/llmflow.go:1102` → `internal/telemetry/trace.go:558` | gen_ai.input.messages |
| dump 范例 | `examples/context_compaction/main.go:172` | -debug + BeforeModel |

---

## 6. 一句话总结

**一次 LLM 调用的上下文 = 一个 `system` message(合并了 description+instruction+skill 概览/正文+[Tool Prompt]+summary+memory) + 一串按时间顺序的 user/assistant/tool 消息(assistant 带 tool_calls 和可能的 reasoning_content,tool 带 tool result) + 一个 Tools map**。组装靠 `RequestProcessor` 责任链,裁剪靠"flow 层 Pass 0/1/2(可恢复占位符)→ maybeCompactContextBeforeLLM(同步摘要)→ model 层 MiddleOut(硬删中间)"三层。要看真实上下文,挂 `BeforeModel` 回调 dump `*model.Request`——那就是真源,没有比它更真的了。

> 关联:[claude-code-compact-design.md](claude-code-compact-design.md)(Claude Code 五层流水线对照 + gap 清单)、[TODO.md](TODO.md) T1(上下文压缩升级 backlog)。本篇的 gap 发现(已有熔断器 `llmflow.go:1533`,与 `claude-code-compact-design.md:160` 旧记"无熔断器"不符,笔记待更新)已记入。
