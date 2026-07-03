# tRPC-Agent-Go Benchmark 探索分析报告

> 时间:2026-07-03 · 环境:本地无外部 LLM key,唯一可用后端是 CodeBuddy 网关(chat-only)+ 本地 Ollama(embedding)
> 关联文档:[TODO.md](TODO.md)(优化项 T1–T6)、[codebuddy-gateway.md](codebuddy-gateway.md)(网关能力/踩坑)
> 本报告汇总对 `benchmark/` 子模块的实跑探索:工作流、测试数据、实测结果、踩坑、以及由此发现的框架级问题。

---

## 0. 结论速览(TL;DR)

- **三块 benchmark 都实跑验证了核心链路**:memory(long_context + auto)、summary(MT-Bench-101)、knowledge(建库+检索+简单问答)。
- **最有价值的产出不是分数,而是暴露的框架级问题**——benchmark 是绝佳的"真实压力测试",逼出了 4 类平时隐藏的缺陷:
  1. **T4 流式 usage 累加 bug**(已修复):token 被虚报 ~chunk 数倍。
  2. **T6 stream-only 不鲁棒**(系统性):框架多处自建 `model.Request` 不设 `Stream`,对 stream-only 网关静默失效(extractor / summarizer / llmagent tool 循环)。
  3. **T5 benchmark 依赖钉死外部版**:go.mod replace 指向外部/个人 fork 旧快照,验证本地改动会误测过时代码。
  4. **CodeBuddy 网关能力边界**:只有 chat,无 embedding。
- **一个被推翻的错误结论**(值得记):曾误判"外部 fork 领先本地",核实后本地官方主线更新。教训:比较性结论必须先核实,不能凭单点外推。

---

## 1. benchmark 套件概览

`benchmark/` 是独立 git 子模块,含 6 块:

| 子块 | 语言 | 测什么 | 数据集 |
|------|------|--------|--------|
| **memory** | Go | 长期对话记忆 | LoCoMo(10 样本,1986 QA) |
| **summary** | Go | 会话摘要的 token/质量权衡 | MT-Bench-101 / QMSum / LongMemEval |
| **knowledge** | Python(RAGAS) | RAG 检索质量,6 框架横向对比 | HuggingFace doc / RGB / MultiHop-RAG |
| anthropic_skills | Go | Skills 兼容性与 token | — |
| gaia | — | GAIA benchmark | — |
| toolsearch | Go | Tool Search 评测 | — |

本次探索了前三块(用户关注点)。

### 通用架构模式

- **memory / summary**:纯 Go 程序,`go run .` 直接跑,内部起 Runner/Agent。
- **knowledge**:Python 编排 + Go HTTP 服务。Python(main.py)负责数据/评测(RAGAS),通过 `subprocess` 起一个 Go 服务(`trpc_knowledge`,暴露 `/load` `/search` `/answer`),用 `requests` 调它。这是唯一的跨语言架构。

---

## 2. 各 benchmark 的工作流与实测

### 2.1 memory(LoCoMo)

**三种场景**(对比"记忆方案"):
- `long_context`:整段对话塞进 prompt 直接问答(基线,最准最贵,无需 DB/embedding)。
- `auto`:两阶段——① seed 阶段把 N 个 session 逐个灌入,Runner 自动触发 `EnqueueAutoMemoryJob` → extractor(LLM)把对话抽成**结构化记忆**;② QA 阶段用带 `memory_search` 的 agent 只检索不注入原始历史。
- `agentic`:agent 主动用 memory 工具(add/search)决定存什么。

**后端**:inmemory(关键词,无需 DB)/ sqlite / sqlitevec / pgvector / mysql。**指标**:F1 / BLEU / LLM-as-Judge。

**实测(claude-sonnet-4.6,迷你数据集 1 样本)**:
- `long_context`(3 QA):F1=0.15,验证了"每轮 LLM 请求 prompt 单调增长"(仅追加式上下文,正确)。
- `auto`(inmemory,3 QA):**修复 T6 前 F1=0.000(记忆全空),修复后 F1=0.746**——见 §4.2。抽取质量高,把对话规范化成 `"Attended an LGBTQ support group on 2023-05-07..."` 这类带标准化日期的结构化事实。

### 2.2 summary(MT-Bench-101)

**测什么**:会话摘要能省多少 token、损失多少信息、能否用按需检索恢复。三数据集:
- **MT-Bench-101**:baseline vs summary,多轮对话质量(一致性/token 节省/信息保留)。
- **QMSum**:long_context vs summary vs `summary_ondemand`(摘要 + `session_search`/`session_load` 按需拉回隐藏细节)。需 pgvector。
- **LongMemEval**:~103K token 极端长度多会话。

**实测(MT-Bench-101,CM 任务,2 case)**:
- 逐轮对比最直观(CM_1146):baseline Turn4 prompt=1064,summary Turn4 prompt=654(**省 39%**),整体 prompt 省 19.2%。
- overall 只省 4%,**CM_1145 甚至 -2%(负节省)**——实证 README 的"每 2 轮触发摘要对短对话有害":短对话摘要开销 > 节省。
- 验证了 summarizer 在改 Stream 后能在 CodeBuddy 上工作(见 §4.2)。

### 2.3 knowledge(HuggingFace doc RAG)

**工作流**:Python 起 Go 服务 → `/load`(切 chunk + embedding 建向量库)→ 逐 QA `/answer`(agent 检索+生成)→ RAGAS 7 指标评测(Faithfulness / Answer Relevancy/Correctness/Similarity / Context Precision/Recall/Entity Recall)。

**实测(claude-sonnet-4.6 + 本地 Ollama embedding,限 30 文档)**:
- ✅ 建库:Ollama `nomic-embed-text` 建成 702 chunk。
- ✅ 检索:`/search` "What is a tokenizer?" 精准命中 3 篇相关文档。
- ✅ 简单问答:`/answer`(不带复杂 tool 循环)正确。
- ❌ **完整 RAGAS 分数未跑出**——原因见 §3.4 + §4.2(agent tool 循环卡死)。

**核心结论**(回答"构建 RAG 一定要 key 吗"):**不需要**。RAG 要的是"文本→向量"能力,本地 Ollama 即可,与本地自建 RAG 无本质区别。

---

## 3. 踩坑记录(工程实操)

### 3.1 CGO / 系统依赖

- memory 依赖 `sqlite-vec`,编译需 `sqlite3.h` → `dnf install -y sqlite-devel`(TencentOS)。
- 所有含 sqlite 的构建需 `CGO_ENABLED=1`。

### 3.2 benchmark go.mod 钉死外部版(T5)

三块的 replace 目标各不相同,**默认都不是本地工作树**:
- memory → `github.com/trpc-group/trpc-agent-go v1.7.1-...20260402`(官方组织,4-02 快照)
- summary → `github.com/Rememorio/trpc-agent-go v0.0.0-...20260526`(**个人 fork**,5-26 快照)
- knowledge → 无 replace,直接 `require v1.7.0`

**后果**:本地改了框架代码后直接跑 benchmark,用的是外部旧版而非自己的改动。本次差点因此把已修复的 T4 bug 当新现象重新分析(见 §4.1)。**验证本地改动前必须把 replace 切到 `../../..` 并 `go mod tidy`**。

### 3.3 数据集获取

- LoCoMo:`git clone github.com/snap-research/locomo`(github 可达)。
- MT-Bench-101:sparse clone `mtbench101/mt-bench-101`,数据在 `data/subjective/mtbench101.jsonl`(需摆成 `<path>/subjective/` 结构)。
- HuggingFace doc:`datasets.load_dataset("m-ric/huggingface_doc")`(huggingface.co 可达)。

### 3.4 规模控制(重要)

- 全量数据集在本环境跑不动:HuggingFace 全量 2647 文档被切成 **54349 chunk**,CPU embedding ETA **1.5 小时**。限到 30 文档仍有 702 chunk。
- benchmark **普遍缺"限样本数"的 flag**——只能改代码加 `MAX_QA`/`MAX_DOC` 环境变量截断(实验性)。
- CPU embedding 是主要瓶颈(每 chunk 数秒);真跑需 GPU 或云 embedding。

### 3.5 Python 环境

- 系统 `python3` 无 pip,`python3 -m ensurepip` 引导。
- **不要 `pip install -r requirements.txt`**——它含 agno/crewai/pyautogen/chromadb 全套(为 6 框架对比),极重且易冲突。只装 trpc 路径需要的:`ragas` + `datasets` + `langchain-openai`。

### 3.6 无 key embedding(Ollama)

- 装:`ollama.com/download/ollama-linux-amd64.tar.zst`(注意是 `.tar.zst` 不是 `.tgz`,需 `zstd`),解压 /usr/local,`ollama serve`。
- 模型:`ollama pull nomic-embed-text`(768 维)。
- 框架 `knowledge/embedder/ollama` 指 `localhost:11434`;RAGAS 这类要 OpenAI 协议的用 ollama 兼容端点 `/v1/embeddings`。

---

## 4. 由 benchmark 逼出的框架级问题(核心价值)

### 4.1 T4:流式 usage 累加 bug(已修复)

- **现象**:memory long_context 报 prompt=186710,真实约 2.5 万(虚高 ~10×)。
- **根因**:OpenAI SDK accumulator 对每个 chunk 的 usage 无条件 `+=`;标准端点只发一次 usage 恰好正确,但 CodeBuddy **每个 chunk 重复回传完整累计 usage**,被累加 ~chunk 数次。
- **修复**:take-last(带 usage 的 chunk 覆盖而非累加),默认开,对合规端点零回归。用 benchmark **验证**:同数据同 prompt,186710 → 18671(与本地估算一致)。
- **教训**:我曾两次误判(先"仅 tools 虚高"、后"框架无辜"),都是没往流式累加想。benchmark 的真实大 prompt 场景才逼出这个 bug。

### 4.2 T6:stream-only 后端不鲁棒(系统性,进行中)

框架多处自建 `model.Request` **不设 `Stream`**(默认 false),对只支持流式的 CodeBuddy 网关**静默失效**。benchmark 连续在 3 个不同路径暴露它:

| 路径 | 现象 | 定位 |
|------|------|------|
| `memory/extractor/memory.go` | auto 记忆抽取全失败,F1=0 | 显式定位:改 Stream:true 后 F1→0.746 |
| `session/summary/summarizer.go` | summary 生成被网关拒 | 显式:`Stream: false` 两处 |
| `llmagent` tool 调用循环 | RAG agent **静默卡死**(无输出/无网络/进程不退) | 疑似:tool 后续请求未继承 stream |

- **最隐蔽的是失败方式**:extractor 的错误被 `waitForAutoExtraction` 静默吞没(超时返回 nil),benchmark 若无其事跑完只是 F1=0;tool 循环则直接永久 hang 无报错。**不主动 dump 根本发现不了**。
- **参考修复范式**:`model/codebuddy` provider 在 provider 层强制 `request.Stream=true` 兜住——应下沉为通用机制。

### 4.3 CodeBuddy 网关能力边界

- **无 embedding endpoint**:`/v2/embeddings`、`/embeddings` nginx-401,`/v2/openapi/embeddings` 404。→ 任何 RAG/向量能力在纯 CodeBuddy 下无法工作,必须额外配 embedding 源(本次用 Ollama)。
- **usage 不可信**(见 4.1)、**只支持流式**(见 4.2)。
- 三条合起来:CodeBuddy 作为"唯一后端"有明显短板,是 TODO T2(可信度评估/备选后端)的实测依据。

---

## 5. 方法论收获

- **benchmark 是最好的框架体检**:三块 benchmark 逼出的框架问题(T4/T6)是平时读代码/跑 demo 难发现的——真实数据规模(大 prompt、多 chunk)、真实多阶段流程(抽取/摘要/tool 循环)才会触发。
- **静默失败是最危险的**:extractor 吞错误、tool 循环 hang、usage 虚高——都不报错,只是结果不对/不返回。**验证时要交叉核对**(请求体大小 vs 报告的 token、dump 中间产物、看有无网络活动),不能只信最终数字。
- **比较性结论先核实**:曾误判"外部 fork 领先本地",教训已单独沉淀。任何"A 比 B 新/好"的判断前查 git log/版本/完整符号,不凭单点外推。
- **务实分层跑**:核心链路(建库/检索/问答)与评分层(RAGAS)分开验证;规模不够就限样本先跑通链路,不必强求完整跑分。

---

## 6. 未完成 / 后续

- knowledge 完整 RAGAS 分数未跑出(卡在 T6 的 llmagent tool 循环 hang)——定位该 hang 是 T6 的下一步。
- QMSum / LongMemEval 的 `summary_ondemand`(摘要+按需检索)未跑(需 pgvector)——这是 T1(上下文压缩升级)最相关的成熟范式,值得后续实跑取经。
- 所有实验改动均为临时、已还原;benchmark 子模块与主仓库均干净。
