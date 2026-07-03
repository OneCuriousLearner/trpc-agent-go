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
| T4 | 流式 usage 累加对非标准网关不鲁棒(可修复 bug) | 高 | `[x]` | 见 [codebuddy-gateway.md](codebuddy-gateway.md) §4b-2 |
| T5 | benchmark 子模块 go.mod 钉死外部旧版,验证本地改动会误测过时代码 | 中 | `[ ]` | — |
| T6 | 框架多处直接构造 `model.Request` 不设 Stream,对 stream-only 后端静默失效 | 高 | `[ ]` | — |
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

---

## T4 — 流式 usage 累加对非标准网关不鲁棒(可修复 bug)`[x]` 已完成

### 问题(实测坐实,详见 [codebuddy-gateway.md](codebuddy-gateway.md) §4b-2)

OpenAI 流式协议约定 usage 只在**最后一个 chunk**出现一次。`model/openai` 依赖 OpenAI SDK 的 `ChatCompletionAccumulator`,其 `accumulateDelta`(`streamaccumulator.go`)对每个 chunk 无条件 `cc.Usage.PromptTokens += chunk.Usage.PromptTokens`。

**当上游网关违反协议、每个 chunk 都重复携带完整 usage 时**(CodeBuddy 就是如此:20-chunk 流里 19 个都带 `prompt_tokens:562`),SDK 就把同一份 usage 累加了 ~chunk 数次 → prompt_tokens 虚高 ~chunk 数倍,多轮滚雪球到几十万/几百万(benchmark 实测单步报到 670 万)。带 tools 时 chunk 多、几乎每个都重复 usage,失真最重(实测 `with-tools` 6479 vs 真实 ~560)。

### 影响面

- 任何用 `Response.Usage` 做计量/计费/预算的场景,在对接"每 chunk 重复 usage"型网关时会严重失真。
- 这类网关不止 CodeBuddy 一家(内网各种 Anthropic→OpenAI 协议转换层都可能有此行为),所以这是**通用健壮性问题**,不只是 CodeBuddy 专属。

### 修复(已完成,commit `6562f89f`,分支 `fix/stream-usage-take-last`)

采用 **take-last** 语义(思路一;实测坐实流式 usage 是"累计快照"而非"增量",completion 逐 chunk 报 1→…→11,take-last=11 正确、sum=30 错)。放弃"只认 finish chunk"(思路二)——严格 OpenAI 的 usage 在**无 finish_reason** 的独立末尾 chunk 上,按 finish_reason 抓会漏掉。

- [x] `model/openai/openai.go` `accumulateChunk`:chunk 带 usage 时 `acc.Usage = chunk.Usage` 覆盖 SDK 累加值(take-last);同时天然废掉 details-fix 的重复累加(仅 legacy 路径保留)。提炼 `chunkHasUsage` helper。
- [x] 新增 option `WithStreamUsageTakeLast(bool)` 默认 **true**;`false` 回退旧 sum;`WithAccumulateChunkTokenUsage` 自定义钩子仍优先。
- [x] 单测 3 个(`openai_test.go`):重复-usage 流 take-last 得真值(非 ×N)、关开关回退 sum、标准流无回归。全绿 + lint 0 警告。
- [x] 真机验证:CodeBuddy `with-tools` prompt 6479 → **589**(对照裸 curl ~588),开关关回到 6479。

关键文件:`model/openai/openai.go`(`accumulateChunk`)、`model/openai/options.go`(`WithStreamUsageTakeLast`)。

### 与 T2 的关系

修好 T4 后,CodeBuddy 的"流式虚高"部分即可消除;剩下的"非流式固定加项 ~575"是纯网关侧问题(框架无法修)。T4 完成会显著改善 T2 的取舍——框架侧至少做到了鲁棒。


---

## T5 — benchmark 子模块 go.mod 钉死外部旧版,验证本地改动会误测过时代码 `[ ]`

### 问题

`benchmark/*/trpc-agent-go-impl/go.mod` 的 `replace` 段把框架依赖**钉死到一个外部发布的伪版本**,例如:

```
trpc.group/trpc-go/trpc-agent-go => github.com/trpc-group/trpc-agent-go v1.7.1-0.20260402032440-a4e36132659f
```

伪版本里的 `20260402` 是 **2026-04-02** 的快照。这意味着:**任何人在本地改了框架代码后,直接 `cd benchmark/.../trpc-agent-go-impl && go run .` 跑 benchmark,用的都不是自己改的代码,而是 4 月那个外部快照。**

### 真实踩坑记录(2026-07-03)

T4(流式 usage take-last)已于 7-01 修复并合入 `feat/lightai`。但 memory benchmark 的 replace 钉死的是 4-02 快照(**不含**该修复)。结果:

- 直接跑 → long_context QA[0] prompt 报 **186710**(旧版 SDK-sum bug 复现,虚高 10×)
- 把 replace 改成 `=> ../../..`(本地工作树)重跑 → 同一份数据同一 prompt,回落到 **18671**(真值),与本地 tokenCounter 估算 16474 一致

**教训**:验证"本地框架改动对 benchmark 的影响"前,必须先确认 benchmark 实际链接的是本地代码而非钉死的外部版本;否则会误测过时代码,甚至据此得出错误结论(本次差点把已修复的 bug 当新现象重新分析)。

### 建议

- [ ] 提供一个"链接本地工作树"的构建方式:比如 benchmark 目录放一个 `go.work`(workspace 模式,自动覆盖 replace),或提供 `-local` 脚本/Makefile target 临时把 replace 切到 `../../..`。
- [ ] 在各 benchmark 的 README 里显式说明:"默认跑外部发布版;要验证本地改动,请用 go.work / 改 replace 到本地工作树。"
- [ ] 评估 CI 是否也踩这个坑(benchmark 是否对本地改动做回归——大概率没有,因为钉死了外部版)。

### 操作备忘(本次用过,可复用)

把 `benchmark/memory/trpc-agent-go-impl/go.mod` 的 replace 全段改成本地相对路径:
```
trpc.group/trpc-go/trpc-agent-go               => ../../..
trpc.group/trpc-go/trpc-agent-go/memory/mysql  => ../../../memory/mysql
...(其余子模块同理指向 ../../../<子模块>)
```
然后 `go mod tidy` 对齐,再 `CGO_ENABLED=1 go build`(sqlite-vec 需系统 `sqlite-devel`)。

### 各 benchmark 的 replace 现状(2026-07-03 核对)

不同 benchmark 钉的外部目标还不一样,踩法各异:

- **memory**:`=> github.com/trpc-group/trpc-agent-go v1.7.1-...20260402`(官方组织,4-02 快照)。切本地即可,子模块多(memory/mysql、pgvector、sqlite、sqlitevec、session/pgvector、storage/postgres)。
- **summary**:`=> github.com/Rememorio/trpc-agent-go v0.0.0-...20260526`(**个人 fork**,5-26 快照)。切本地时会遇到 `undefined: sessionsummary.WithDetailedContinuityPrompt` —— 该便捷 option 只存在于这个个人 fork(为跑 benchmark 定制:九段式 continuity prompt + verbatim 用户消息附录),官方主线没有。

**重要澄清(避免误判方向)**:曾一度误以为"外部 fork 领先本地"。核实后**不成立**——本地 `session/summary` 最新提交 `812e273a`(2026-06-10,cache-safe forking #1932)比 summary benchmark 钉的 fork 快照(5-26)**还新**,本地官方主线整体更新。`WithDetailedContinuityPrompt` 只是个人 fork 里的一个 benchmark 专用**便捷封装**;本地用更底层通用的 `WithPrompt`/`WithSystemPrompt` 同样能实现自定义 summary prompt,只是没有那个特定函数名。**结论:本地不落后;benchmark 钉个人 fork 才是问题**——这类 benchmark 应指向官方主线/本地,而非个人 fork 的旧快照。

---

## T6 — 框架多处直接构造 `model.Request` 不设 Stream,对 stream-only 后端静默失效 `[ ]`

### 问题

框架内部多个地方直接 `req := &model.Request{Messages, Tools}` 构造请求,**不设 `GenerationConfig.Stream`**(默认 `false`)。对接**只支持流式**的网关(如 CodeBuddy:非流式请求直接返回 `11101 Non-stream chat request is currently not supported`)时,这些请求**全部失败**;而且很多失败路径是**静默**的,导致功能"看起来在跑、实际全废"。

这是与 T4(流式 usage 累加)同源的一类问题:**框架对 stream-only 后端整体不鲁棒**。已确认多处:
- `model/codebuddy` provider —— 已在 provider 层强制 `request.Stream = true` 兜住(见 [codebuddy-gateway.md](codebuddy-gateway.md))。
- benchmark 的多处 `Stream: false`(scenarios / metrics)—— 本地实验改过,非框架本体。
- **`memory/extractor/memory.go:128`** —— 框架核心,**无任何兜底**;auto 记忆抽取实测因此完全失效(见下)。
- **`session/summary/summarizer.go:866` 与 `:875`(`newSummaryRequest`)** —— 框架核心,summarizer 显式 `Stream: false, // Non-streaming for summarization.`。summary benchmark(MT-Bench-101)实测:两处改成 true 后 summary 模式才能在 CodeBuddy 上生成(否则非流式被网关拒)。**说明这不是 extractor 个例,而是系统性问题**——凡框架内部自建 `model.Request` 的路径都可能中招。

### 实测证据(2026-07-03,memory benchmark auto 场景)

auto 场景(自动记忆抽取 + memory_search),inmemory 后端,claude-sonnet-4.6:

| | 修复前(Stream 默认 false) | 修复后(extractor 加 Stream:true) |
|---|---:|---:|
| Overall F1 | **0.000** | **0.746** |
| QA1(日期题) | no results | F1=1.0(精确答出 "7 May 2023") |
| memory_search | 全部 `count:0` | 全部命中 |
| 耗时 | 328s(轮询超时空等) | 75s |

一行改动(extractor 请求 `GenerationConfig{Stream:true}`)让 auto 从**完全失效**变成 **F1=0.746**(远超 long_context 基线 0.15)。抽取质量很高——把对话规范化成 `"Attended an LGBTQ support group on 2023-05-07..."` 这类带标准化日期的结构化事实。

### 更严重的次生问题:错误被静默吞没

extractor 抽取失败后,`benchmark/.../auto.go` 的 `waitForAutoExtraction` 轮询 memory 数一直是 0,`sawAnyMemories` 永远 false,最后**超时返回 nil(不报错)**;上层 `EnqueueAutoMemoryJob` 的错误也没有冒泡到用户可见处。结果:抽取全挂,benchmark 却若无其事跑完,只是 F1=0。**比 token 虚高更隐蔽**——不主动 dump 记忆根本发现不了。

### 建议

- [ ] 排查框架内所有直接构造 `model.Request` 的地方(extractor、planner、summary、evaluation judge 等),评估是否应默认 `Stream: true`,或提供一个统一的"provider 声明 stream-only → 框架自动强制流式"的机制(类似 `model/codebuddy` 已做的,但下沉到通用层)。
- [ ] extractor 抽取失败要有**可见的错误/警告**,不能静默吞没(至少在 auto 记忆一条都没抽出来时给出 warning)。
- [ ] 关联 T2:若决定保留 stream-only 的 CodeBuddy 后端,这类兼容性必须在框架层解决,而非每个调用点各自设 Stream。

### 关键文件

- `memory/extractor/memory.go`(`~L128` 构造请求处)
- `session/summary/summarizer.go`(`newSummaryRequest` `~L866/875`,显式 Stream:false)
- `model/codebuddy/codebuddy.go`(已有的 provider 层强制 stream 兜底,可作参考范式)
- `benchmark/memory/trpc-agent-go-impl/evaluation/scenarios/auto.go`(`waitForAutoExtraction` 静默超时)
