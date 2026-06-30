# tRPC-Agent-Go 优化 TODO

> 本文件跟踪 **trpc-agent-go 框架的待优化项**(对照 Claude Code 等成熟 Agent 产品取经而来)。
> 工程笔记见 [README.md](README.md);本文件偏"待办 backlog",随挖掘持续追加。
>
> 状态图例:`[ ]` 待开始 · `[~]` 进行中 · `[x]` 已完成 · `[?]` 待决策/调研

## 优先级与依赖

| ID | 主题 | 优先级 | 状态 | 依赖 |
|----|------|--------|------|------|
| T1 | 上下文压缩(Compact)升级:对标 Claude Code 分级流水线 | 高 | `[ ]` | — |
| T2 | CodeBuddy Provider 可信度评估 / 备选后端 | 中 | `[?]` | 见 [codebuddy-gateway.md](codebuddy-gateway.md) §4b |
| T3 | 跨多次 LLM 调用的 token usage 累加 helper | 中 | `[ ]` | — |
| _(后续挖掘持续追加)_ | | | | |

---

## T1 — 上下文压缩(Compact)升级 ⭐

### 背景与动机

实测对比(2026-06-30)发现 trpc-agent-go 的上下文管理与 Claude Code 存在**明显成熟度差距**。

**先澄清一个非问题**:trpc-agent-go 的上下文**拼接**是正确的。实测每次 LLM 请求是标准"仅追加"结构 `[system][user][asst][tool_result][asst][tool_result]...`,每轮只追加最新的 assistant + tool_result,膨胀可控(每步 ~370 B)。**拼接不需要改。**

**真正的差距在"压缩/裁剪"策略的成熟度。**

### 现状(trpc-agent-go)

两个**独立、较初级**的机制,各自单一策略:

1. **`internal/flow/processor/context_compact.go`**(flow 层,请求组装时)
   - 唯一能力:把**历史超大 tool result** 替换成可恢复占位符。
   - 触发:`WithEnableContextCompaction(true)` + tool result 超 `ToolResultMaxTokens` + 不在 `KeepRecentRequests` 保护窗口。
   - 已有 option:`WithContextCompactionThresholdRatio`(按 context window 比例)、`ToolResultMaxTokens`、`OversizedToolResultMaxTokens`、`KeepRecentRequests`、`TokenCounter`、`WithToolResultCompactionConfig`(按工具名定制)。
   - 局限:**只管 tool result**,不碰对话消息;无摘要,只有"占位符替换"一种手段。

2. **`model/token_tailor.go` 的 `MiddleOutStrategy`**(model 层,发请求前兜底)
   - 唯一能力:按 token 预算**整轮删除中间消息**("lost in the middle" 理论背书,Yi et al. 2025)。
   - 安全底线:最小保护上下文(system + 最近一轮)仍超预算 → 返回 overflow error,不静默丢系统提示。
   - 局限:**硬删除**而非压缩,删了就没了(不可恢复);`TailoringStrategy` 是接口(可插拔)但目前只有 MiddleOut 一个实现。

### 对标:Claude Code 的分级压缩流水线

参考 `claude-code/src/query.ts:365-445`(主循环)与 `claude-code/src/services/compact/autoCompact.ts`。

Claude Code 把上下文管理做成一条**正交、可组合、分级触发**的流水线,每层职责单一:

1. `getMessagesAfterCompactBoundary` — 只取 compact 边界之后的消息视图。
2. `applyToolResultBudget` — **per-message 工具结果预算**,按 `tool_use_id` 替换内容(不看内容本身,与下游 microcompact 正交组合)。
3. `snip`(`HISTORY_SNIP`) — 历史精简,`tokensFreed` 反馈给 autocompact 的阈值判断。
4. `microcompact` — 细粒度压缩,支持 cache 编辑(`CACHED_MICROCOMPACT`)。
5. `contextCollapse`(`CONTEXT_COLLAPSE`) — **可投影的折叠视图**:summary 存在独立 collapse store(不污染原始消息数组),每次进入用 commit log 重放投影,使折叠**跨轮持久**。先于 autocompact 跑,若折叠后已达标则 autocompact 不触发(保留细粒度上下文)。
6. `autoCompact` — **LLM 全量摘要兜底**:为摘要输出预留 token(基于 p99.99 的摘要输出 = 17387 token),分级阈值(warning / error / autocompact 三档),连续失败上限后停止重试。

关键设计理念:
- **分级触发**:能用轻量手段(预算替换/折叠)达标就不上重手段(LLM 摘要)。
- **可恢复 / 不破坏原始**:折叠是读时投影,原始历史不动。
- **LLM 摘要是最后兜底**,不是第一选择。
- **token 反馈闭环**:每层释放的 token 反馈给阈值判断。

### 改造方案(建议分阶段,不要一次推翻)

> 原则:复用现有 `TailoringStrategy` 接口与 compaction processor 框架,**增量加层**,不破坏既有语义。每阶段独立可交付、可测。

**阶段 1 — 补 LLM 摘要兜底(最高价值,最小侵入)**
- [ ] 新增一个 `SummarizeTailoringStrategy`(实现 `model.TailoringStrategy`):当 MiddleOut 删除会丢失关键上下文时,改为调用一次 LLM 把"中段历史"压成摘要消息,而非直接删。
- [ ] 摘要输出预留 token(参考 Claude Code 的预留思路,避免摘要请求本身超窗)。
- [ ] 通过 `openai.WithTailoringStrategy(...)` / `provider.WithTailoringStrategy` 注入,默认仍是 MiddleOut(不改默认行为)。
- 验证:构造超长对话,对比 MiddleOut(删除)vs Summarize(摘要)后,后续回答对早期信息的保留度。

**阶段 2 — compaction 分级触发**
- [ ] 把 `context_compact` 的单一触发(超阈值即压)升级为分级:轻量(tool result 占位符)→ 中等(历史 user/asst 摘要)→ 重(全量 LLM 摘要)。
- [ ] 复用已有的 `WithContextCompactionThresholdRatio`,补 warning / compact 两档阈值。
- [ ] 释放的 token 反馈到下一档阈值判断(闭环)。

**阶段 3 — 折叠视图持久化(较大改动,需评估收益)**
- [?] 评估是否值得引入 Claude Code 式的 "collapse store + commit log 重放":让摘要/折叠跨轮持久,而不是每轮重新计算。
- [?] 这依赖 session 层支持"投影视图",改动面大,**先调研再决定**。

### 注意事项 / 坑

- **不要用 CodeBuddy 网关的 usage 数字驱动压缩决策** —— 网关 token 虚高(见 [codebuddy-gateway.md](codebuddy-gateway.md) §4b)。压缩判断要用本地 `TokenCounter` 估算(框架现状正是如此,保持)。
- 改 `TailoringStrategy` 时注意:它在 model 层、发请求前的最后兜底;`context_compact` 在 flow 层、更早。两者职责不同,**不要合并**,而是让它们分级协作。
- 任何新策略都要守住 MiddleOut 已有的安全底线:**宁可报 overflow error,也不丢系统提示**。

### 关键文件索引

| 文件 | 作用 |
|------|------|
| `internal/flow/processor/context_compact.go` | flow 层 compaction(tool result 占位符) |
| `model/token_tailor.go` | model 层裁剪(`TailoringStrategy` 接口 + `MiddleOutStrategy`) |
| `agent/llmagent/option.go` (`WithContextCompaction*`) | compaction 配置入口 |
| `claude-code/src/query.ts:365-445` | 对标:主循环上下文流水线 |
| `claude-code/src/services/compact/autoCompact.ts` | 对标:LLM 摘要兜底 + 分级阈值 |
| `examples/context_compaction/` | 现有 compaction demo(带 `-debug`) |
| `examples/tailor/` | 现有 tailoring demo(`/bulk N` 灌消息触发) |

---

## T2 — CodeBuddy Provider 可信度评估 `[?]`

- 详见 [codebuddy-gateway.md](codebuddy-gateway.md) §4b:网关带 tools 时 usage 失真(Claude 系有 ~575 token 固定加项;模型族差异大,gemini/gpt/kimi 基本正常)。
- [?] 决策:是否继续采用 CodeBuddy Provider,还是改走更可信的 OpenAI 兼容后端(Venus / 太极等)。
- [ ] 若保留:在文档/代码注释里明确警告"勿用网关 usage 计量"。

---

## T3 — 跨多次 LLM 调用的 token usage 累加 helper `[ ]`

- 现状:一次 `Runner.Run` 内若有工具调用,会发生多次 LLM 调用,**每次各有独立 Usage**;框架不在 Runner 层累加总量。要全程总消耗得消费端自己加(参考 `examples/tokentracker/main.go:296`)。
- [ ] 评估是否提供一个内置 helper / event,聚合单次 Run 的总 token 消耗,免去每个调用方重复实现。
- 注意:同样**不能依赖 CodeBuddy 网关 usage**(虚高)。
