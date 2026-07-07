# tRPC-Agent-Go 优化 TODO

> 本文件跟踪 **trpc-agent-go 框架的待优化项**(对照 Claude Code 等成熟 Agent 产品取经而来)。
> 工程笔记见 [README.md](README.md);本文件偏"待办 backlog",随挖掘持续追加。
>
> 状态图例:`[ ]` 待开始 · `[~]` 进行中 · `[x]` 已完成 · `[?]` 待决策/调研

## 优先级与依赖

| ID | 主题 | 优先级 | 状态 | 依赖 |
|----|------|--------|------|------|
| T1 | 上下文压缩(Compact)升级:对标 Claude Code 分级流水线 | 高 | `[ ]` | — |
| T2 | CodeBuddy Provider 可信度评估 / 备选后端 | 中 | `[?]` | 见 [codebuddy-gateway.md](codebuddy-gateway.md) §4b;(2026-07-07 额度一度耗尽 `14019`,已换 key 恢复;推荐高性价比模型见 CLAUDE.md) |
| T3 | 跨多次 LLM 调用的 token usage 累加 helper | 中 | `[ ]` | — |
| T4 | 流式 usage 累加对非标准网关不鲁棒(可修复 bug) | 高 | `[x]` | 见 [codebuddy-gateway.md](codebuddy-gateway.md) §4b-2 |
| T5 | benchmark 子模块 go.mod 钉死外部旧版,验证本地改动会误测过时代码 | 中 | `[ ]` | — |
| T6 | 框架多处直接构造 `model.Request` 不设 Stream(已逐条复核:非静默失效,会显式报错;修法应在 provider 层兜底) | 中 | `[~]` | 见 T2 决策 |
| T7 | llmflow 主循环:空 completion 被判非 final → 死循环(真 bug,已修复);benchmark hang 实测证伪 | 高 | `[x]` | 由 T6 定位挖出 |
| T8 | summary_ondemand 实跑取经 + 详细 prompt 已落地验证(`WithDetailedContinuityPrompt`,commit b1497d09) | 中 | `[~]` | 关联 T1;详 pgmt 验证见 T8 正文 |
| _(后续挖掘持续追加)_ | | | | |

---

## 进展快照(2026-07-06):几条旧结论已被实测更新

早期(2026-07-03 前后)对 benchmark 暴露的框架问题有一批结论,这几轮实测后有**重要更新**,列在最前面以免拿着旧判断行事:

- **"工具循环在 CodeBuddy 上静默卡死,根因不确定,疑似大工具结果触发"** → **已证伪 + 已修复**(见 [T7](#t7--llmflow-主循环空-completion-被判非-final--死循环真-bug已修复-))。用真实网关实测:CodeBuddy 拒绝非流式请求返回干净 HTTP 400,框架能正常报错退出;无论工具结果大小、开不开流式,都复现不出卡死。原"卡死"很可能是错误被异步/上层静默吞没后的空等被误记。排查中顺带发现并修复了一个真实的独立 bug(空 completion 死循环,与后端无关)。
- **"内部模块拼请求不开流式 → 对 stream-only 网关静默失效"** → **定性已修正为"非静默,会显式报错"**(见 [T6](#t6--框架多处直接构造-modelrequest-不设-stream已逐条复核-))。逐条复核 13 处构造点:发 LLM 的都会显式报错,误报的几处根本不发 LLM。修法方向不变(provider 层统一兜底),但它不是"隐蔽 bug"。
- **"WithGenerationConfig 用零值覆盖了 Stream:true 默认,是隐蔽缺陷"** → **查证为非 bug,不修**(见 [T7](#t7--llmflow-主循环空-completion-被判非-final--死循环真-bug已修复-) 剩余可选项)。iWiki 官方文档已把"默认非流式、要流式显式 `agent.WithStream(true)`"写成设计约定。
- **"benchmark 钉死外部旧版,本地改动会误测过时代码"** → **结论成立,已充分记录**(见 [T5](#t5--benchmark-子模块-gomod-钉死外部旧版验证本地改动会误测过时代码-));同份数据 token 从 ~186k 回落到 ~18k 就是切回本地工作树后的真值。
- **T1 可恢复压缩闭环** → **已端到端验证闭合**(见 T1"已坐实的基础")。压缩占位符带 event_id、`session_load` 能靠它取回原始内容,两端接缝对齐。
- **T1 摘要 prompt 升级(详细连续性)** → **已落地为 `WithDetailedContinuityPrompt`**(commit b1497d09)+ **实测验证**。设计被验证正确(claude-sonnet-4.6 下详细摘要字符数达基线 74960 量级);但澄清了适用边界——"详细必然提升 ROUGE-L"不成立,只对弱模型/默认摘要弱/on-demand 检索场景有效,强模型+纯摘要直接回答反而被冗长拖累(见 T8 验证结论)。
- **CodeBuddy 网关 `ck_` key 额度**(2026-07-07):实测验证 benchmark 时一度耗尽(`code 14019`,claude 与 glm 均不可用);**已换 key 恢复**,glm-5.2/minimax-m3/kimi-k2.7/deepseek-v4-pro 实测均通。换 key 有个坑:当前 shell 的 `CODEBUDDY_API_KEY` 若残留旧 key,cb-chat/provider 不会重读 `~/.codebuddy.env`(见 CLAUDE.md "换 key 后的坑")。后续跑 benchmark **只用高性价比模型**(glm-5.2 等,见 CLAUDE.md),避免 claude 系虚高且易耗尽额度。

**下一步聚焦**:T1 剩余 gap 里挑一个推进——"压缩熔断器"(纯框架代码 + mock 测试,无需 LLM 调用,跟 [T7](#t7--llmflow-主循环空-completion-被判非-final--死循环真-bug已修复-) 同属健壮性)或"可恢复裁剪"(`token_tailor` 硬删→可恢复,改动面较大)。额度已恢复,需要时可继续 benchmark 类验证,但优先选不依赖网关的方向。

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

### 已坐实的基础:可恢复压缩闭环是闭合的(2026-07-06)

阶段 2 的"轻量档"(tool result 占位符)依赖一个前提——占位符要可恢复,否则压掉的信息就真丢了。**这个前提本轮已实测坐实**:

- **占位符已带恢复锚点**:`context_compact.go` 的 `toolResultRecoveryRef` 已带 `EventID` / `ToolCallID`,压缩后占位符文本会写入 `event_id: <id>` 并提示"用 `session_load` 配合 `event_id` + `content_offset`/`content_limit` 取回"。(原以为"占位符要加 event_id"是待办,实际框架已实现,认知已纠正。)
- **恢复端已就位**:`internal/session/tool/recall/load.go` 的 `session_load` 工具接受 `event_id`(或 `tool_call_id` 后备),经 `GetEventWindow` 从 session 取回原始事件窗口,支持大结果切片。
- **闭环已端到端验证**:新增测试 `TestContextCompaction_RecoveryRoundTrip_ViaSessionEventWindow`(`internal/flow/processor/context_compact_test.go`)——用**真实 inmemory session service**:存入大 tool result → 压缩成带 `event_id` 的占位符(确认原文已从视图消失)→ 用该 `event_id` 调 `GetEventWindow` 取回**压缩前的原始完整内容**。证明两端接缝(compact 写的 `evt.ID` == session 窗口查的 `AnchorEventID`)对齐。此前 compact 侧和 recall 侧各有单测,但缺跨模块闭环验证,本轮补上。

**意义**:分级压缩的轻量档有了可靠地基——压缩不是有损丢弃,而是"移出活动上下文、按需可拉回"。后续做阶段 2 时可放心在此之上叠加中等/重档。

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

## T6 — 框架多处直接构造 `model.Request` 不设 Stream(已逐条复核) `[~]`

### 问题

框架内部多个地方直接 `req := &model.Request{Messages, Tools}` 构造请求,**不设 `GenerationConfig.Stream`**(默认 `false`)。对接**只支持流式**的网关(如 CodeBuddy:非流式请求直接返回 `11101 Non-stream chat request is currently not supported`)时,这些请求**全部失败**;而且很多失败路径是**静默**的,导致功能"看起来在跑、实际全废"。

这是与 T4(流式 usage 累加)同源的一类问题:**框架对 stream-only 后端整体不鲁棒**。已确认多处:
- `model/codebuddy` provider —— 已在 provider 层强制 `request.Stream = true` 兜住(见 [codebuddy-gateway.md](codebuddy-gateway.md))。
- benchmark 的多处 `Stream: false`(scenarios / metrics)—— 本地实验改过,非框架本体。
- **`memory/extractor/memory.go:128`** —— 框架核心,**无任何兜底**;auto 记忆抽取实测因此完全失效(见下)。
- **`session/summary/summarizer.go:866` 与 `:875`(`newSummaryRequest`)** —— 框架核心,summarizer 显式 `Stream: false, // Non-streaming for summarization.`。summary benchmark(MT-Bench-101)实测:两处改成 true 后 summary 模式才能在 CodeBuddy 上生成(否则非流式被网关拒)。**说明这不是 extractor 个例,而是系统性问题**——凡框架内部自建 `model.Request` 的路径都可能中招。
- **`llmagent` 的 tool 调用循环** —— knowledge benchmark(2026-07-03)曾观察到:带 search tool 的 RAG agent 在 CodeBuddy 上疑似"静默卡死"。**这条观察于 2026-07-06 用真实网关实测证伪**(拆出为 [T7](#t7--llmflow-主循环空-completion-被判非-final--死循环真-bug已修复-)):CodeBuddy 拒绝非流式请求时返回的是干净的 HTTP 400,框架能正常收到 error 并退出,不会 hang;无论开不开流式都复现不出 tool 循环卡死。原观察很可能是"非流式请求被网关 400 拒、而该错误被上层静默吞没(如 `waitForAutoExtraction`)、Python 侧 RAGAS 编排空等"被误记成了 tool 循环 hang。真正卡住的是错误被吞没后的等待,不是 tool 循环本身。**不过 T7 顺带发现并修复了一个真实的独立 bug**:主循环遇到"空 completion"会死循环(与后端无关,mock 已坐实并修复)。

### 逐条复核:框架内所有直接构造 `model.Request` 的点(2026-07-06 全仓库扫描 + 逐条读码核实)

全仓库扫描(排除 benchmark / examples / test)得到 13 处框架核心的 `model.Request{...}` 构造点。逐条核实"是否发给 LLM、Stream 设成什么、错误怎么消费、是否核心功能",结论如下:

| 点 | 是否发 LLM | Stream | stream-only 网关下的后果 |
|----|-----------|--------|------------------------|
| `internal/flow/llmflow/llmflow.go:518` | ❌ 否,只装 Tools 传给 response processor | 不适用 | 不受影响 |
| `internal/flow/llmflow/llmflow.go:687` | ✅ 是(主请求) | 由 `BasicRequestProcessor` 在 preprocess 阶段填充 | 遵循 `RunOptions.Stream` / GenerationConfig,不是裸构造 |
| `internal/flow/processor/functioncall.go:296` `:384` | ❌ 否,只装 Tools | 不适用 | 不受影响 |
| `agent/graphagent/graph_agent.go:428` | ❌ 否,只传给 content processor 注入消息 | 不适用 | 不受影响 |
| `graph/state_graph.go:1597` | ✅ 是(graph LLM 节点主路径) | **默认 `Stream: true`**(`:656`/`:1219`),可被 RunOptions 覆盖 | 默认安全;仅当显式覆盖成 false 才会被拒(显式报错) |
| `memory/extractor/memory.go:128` | ✅ 是 | 零值 false | **报错**(非静默,见下) |
| `session/summary/summarizer.go:872` | ✅ 是 | 显式 false | 报错(错误显式传播) |
| `evolution/reviewer.go:137` | ✅ 是(异步复盘,边缘) | 零值 false | 报错(错误显式传播) |
| `knowledge/query/llm.go:78` | ✅ 是(RAG 查询改写) | 零值 false | 报错(错误显式传播) |
| `plugin/toolsearch/knowledge_searcher.go:74` | ✅ 是(工具检索查询改写) | 显式 false | 报错(错误显式传播) |
| `plugin/toolsearch/llm_search.go:69` | ✅ 是(LLM 工具选择) | 显式 false | 报错(错误显式传播) |
| `evaluation/evaluator/llm/internal/judger/judger.go:48` | ✅ 是(LLM 评判) | 显式 false | 报错(错误显式传播) |

**关键更正**:原 T6 定性的"多处**静默失效**"不准确。逐条核实后,**没有一处是"框架吞掉错误"**。发给 LLM 的那些点在 stream-only 网关下都会**显式报错**(网关回干净 400 → provider emit 带 Error 的响应 → 错误 `return` 向上传播)。误报的几处根本不发 LLM(只是装 Tools 的容器)。

**唯一接近"静默"的是 memory 自动抽取,但根因不是 Stream 缺失**:`memory/extractor/memory.go` 的 `Extract` 会把错误 `return`(`:142`/`:167`),上层 `memory/internal/memory/auto.go:413` 也 `return fmt.Errorf(...)` + `log.WarnfContext`。真正"看不见"是因为自动抽取跑在**后台异步 goroutine**里,错误 return 到 goroutine 顶层无人接,只剩一条 WARN 日志;benchmark 侧 `waitForAutoExtraction` 又只轮询记忆条数、等不到就超时返回 nil。这是**"异步任务错误可见性"问题**,与"该不该设 Stream"是两个独立问题。

### 实测证据(2026-07-03,memory benchmark auto 场景)

auto 场景(自动记忆抽取 + memory_search),inmemory 后端,claude-sonnet-4.6:

| | 修复前(Stream 默认 false) | 修复后(extractor 加 Stream:true) |
|---|---:|---:|
| Overall F1 | **0.000** | **0.746** |
| QA1(日期题) | no results | F1=1.0(精确答出 "7 May 2023") |
| memory_search | 全部 `count:0` | 全部命中 |
| 耗时 | 328s(轮询超时空等) | 75s |

一行改动(extractor 请求 `GenerationConfig{Stream:true}`)让 auto 从**完全失效**变成 **F1=0.746**(远超 long_context 基线 0.15)。抽取质量很高——把对话规范化成 `"Attended an LGBTQ support group on 2023-05-07..."` 这类带标准化日期的结构化事实。

### 更严重的次生问题:异步失败不可见(非"框架吞错误")

需要澄清:**框架本体并没有吞掉这个错误**(见上"逐条复核")——extractor 和 auto worker 都把错误 `return` 了。之所以表现成"静默失效",是因为自动记忆抽取跑在**后台异步 goroutine**里,错误 return 到顶层无人接,只剩一条 WARN 日志;而 benchmark 侧 `benchmark/.../auto.go` 的 `waitForAutoExtraction` 只轮询 memory 条数,一直是 0、`sawAnyMemories` 永远 false,最后**超时返回 nil(不报错)**。两头一叠加,抽取全挂但 benchmark 若无其事跑完、只是 F1=0。**比 token 虚高更隐蔽**——不主动 dump 记忆根本发现不了。这是"异步任务错误可见性"的通病,修法见下方建议问题二。

### 建议(2026-07-06 复核后修订)

复核厘清了两个**独立**的问题,不要混为一谈:

**问题一:Stream 缺省与 stream-only 网关的兼容性。** 上面 8 处发 LLM 的点都是非流式(零值或显式 false),对接 stream-only 网关会被拒。但这**不是隐蔽 bug**——它们都会显式报错。而且据 T7 查证,"默认非流式"是 iWiki 官方已文档化的约定,不该在框架层擅自翻转默认。

- [ ] 若决定长期支持 stream-only 后端(关联 T2 决策),正解是**在 provider 层做统一兜底**(如 `model/codebuddy` 已强制 `Stream:true`),或提供"provider 声明 stream-only → 框架自动强制流式"的通用机制。**不要**逐个调用点去改 Stream,那样零散又易漏。
- [ ] 在此之前,这些点的报错信息可以更友好:识别出"stream-only 网关拒非流式"这类错误时,提示"该后端仅支持流式"而非透传原始 400/11101。

**问题二(更值得修):异步任务的错误可见性。** memory 自动抽取的失败之所以像"静默",是因为它在后台 goroutine 里跑,错误只落到一条 WARN 日志、无人接。这跟 Stream 无关,是一类通病。

- [ ] 评估给异步任务(自动记忆抽取、evolution 复盘等)加**可观测的失败上报**:计数器 / 事件 / 回调,让"后台任务连续失败"能被上层感知,而不是只在日志里。这比逐个设 Stream 更有普适价值。

### 关键文件

- `memory/extractor/memory.go`(`~L128` 构造请求处)
- `session/summary/summarizer.go`(`newSummaryRequest` `~L866/875`,显式 Stream:false)
- `model/codebuddy/codebuddy.go`(已有的 provider 层强制 stream 兜底,可作参考范式)
- `benchmark/memory/trpc-agent-go-impl/evaluation/scenarios/auto.go`(`waitForAutoExtraction` 静默超时)

---

## T7 — llmflow 主循环:空 completion 被判非 final → 死循环(真 bug,已修复) `[x]`

> 2026-07-06 由 T6 定位深挖而来。**已修复**(commit c7aac6a1)。这是一个与具体后端/provider 无关的框架健壮性缺陷:任何 provider 吐出"标记 done 但无 content、无 error、无 tool call"的响应,都会让 llmagent 主循环无限重试。**注意**:它最初是顺着 benchmark 的"tool 循环 hang"线索挖出来的,但用真实网关实测后**证伪了"它是 benchmark hang 原因"这条因果链**(CodeBuddy 回干净 400,框架能正常报错退出)——空 completion 死循环是真 bug,只是不由 CodeBuddy 触发。详见下方"这个 bug 是怎么被发现的"。

### 问题

`llmagent` 的 tool 调用主循环(`internal/flow/llmflow/llmflow.go:205-284` 的 `for {}`)**没有任何迭代/步数上限**。它的退出条件只有四个:

1. `runOneStep` 返回 error → 发 error event 退出;
2. `invocation.EndInvocation` 被置位;
3. `lastEvent == nil`(该步无任何 event);
4. `lastEvent.IsFinalResponse()`。

问题出在:**一个 `Done:true` 但既无 `Choices` 也无 `Error` 的"空 completion",`IsFinalResponse()` 返回 `false`**(`model/response.go:369`:`return rsp.Done && (len(rsp.Choices) > 0 || rsp.Error != nil)`),而它又不是 nil、不是 error、不置 EndInvocation。于是四个退出条件**一个都不满足** → 主循环带着**内容完全没变**的请求再跑一轮 → 又得到同样的空 completion → **无限循环,进程永不退出**。

对比:`cycleagent`(`WithMaxIterations`)、`graphagent`(`WithMaxSteps`)都有迭代上限保护;**唯独最常用的 llmagent tool 循环裸奔无保护**。

### 这个 bug 是怎么被发现的,以及它和 benchmark hang 的真实关系(2026-07-06 实测厘清)

这个 bug 最初是顺着 knowledge RAG benchmark 的"tool 循环静默卡死"这条线索挖出来的。挖的过程中先形成了一个推断,后来用真实的 CodeBuddy 网关实测,把这个推断**证伪**了。因为这段经历本身是"比较性/因果性结论必须先核实"的又一个教训,所以完整记下来。

**最初的推断(现已证伪)**是这样一条因果链:benchmark 的 RAG agent 用 `llmagent.WithGenerationConfig({Temperature:0})`,`Stream` 是零值 `false` → `buildRequestProcessorsWithAgent`(`agent/llmagent/llm_agent.go:289`)把这个 config 传进 basic processor,`BasicRequestProcessor.ProcessRequest`(`internal/flow/processor/basic.go:81`)**整体赋值** `req.GenerationConfig = p.GenerationConfig`(不 merge),于是默认的 `Stream:true` 被覆盖成 `false` → 非流式请求被 stream-only 的 CodeBuddy 网关拒 → 产出一个空 completion → 主循环无限重试 → hang。前半段(Stream 被覆盖成 false)是真的,后半段(被拒后产出空 completion 导致 hang)是猜的。

**实测证伪(用真实网关 `ck_` key)**:
1. 裸 `curl` 打非流式请求,网关返回的是 **HTTP 400** + `{"code":11101,"msg":"Non-stream chat request is currently not supported","requestId":"..."}`。是明确的 400,不是"HTTP 200 装成功、body 里藏错误"。
2. 用框架的 `openai` provider 直接发非流式请求,因为是 400,SDK 直接当错误抛出,框架 emit 出来的是一个**带 `Error` 的响应**(`type="api_error"`,消息含 `400 Bad Request`),`IsFinalResponse()==true`、`IsEmptyTerminalResponse()==false`。
3. 完整搭一个和 benchmark 一样的带 tool 的 RAG agent 打真实网关:不开流式时,第一次请求就被 400 拒、框架 **691ms 内正常报错退出**,根本走不到 tool 循环;用 `agent.WithStream(true)` 强制流式时,**tool 循环完整跑通**(9.6s,18 个事件:LLM 返回 tool call → 执行 search 拿到 8200 字节大结果 → 续请求也流式成功 → 正常收尾),也不 hang。

**结论**:无论开不开流式,用现在这份框架代码,都**复现不出**"tool 循环静默卡死"。CodeBuddy 网关给的是干净的 400,框架能正常报错终止。所以"空 completion 死循环"**不是** benchmark 那次 hang 的原因。那条 2026-07-03 的 hang 记录很可能是**误判**:当时 benchmark 非流式,第一次请求就被网关拒,而这个错误被上层某个环节静默吞没(笔记里本就记过 extractor 那类错误会被 `waitForAutoExtraction` 吞掉),Python 侧的 RAGAS 编排在等一个永不到来的结果,表现为"进程不退",被误记成了"tool 循环 hang"。真正卡住的不是 tool 循环,是错误被吞没之后的等待。

**但空 completion 死循环本身是一个真实的、独立的框架 bug** —— 它只是不由 CodeBuddy 触发。任何 provider(或未来别的 stream-only 网关,如果它返回的是"HTTP 200 + 不可识别的空 body"而非干净的 400)只要吐出一个"标记 done 但无 content、无 error、无 tool call"的响应,主循环就会无限重试。下面的 mock 复现坐实了这一点,与任何具体后端无关。

> 框架里其实**已有一段针对性防御**:`model/openai/openai.go:2512-2519` 的 `extractEmbeddedErrorResponse`,注释直白写着要防止 "empty completion that can cause **silent infinite loops** in the agent flow" —— 说明**有人已经踩过这个坑**。但它只兜"HTTP 200 + 可识别 error body"这一种;识别不了的返回格式(空 body / 非标准 error)照样漏成空 completion。这佐证了空 completion 死循环是真实风险,只是 CodeBuddy 恰好不走这条路(它回干净 400)。**根治应在主循环兜底,不能只靠逐一识别各家网关的错误格式。**

### 复现证据(mock,不依赖任何网关 / key)

用一个每次都返回空 completion(`{Done:true}`,无 Choices 无 Error)的 mock model 跑 llmflow 主循环 300ms:

```
model was called 19094 times in 300ms
```

**19094 次调用 = 铁证的无限循环**(正确行为应只调 1 次即终止)。这个断言已固化为回归测试(见下方"已完成修复")。

### 已完成修复(2026-07-06,commit c7aac6a1)

采用了"空 completion 判为终止 + 报错"的方案,而不是"给 MaxLLMCalls 一个默认兜底值"。因为查 iWiki 官方 Agent 文档(`4015773536`)确认,框架已有 `llmagent.WithMaxLLMCalls(n)` / `WithMaxToolIterations(n)` 两道保护,但它们**默认不限制**是官方有意的设计(把决定权交给用户),所以不该擅自改默认值。而"零进展的空转"和"合法但过长的循环"是两码事,前者是纯 bug,该单独兜底。

- 新增 `model.Response.IsEmptyTerminalResponse()`(`model/response.go`):精确识别"标记 done 但无 content、无 error、无 tool call、非 partial"的空响应。判断条件排除了流式中间片段、正常 tool call、带 content/reasoning/tool result 的响应,不会误伤合法循环。
- llmflow 主循环(`internal/flow/llmflow/llmflow.go:280` 之后)在退出判定处加拦截:检测到空响应就 emit 一个明确的 `flow_error` 事件后退出,不再拿相同请求反复重试。这一层兜底**与后端无关**,能挡住所有 provider 的此类空转。
- 回归测试:`model/response_test.go`(12 个 case 覆盖 `IsEmptyTerminalResponse` 语义)+ `internal/flow/llmflow/llmflow_test.go`(`TestRun_EmptyTerminalResponseTerminatesWithFlowError`,验证终止 + emit flow_error)。修复前 mock 复现 19094 次调用,修复后降到 1 次。

### 剩余可选项(未做)

- [ ] **源头防御(治标,低优先)**:`openai` provider 的非流式路径遇到"HTTP 200 + 不可识别空 body"时可直接 emit error。但主循环那层已治本兜住,这只是锦上添花,且 CodeBuddy 实测回的是干净 400、不走这条路,暂无实际触发场景。
- [x] **~~同源修:`WithGenerationConfig` 覆盖 Stream~~(2026-07-06 查证:非 bug,不修)**。原以为"basic processor 默认 `Stream:true` 被零值覆盖成 false"是隐蔽缺陷,准备改成 merge 语义保留 true 默认。但查 iWiki 官方 Agent 文档(`4015773536`)发现,**"默认非流式、要流式请显式 `agent.WithStream(true)` 或在 `GenerationConfig` 里带 `Stream:true`"是已文档化的设计约定**(文档原文:"如果没有显式传入 `llmagent.WithGenerationConfig(...)`,LLMAgent 默认会透传零值 `model.GenerationConfig{}`,因此默认是非流式";并举例 OpenClaw 会自行开流式)。改成"保留 true 默认"会把默认行为从非流式翻转成流式,**与官方约定冲突**,按"以 iWiki 为准"原则不改。`basic processor` 里那个 `Stream:true` 内部初值会被正常覆盖,无对外效果。`GenerationConfigPatch`(`model/request.go`)是框架已有的"部分覆盖"类型,但目前未被 llmagent/runner 接线;单次请求覆盖 Stream 的正道是 `agent.WithStream(true)`(即 `RunOptions.Stream`,`*bool`)。**benchmark RAG agent 被 400 拒的正解是"对 stream-only 网关显式开流式",不是改框架默认。**

### 关键文件

- `internal/flow/llmflow/llmflow.go`(`205-284` 主循环 `for {}`,无迭代上限)
- `model/response.go`(`~L359` `IsFinalResponse`:空 completion 判非 final)
- `internal/flow/processor/basic.go`(`~L81` `req.GenerationConfig = p.GenerationConfig` 整体覆盖)
- `agent/llmagent/llm_agent.go`(`~L289` 把 GenerationConfig 传进 basic processor)
- `model/openai/openai.go`(`~L2512` `extractEmbeddedErrorResponse` 已有的部分防御,注释点明 "silent infinite loops")
- `agent/cycleagent/cycle_agent.go` / `agent/graphagent`(已有的 `MaxIterations`/`MaxSteps` 可作参照范式)

---

## T8 — summary_ondemand(摘要 + 按需检索)实跑取经,喂给 T1 `[ ]`

> 用户"下一步"里点名、且和 [T1](#t1--上下文压缩compact升级-) 上下文压缩升级最相关的方向。**好消息:benchmark 已有现成跑分报告**(`benchmark/summary/results/REPORT.zh_CN.md`),第一步是读懂它、把结论提炼成 T1 的设计输入,而不是从零跑。

### summary_ondemand 是什么

摘要模式的进阶版:把历史对话压成摘要(大幅省 token),但保留 `session_search` / `session_load` 工具,让 agent 在摘要不够用时**按需把隐藏细节拉回来**(渐进式披露)。这正是 T1 想要的"压缩不是有损丢弃,而是可恢复"——和 [T1 已坐实的可恢复压缩闭环](#已坐实的基础可恢复压缩闭环是闭合的2026-07-06)是同一套思路,只是从"tool result 占位符"扩展到"整段历史摘要 + 按需检索"。

### 已有报告的关键数据(直接可作 T1 设计依据)

来自 `benchmark/summary/results/REPORT.zh_CN.md`(三数据集:MT-Bench-101 / QMSum ~19K / LongMemEval ~103K):

- **纯 summary 省 token 但丢早期细节**:QMSum 上纯 summary 省 94.78% prompt,但 ROUGE-L 从 0.1930 掉到 0.1516。
- **按需检索能追回大半质量,且仍大幅省 token**:`summary_ondemand` 把 QMSum ROUGE-L 拉回 0.1770(追回 61.5% 的 ROUGE-L 损失、59.9% 的 F1 损失),同时相较 long context **仍省 76.69% prompt**。
- **越长的上下文,按需检索越关键**:LongMemEval(~103K)上默认 summary 极限压缩(省 99.56%)但直接回答很弱,`summary_ondemand` 把 ROUGE-L 从 0.0473 提到 0.2486、仍省 93.90% prompt。
- **一个反直觉的边界**:当摘要本身已保留原始用户事实(九段式 detailed summary)时,on-demand 的增量价值下降(detailed summary_ondemand ROUGE-L 0.2595,略低于 detailed 纯 summary 0.2965)。**启示:摘要质量和按需检索是替代关系,不是纯叠加**——摘要越好,越不需要回捞;摘要越激进,越依赖回捞兜底。
- **短对话(≤2 轮)开摘要有害**:摘要生成开销 > 节省。**启示:T1 的分级触发必须按对话长度/token 量设阈值,短对话不压。**

### 对 T1 的直接输入

- T1 阶段 2 的"重档(全量 LLM 摘要)"应默认配套按需检索(`session_search`/`session_load`),否则超长上下文下质量塌陷。
- 压缩强度要分级:短对话不压;中等上下文摘要 + 按需检索(性价比最高);超长上下文靠激进摘要 + 按需兜底。
- 摘要 prompt 质量本身是杠杆:detailed prompt 能让纯 summary 逼近 long context,减少回捞频率(省 tool 调用往返)。

### detailed prompt 实测验证结论(2026-07-07,已落地为 `WithDetailedContinuityPrompt`)

我们加的 `WithDetailedContinuityPrompt`(commit b1497d09)用 codebuddy provider 在 LongMemEval 上跑了实测验证(两轮:glm-5.0 + claude-sonnet-4.6,benchmark 临时切本地 + codebuddy provider + inmemory 绕开 pgvector,跑完还原)。关键发现:

**1. prompt 设计本身被验证正确(强模型下产出基线量级的 verbatim 摘要)。** claude-sonnet-4.6 下 detailed summary 字符数 53162–80795(平均 66010),达到基线报告 74960 的量级;而上一轮 glm-5.0 只有 6650 字符。证明九段式 prompt + verbatim 用户消息的指令有效,只要模型遵从度够就能产出基线量级详细摘要。上一轮 glm 跑不出提升的根因是模型不遵从(completion 几十 token 就停),不是 prompt 有问题。

**2. 但"detailed 必然带来 ROUGE-L 提升"这个结论不成立,需澄清适用边界。** 在 claude-sonnet-4.6 这种强模型上,detailed 的 ROUGE-L 反而比 default 略低(同 3 case 0.267→0.184)。原因:基线报告那种 0.0473→0.2965 的大幅提升发生在"default 摘要太弱、模型抓不住关键事实"的弱模型场景;强模型即使 default 短摘要也能抓住关键事实(3 case 有 2 个 EM 命中,default ROUGE-L 已 0.267 远高于基线 0.0473),提升空间被压缩;且 detailed summary 太长(6 万字符)让回答 agent 倾向冗长回答,ROUGE-L 对长回答不友好,边际质量收益被惩罚抵消。

**3. detailed prompt 真正的价值在 on-demand 检索场景(上一轮 glm 数据)。** detailed 的 ondemand 比 default 的 ondemand ROUGE-L 高 3 倍(0.1444 vs 0.0465)、Exact Match 0.60 vs 0.00。因为 detailed summary 大且信息密集,检索才有东西可搜;default summary 太短,检索也无米之炊。**这才是 detailed prompt 的甜蜜点**。

**对 T1 的修正输入**:detailed prompt 不是"默认就该开"的银弹,而是**有条件的最优**——适合 (a) 弱模型 / default 摘要抓不住关键事实的场景,(b) 需要 verbatim 保留原始用户消息供后续 on-demand 检索的场景。对强模型 + 纯 summary 直接回答的场景,detailed 反而可能因冗长拖累 ROUGE-L。这与我们"默认行为不动、显式开启"的设计取向一致。后续 T1 分级压缩的"重档"若配 detailed,应同时配 on-demand 检索(detailed summary 越大越依赖检索放大价值)。

> 注:实测中 codebuddy 网关 `ck_` key 额度一度耗尽(`code 14019`,claude 与 glm 均不可用),claude-sonnet-4.6 detailed 模式仅成功 3 case。3 case 已足以支撑上述结论(prompt 正确性 + 字符量级对齐基线),但 ROUGE-L 数值为小样本,趋势性参考。(2026-07-07 额度已换 key 恢复,后续验证改用 glm-5.2 等高性价比模型。)

### 待办

- [x] **精读 Claude Code compact 全部源码并提炼精华文档**(2026-07-06 完成):见 [claude-code-compact-design.md](claude-code-compact-design.md)。5 层分级流水线逐层拆解 + 横切设计(四级阈值/熔断器/两段式 prompt/缓存感知分流/PTL 重试/post-compact 附件恢复) + **对照 trpc-agent-go 现状标注 gap** + 给 T1 的启示。
- [x] 精读 `benchmark/summary/results/REPORT{,.zh_CN}.md` 全文(已在精华文档 §4 对照表 + T8 上文"已有报告的关键数据"提炼)。
- [?] (可选)本地实跑 `summary_ondemand` 复现报告:需 pgvector(`-pgvector-dsn`)+ 数据集下载;注意 benchmark go.mod 钉外部版问题(见 [T5](#t5--benchmark-子模块-gomod-钉死外部旧版验证本地改动会误测过时代码-),要先切本地工作树才能测本地改动)。报告已有数据,本地实跑主要用于验证 T1 改动后的效果,非取经必需。

### T1 待办的 gap 优先级(源自精华文档 §5,按"价值×可行性"排序)

- [ ] **高价值/中改动:摘要 prompt 升级**。报告实证是最大质量杠杆(九段式让纯 summary ROUGE-L 0.0473→0.2965)。给 `session/summary` 加 detailed prompt option,默认行为不变,风险低。
- [ ] **高价值/低改动:压缩熔断器**。跟 [T7](#t7--llmflow-主循环空-completion-被判非-final--死循环真-bug已修复-) 同属健壮性;`maybeCompactContextBeforeLLM` 路径加连续失败计数,超限跳过。有 BQ 数据(250K API 调用/天)。
- [ ] **高价值/高改动:可恢复裁剪**。`token_tailor` 硬删 → 可恢复(留占位符指向 session)。最契合 T1 主题,但要改 MiddleOut 等核心策略,需评估回归。建议作 T1 后续阶段。
- [ ] **中价值/中改动:多档阈值**。单档 0.7 → 补 warning/error/blocking,提升可观测性。
- [ ] 低优先:PTL 重试(框架不直接发 Anthropic API,难统一介入)、post-compact 自动附件重注入(已有 session_load 按需,自动重注入偏 CLI 特性)、缓存感知分流(依赖 Anthropic cache_edits,多 provider 难通用化)。

### 关键文件

- [claude-code-compact-design.md](claude-code-compact-design.md)(⭐ Claude Code compact 设计精华 + 对照 trpc-agent-go gap + T1 启示)
- `benchmark/summary/results/REPORT.zh_CN.md`(现成跑分报告 + 结论)
- `benchmark/summary/trpc-agent-go-impl/qmsum.go` / `longmemeval.go`(summary_ondemand 场景实现,`WithAddSessionSummary(true)` + `WithEnableOnDemandSession(true)` 的标准用法)
- `internal/session/tool/recall/`(`session_search` / `session_load` 工具实现)
