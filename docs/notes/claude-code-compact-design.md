# Claude Code 上下文压缩(compact)设计精华

> 2026-07-06 精读 `claude-code/src/services/compact/` 全部 11 文件(~3960 行)提炼。
> 目的:为 trpc-agent-go 的 [T1 上下文压缩升级](TODO.md) 提供设计参照,并标注双方差距。
> 关联:[benchmark/summary/results/REPORT.zh_CN.md](../../benchmark/summary/results/REPORT.zh_CN.md)(summary_ondemand 实证数据)、[TODO.md T1](TODO.md)。

---

## 0. 一句话总览

Claude Code 的上下文压缩是一条 **5 层分级流水线**,遵循四条原则:**先轻后重**(能用轻量手段达标就不上 LLM 摘要)、**可恢复优先**(折叠/占位符优先于硬删)、**压缩后重试 + 熔断**(压缩成功就重新进入流水线,连续失败就停)、**LLM 全量摘要是最后兜底**而非第一选择。

---

## 1. 分级架构总览

| 层 | 名称 | 触发 | 动作 | 可恢复 | 成本 | 文件 |
|----|------|------|------|--------|------|------|
| L1 | 工具结果预算替换 | tool result 总和过大 | 截断/替换 tool_result 内容,按 `tool_use_id` | ✅(本地存 toolResultStorage) | 0 API | `query.ts:369-394` |
| L2 | 历史截断 snip | 特性门控 `HISTORY_SNIP` | 删旧中间消息,保留头尾 | ❌ 硬删 | 0 API | `query.ts:400-410` |
| L3a | 时间基微压缩 | 距最后 assistant >gap 分钟(缓存冷) | 本地 content 清成 `[Old tool result content cleared]`,保最近 N 个 | ❌(content 覆盖) | 0 API | `microCompact.ts:446-530` |
| L3b | 缓存编辑微压缩 | 工具数 ≥门槛 + 缓存热 | API `cache_edits` 删 `tool_use_inputs`,**本地消息不动** | ✅(本地未变) | 0 API(保活缓存) | `microCompact.ts:305-399` |
| L4 | 会话内存压缩 | SM 提取完成 + 内存非空 | 用 claude.md 摘要替代旧消息,保末尾 N 条 | ⚠️ 部分(摘要不可展开) | 0 API(读已提取的 claude.md) | `sessionMemoryCompact.ts` |
| L5 | 全量 LLM 摘要 | token ≥ autocompact 阈值 | forked agent 生成九段式摘要,替代历史 | ✅ 部分(boundary 后可续,含附件恢复) | ~4K input tokens | `compact.ts` + `autoCompact.ts` |

**编排顺序**(`query.ts:365-545` 主循环):每轮 LLM 调用前,先跑 L1→L2→L3→(L4/L5);若触发了 L4/L5 压缩,则 `continue` **重新进入流水线**(给 L1-L3 再一次机会),而不是直接发请求。

---

## 2. 各层精华设计

### L1 工具结果预算替换(`applyToolResultBudget`)
- 按 `tool_use_id` 定位,**不看内容本身**,只看"这个工具结果占多少 token、是否超预算"。
- 与下游 microcompact **正交组合**(一个管"整段删/留",一个管"内容内截断")。

### L3 微压缩:缓存感知分流(核心创新)
`microCompact.ts` 的分流逻辑是最大亮点:

- **time-based 先跑且短路**(`maybeTimeBasedMicrocompact`):距最后 assistant 消息超过 `gapThresholdMinutes` → 说明服务端缓存已失效,全 prefix 都要重写 → 此时**本地 content-clear 是免费的**(反正要重写),直接把旧 tool result 改成 `[Old tool result content cleared]`。
- **cached MC 仅在缓存热时跑**(`cachedMicrocompactPath`):缓存还热 → 用 API 的 `cache_edits` block 删 `tool_use_inputs`,**本地消息完全不动**,缓存前缀保活。
- 只压特定工具(`COMPACTABLE_TOOLS`:Read/Shell/Grep/Glob/WebSearch/WebFetch/FileEdit/FileWrite),不动别的。
- `keepRecent` 下限 1(`Math.max(1, ...)`)——至少留最近一个,避免清空导致模型零工作上下文。
- **只在 main thread 跑**,防 forked agent 把自己的 tool_results 注册到全局 state。

> 精华所在:**根据"改本地 content 会不会失效缓存"这个成本,选择两种完全不同的压缩方式**。缓存冷就大胆改本地,缓存热就用 API 侧 cache_edits 保活。这是把"上下文管理"和"prompt cache"两个正交能力协同起来的范例。

### L4 会话内存压缩(`sessionMemoryCompact.ts`)
- 触发条件:session memory(claude.md,后台异步提取)非空 + 提取完成。
- 向后扩展边界:至少 10K tokens / 至少 5 条文本块消息 / 不超 40K tokens(`minTokens`/`minTextBlockMessages`/`maxTokens`)。
- `adjustIndexToPreserveAPIInvariants()`:确保不拆分 `tool_use`/`tool_result` 对。
- **成功后直接结束 turn,不再跑 L5**——L4 是 L5 的低成本替代(0 API、<100ms,vs L5 的 5-10s)。

### L5 全量 LLM 摘要(`compact.ts:387` `compactConversation`)
- **cache-sharing fork 优先**(`streamCompactSummary:1179-1248`):用 `runForkedAgent(maxTurns:1)` 复用主线程的 prompt cache(system+tools+messages prefix),省 cache_creation。失败回退到普通流式。
  - 注意:**不设 maxOutputTokens**——设了会和主线程 thinking config 不匹配导致 cache 失效(注释 `compact.ts:1181-1187` 专门说明)。
  - keep-alive 信号:压缩要 5-10s,期间发心跳防 WebSocket idle 超时(`streamCompactSummary:1159-1176`)。
- **PTL(prompt-too-long)重试**(`compact.ts:450-470`):压缩请求本身超长时,`truncateHeadForPTLRetry` 按 API-round 分组删最旧(`compact.ts:243-291`),最多重试 `MAX_PTL_RETRIES` 次。
- **post-compact 附件恢复**(见 §3.6)。

---

## 3. 横切设计精华(跨层)

### 3.1 四级阈值体系(`autoCompact.ts:62-145`)

不是单一"超阈值就压",而是四级:

| 级别 | 计算 | 作用 |
|------|------|------|
| effectiveContextWindow | contextWindow - min(maxOutputTokens, 20K) | 为摘要输出预留 token(基于 p99.99 摘要=17387 token) |
| autocompactThreshold | effectiveWindow - 13K | **主动触发** L5,留缓冲 |
| warningThreshold | threshold - 20K | UI 黄色警告 |
| errorThreshold | threshold - 20K | UI 红色警告 |
| blockingLimit | effectiveWindow - 3K | 硬阻止(手动 `/compact` 仍可用) |

> 精华:**给系统充足缓冲**(不等到最后一秒)、**用户有清晰反馈**(黄→红→阻止)、**手动 /compact 永远留 3K**。

### 3.2 熔断器(`autoCompact.ts:70, 260-265, 341-349`)

`MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES = 3`,连续失败 3 次就停试。

有 BQ 数据支撑(注释 `autoCompact.ts:68-70`):1279 个 session 有 50+ 连续失败(最高 3272 次),**每天浪费 ~250K API 调用**。

`AutoCompactTrackingState` 跨轮跟踪:`{compacted, turnCounter, turnId, consecutiveFailures}`,成功清零,失败递增。

### 3.3 摘要 prompt 两段式 + 九段结构(`prompt.ts`)

**这是报告实证的最大质量杠杆**(九段式 detailed prompt 让纯 summary 逼近 long_context)。

- **NO_TOOLS_PREAMBLE(放最前)+ NO_TOOLS_TRAILER(放最后)**:双重强调"不要调工具"。原因:cache-sharing fork 继承父工具集,adaptive-thinking 模型有时会试图调工具,maxTurns:1 下被拒就没文本输出。
- **两段式**:`<analysis>` 草稿区(模型先想)+ `<summary>` 正文。`formatCompactSummary` 会**剥掉 analysis**(它是 scratchpad,提升质量但无信息价值)。
- **九段式 summary 结构**:
  1. Primary Request and Intent
  2. Key Technical Concepts
  3. Files and Code Sections(含完整代码片段 + 为什么重要)
  4. Errors and fixes(**特别关注用户反馈"让你换个做法"**)
  5. Problem Solving
  6. **All user messages**(非工具结果的用户消息,全列——理解用户意图变化的关键)
  7. Pending Tasks
  8. Current Work
  9. Optional Next Step(**含 verbatim 引用最近对话**,防漂移)
- **三变体**:BASE(全量)、PARTIAL(仅最近消息)、PARTIAL_UP_TO(摘要作为续接开头,多了 "Context for Continuing Work" 段)。

### 3.4 PTL 重试 + API-round 分组(`grouping.ts`, `compact.ts:243-291`)

- **API-round 分组**(`groupMessagesByApiRound`):按 assistant message.id 变化切组——一个 API round-trip 一组。比"按 user turn 分组"更细,能在单 user turn 的 agentic session 上工作。
- **PTL 重试**(`truncateHeadForPTLRetry`):压缩请求本身 prompt-too-long 时,按组删最旧。
  - 精准模式:从错误里提取 `tokenGap`,按组累加 token 直到覆盖 gap。
  - 兜底模式:删 20% 的组。
  - 删后若首条变 assistant(API 要求首条 user),插 synthetic user marker `PTL_RETRY_MARKER`。
  - `ensureToolResultPairing` 修复删出来的孤儿 tool_result。

### 3.5 压缩后重试循环(`query.ts:521-543`)

L4/L5 压缩成功 → `buildPostCompactMessages` 重组消息 → `continue` **重新进入主循环前处理**(再跑 L1-L3)→ 再次评估是否还超阈值。

这形成闭环:轻量层先消化,消化不了上重层,重层完成后轻量层再扫一遍。`snipTokensFreed` 贯穿传递给 autocompact 做精确阈值计算(L2 和 L5 联动)。

### 3.6 post-compact 附件恢复(`compact.ts:1415-1534`)

压缩后自动把"关键工作上下文"作为**附件**重新注入,避免压缩丢掉正在用的东西:

| 附件类型 | 函数 | 上限 |
|---------|------|------|
| 最近读过的文件 | `createPostCompactFileAttachments` | 最多 5 个(`POST_COMPACT_MAX_FILES_TO_RESTORE`),每个 5K token,总预算 50K |
| plan 文件 | `createPlanAttachmentIfNeeded` | — |
| invoked skills | `createSkillAttachmentIfNeeded` | 每个 5K,总预算 25K,按最近调用排序 |

- 文件恢复会**排除已保留段(messagesToKeep)读过的路径**,避免重复。
- skill 按最近调用排序,budget 压力下先丢最不相关的;每个 skill 截断保留头部(setup/usage 指令通常在头)。

> 这是"可恢复"的**第三种形式**(前两种:占位符 event_id、messagesToKeep 保留段)——压缩掉的不是真的没了,关键工作上下文会自动重注入。

### 3.7 `buildPostCompactMessages` 固定顺序(`compact.ts:330-338`)

`boundaryMarker → summaryMessages → messagesToKeep → attachments → hookResults`,所有压缩路径共用此顺序。

`annotateBoundaryWithPreservedSegment`:boundary 带 `preservedSegment{headUuid, anchorUuid, tailUuid}`,供 REPL loader 重链(head→anchor,anchor 的其它 children→tail)。

### 3.8 postCompactCleanup 的"有意不清"(`postCompactCleanup.ts`)

压缩后清理缓存/状态,但**有意不清 invoked skill content**(注释 `:65-69`):skill 内容要跨压缩保留,让 `createSkillAttachmentIfNeeded` 能在后续压缩里带上完整 skill 文本。否则每次压缩后重注入 skill 会是纯 cache_creation(~4K token 浪费)。

**子 agent 压缩不清主线程 state**(`isMainThreadCompact` 判定):子 agent 和主线程共享 module-level state,子 agent 压缩时清主线程的 context-collapse/memory cache 会污染主线程。

---

## 4. 对照 trpc-agent-go 现状

trpc-agent-go **已有**一套相当成熟的分级体系(见 [TODO.md T1](TODO.md) + iWiki Agent/Summary 文档),双方能力高度对应:

| Claude Code | trpc-agent-go 现状 | 状态 |
|-------------|-------------------|------|
| L1 工具结果预算替换 | `context_compact` Pass 1(历史 tool result 超阈值→占位符,带 event_id) | ✅ 已具备,**且更优**(占位符可恢复) |
| L2 历史截断 snip | `token_tailor` MiddleOut(删中间轮次) | ⚠️ 有,但**硬删不可恢复** |
| L3 微压缩(缓存感知) | `context_compact` Pass 2(超大 tool result head+tail 截断) | ⚠️ 有截断,但**无缓存感知分流** |
| L4 会话内存压缩 | `WithAddSessionSummary(true)` + summary 注入 | ✅ 已具备 |
| L5 全量 LLM 摘要 | `maybeCompactContextBeforeLLM` 同步刷新 summary + 重建请求 | ✅ 已具备(逼近上限时触发) |
| 四级阈值 | `ContextCompactionThresholdRatio`(单档 0.7) | ❌ **gap:只有单档,无 warning/error/blocking** |
| 熔断器 | 无 | ❌ **gap:无连续失败保护** |
| 摘要 prompt 两段式/九段 | 默认 summary prompt 较简单 | ❌ **gap:prompt 质量杠杆未用**(报告实证) |
| 压缩后重试循环 | `maybeCompactContextBeforeLLM` 重建后继续 | ✅ 有重建,但**无"重新评估阈值"闭环** |
| PTL 重试 | 无 | ❌ gap(优先级低) |
| post-compact 附件恢复 | `session_load` 工具(按需,模型主动调) | ⚠️ 有按需恢复,**无自动重注入** |
| 可恢复(tool result) | 占位符带 event_id → session_load | ✅ 已坐实闭环(T1 已验证) |
| 可恢复(消息轮次) | tailoring 硬删 | ❌ **gap:不可恢复** |

---

## 5. 给 T1 的启示:gap 优先级

按"价值×可行性"排序,T1 值得做的 gap:

### 高价值、中改动:摘要 prompt 升级(对应 §3.3)
- 报告实证这是**最大质量杠杆**(九段式让纯 summary ROUGE-L 0.0473→0.2965)。
- 改动小:`session/summary` 加一个 detailed prompt option(类似 benchmark 用的九段式),非框架核心逻辑改动。
- 风险低:默认行为不变,只是提供更好的 prompt 选项。

### 高价值、低改动:熔断器(对应 §3.2)
- 跟刚修的 [T7](TODO.md)(空响应死循环)同属健壮性。
- 改动小:`maybeCompactContextBeforeLLM` 路径加连续失败计数,超限跳过。
- 有 BQ 数据支撑价值(250K API 调用/天)。

### 高价值、高改动:可恢复裁剪(对应 §4 "可恢复消息轮次" gap)
- tailoring 硬删 → 改成可恢复(留占位符指向 session)。
- 最契合 T1 "可恢复压缩" 主题,但要改 `model/token_tailor.go` 核心策略,需评估对 MiddleOut/HeadOut/TailOut 的影响和回归。
- 改动面大,建议作为 T1 的后续阶段。

### 中价值、中改动:多档阈值(对应 §3.1)
- 单档 0.7 → 补 warning/error/blocking。
- 提升可观测性和缓冲,但不是功能性的必须。

### 低优先:PTL 重试、post-compact 自动附件重注入、缓存感知分流
- PTL 重试:trpc-agent-go 不直接发 Anthropic API,PTL 错误处理在 provider 层,框架层难统一介入。
- 自动附件重注入:框架已有 session_load 按需恢复,自动重注入偏 CLI 产品特性。
- 缓存感知分流:依赖 Anthropic 的 cache_edits API,trpc-agent-go 作为多 provider 框架难通用化。

---

## 6. 关键文件索引(Claude Code 侧)

| 文件 | 作用 |
|------|------|
| `src/services/compact/autoCompact.ts` | 四级阈值 + 熔断器 + 触发决策 |
| `src/services/compact/prompt.ts` | 摘要 prompt 两段式 + 九段结构 |
| `src/services/compact/microCompact.ts` | L3 微压缩(缓存感知分流) |
| `src/services/compact/sessionMemoryCompact.ts` | L4 会话内存压缩 |
| `src/services/compact/compact.ts` | L5 全量 LLM 摘要主逻辑 + PTL 重试 + post-compact 附件 |
| `src/services/compact/grouping.ts` | API-round 分组(PTL 重试基础) |
| `src/services/compact/postCompactCleanup.ts` | 压缩后清理(有意不清 skill) |
| `src/query.ts:365-545` | 主循环编排 |
