# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# 语气要求

一轮任务收尾时，写的总结/说明使用自然口语化的叙述，不要电报体。规则：

1. 一句话只讲一件事，避免长句嵌套括号和破折号补充说明；
2. 函数名 / API名如 `foo()` 保留原文，但前后要有完整的谓语和因果连接词（"因为/所以/结果"），不要让变量名孤立地插在名词短语里；
3. 专业缩写或内部术语（如任务编号、内部文档ID）第一次出现时用一句话说明是什么；
4. 优先讲清楚"发生了什么、为什么、结果如何"这条主线，细节数据放在主线之后作为佐证，而不是和主线混在一起堆砌；
5. 报告是为了让读者更容易理解，而不是让人不知所云。

# trpc-go-agent

本项目的任务是**优化 tRPC-Agent-Go 框架**(`trpc-agent-go/`)——一个基于 tRPC-Go 的 Go Agent 框架。

## 工作区布局:1 个工作目标 + 2 个参考仓库

`/data/workspace` 下检出了三个独立的 git 仓库(各有自己的 `.git`),分工明确:

| 目录 | 角色 | 说明 |
|------|------|------|
| `trpc-agent-go/` | ⭐ **工作目标(要改的就是它)** | tRPC-Agent-Go 框架源码(Go 多模块 monorepo,~80 个 `go.mod`,模块路径 `trpc.group/trpc-go/trpc-agent-go`)。所有优化、修改、新增代码都落在这里 |
| `claude-code/` | **设计参考(只读)** | Claude Code CLI 的 source-map 重建快照(TypeScript/Bun)。作为成熟 agent 产品的形态/工具设计/交互范式参考——优化 trpc-agent-go 时,对照它的工具系统、skill、上下文管理等设计取经 |
| `trpc-go/` | **内网服务胶水层(只读)** | tRPC-Go 微服务框架源码(LTS v0.18.x,模块路径 `git.code.oa.com/trpc-go/trpc-go`)。trpc-agent-go 接入内网服务生态(RPC/server/client/plugin、A2A、MCP)的底层依赖,需要核对底层 API 时查它 |

**只修改 `trpc-agent-go/`**;`claude-code/` 和 `trpc-go/` 是只读参考,不要改动。

## 架构:tRPC-Agent-Go 核心抽象(优化时的主链路)

改动前先理解 `trpc-agent-go/` 的这条主链路(均为需要跨文件阅读才能理解的"大图"):

- **`agent/agent.go`** — `Agent` 接口是核心:`Run(ctx, *Invocation) (<-chan *event.Event, error)`、`Tools()`、`SubAgents()` / `SetSubAgents()`。一次调用的上下文封装在 `Invocation` 中。具体实现见 `agent/llmagent/`(LLM agent)、`agent/graphagent/`、`agent/chainagent/`、`agent/parallelagent/`、`agent/cycleagent/`。
- **`runner/runner.go`** — `Runner` 接口驱动 agent 执行,`NewRunner(appName, agent, opts...)` 注入 session/memory 等服务;以 `(userID, sessionID, message)` 为入口,返回事件流。
- **`event/event.go`** — `Event` 内嵌 `*model.Response`,是 agent → 调用方的**流式输出单元**(含 `Done` 标志、`StructuredOutput`、`EventActions`)。整个框架是事件流驱动的。
- **`model/`** — LLM 抽象与 `Response`;`tool/` — 函数工具 / MCP 工具(如 `function.NewFunctionTool(...)`、`mcptool`);`session/`、`memory/`、`knowledge/`、`artifact/` — 持久化状态与 RAG。

### 模块地图(改动前先定位"在哪改")

主链路之外,框架按"编排 / 接入 / 能力 / 协议"分层。优化某模块前先确认它属于哪一层:

- **内置 Agent(`agent/` 下,多数无需自己实现 `Agent` 接口)**:
  - 编排型:`llmagent`(LLM 封装)、`chainagent`(顺序)、`parallelagent`(并发合并)、`cycleagent`(planner+executor 循环)、`graphagent`(类型安全图工作流,底层是 `graph/` + `agent/graph`,功能对标 LangGraph)。
  - 接入外部 runtime 的 adapter agent:`claudecode`、`codex`、`dify`、`n8n`、`weknora`、`a2aagent`(远程 A2A 客户端)、`taskrun`、`transfer`。新增此类适配器时参照同目录已有实现。
- **Model provider(`model/` 下)**:`openai`(含 DeepSeek 等兼容变体 `WithVariant`)、`anthropic`、`bedrock`、`gemini`、`hunyuan`、`ollama`、`huggingface`、`codebuddy`(内网网关,见 `docs/notes/codebuddy-gateway.md`)。可靠性包装:`failover`、`hedge`;token 处理:`tiktoken`、`token_tailor`。Prompt Caching 自动降本是 model 层能力。
- **Tool 生态(`tool/` 下)**:`function`、`mcp`/`mcpbroker`、`agent`(agent-as-tool)、`skill`(skill_load/skill_run 等)、`duckduckgo`/`google`/`arxivsearch`/`wikipedia`/`webfetch`、`file`/`codeexec`/`hostexec`/`workspaceexec`、`openapi`、`todo`、`transfer`、`awaitreply`。`filter.go`/`permission.go`/`retry.go` 是工具调用的横切控制。
- **能力 / 状态层**:`session`、`memory`(均含 in-memory + Redis)、`knowledge`(RAG/vector store)、`artifact`(in-memory/S3/COS)、`planner`、`codeexecutor`(本地/沙箱/docker)、`team`;`skill`(`SKILL.md` 仓库,`NewFSRepository` 支持本地目录或 HTTP(S) 压缩包);`evolution`(异步复盘已完成会话 → 过质量门禁 → 发布为托管 `SKILL.md`,Runner 侧只需 `WithEvolutionService(...)`);`evaluation`(EvalSet + Metric 长期质量度量)。
- **协议 / 服务层(`server/` 下)**:`a2a`(Agent-to-Agent 互通)、`agui`(AG-UI,SSE 把 Runner 事件流推给前端)、`openai`(OpenAI 兼容 API server)、`evaluation`、`promptiter`(提示词自动迭代)。`openclaw/` 是一个完整的 OpenClaw-like gateway 示例服务(Telegram + gateway,含 allowlist/mention gating、稳定 session、同 session 串行执行)。

优化某模块时,可对照 `claude-code/` 中对应能力的成熟实现(工具系统见 `claude-code/src/tools/`、skill 见 `src/skills/`、上下文管理见 `src/context/`),取其设计思路。

## 构建与测试命令

### trpc-agent-go(Go,工作目标)

详见 `trpc-agent-go/AGENTS.md`。要点:

```bash
cd trpc-agent-go
go build ./...                              # 仅根模块
go test ./...                               # 根模块单测(全用 mock,无需 API key)
go test ./agent/... -run TestXxx            # 跑单个测试
cd test && go test ./...                    # E2E 测试(test/ 是独立模块)
golangci-lint run --timeout=10m             # lint(配置 .golangci.yml)
bash .github/scripts/run-go-tests.sh        # CI 式:跨 ~80 个子模块跑测试
bash .github/scripts/check-examples.sh      # CI 式:校验所有 examples 能编译
```

- 多模块 monorepo:根目录 `go test ./...` **只测根模块**,跨模块要用上面的 CI 脚本。`examples/`、`test/`、`docs/mkdocs/assets`、`benchmark` 都是独立 module(各有 `go.mod`),不被根模块的 `./...` 覆盖。
- enable 的 linter:`govet`、`goimports`、`gofmt`、`revive`(强制 exported 注释)、`gocyclo`(复杂度上限 20)、`gosec`、`ineffassign`。提交前确保 exported 符号有规范注释、圈复杂度不超标。
- 根模块依赖 `github.com/mattn/go-sqlite3`,需 **CGO**(`CGO_ENABLED=1` + C 编译器)。
- CI 强制:所有 `.go` 文件带 Tencent Apache 2.0 license header;用 `any` 而非 `interface{}`;`golangci-lint`/`goimports` 需 `$(go env GOPATH)/bin` 在 PATH 上。
- 运行 `examples/` 才需要 `OPENAI_API_KEY`(如 `cd examples/runner && go run . -model=gpt-4o-mini`),测试不需要。

### claude-code(TypeScript / Bun,设计参考)

详见 `claude-code/CLAUDE.md`。**运行时仅限 Bun ≥1.3.13,不支持 Node**。作为只读设计参考,一般无需构建;如要实际跑起来体验其行为:

```bash
cd claude-code
bun install
bun run build:thin        # 改完 src/ 后、测试前必跑(复制 src/→.build/src/ 并重写 import)
bun run repl              # 交互式 REPL
bun run print "提示词"     # 单次无头执行
```

## 框架关键约定(易踩坑)

这些是 README.zh_CN.md / `docs/mkdocs/zh/` 反复强调、改代码或写示例时最容易违反的语义:

- **事件流必须读到关闭**:`Runner.Run` 返回 `<-chan *event.Event`。要中断一次运行,**取消传入的 `context.Context`**,而不是在消费的 `for range` 里直接 `break` 返回——若不再读通道但 agent 还在后台写,会阻塞并泄漏 goroutine。正确姿势:先 cancel,再把通道读到关闭。服务端可用 `agent.WithRequestID(id)` + `runner.ManagedRunner.Cancel(id)` 按请求取消。详见 `docs/mkdocs/zh/runner.md`、`agui.md`。
- **按请求动态构建 Agent**:用 `runner.NewRunnerWithAgentFactory(app, name, factory)`,在每次 `Run` 时按 `agent.RunOptions` 新建 Agent(不同提示词/模型/工具/沙箱)。
- **Skill + CodeExecutor 的组合陷阱**:若给 `LLMAgent` 配 `WithCodeExecutor(...)` 只是为了给 `skill_run` 提供运行时,务必同时 `WithEnableCodeExecutionResponseProcessor(false)`,否则会自动执行 assistant 文本里的 Markdown 围栏代码块。`examples/skill*` 系列均这样配置。
- 优化前先看 `examples/<feature>/`:几乎每个能力都有可运行 Demo,是最快的"标准用法"参照(见下文示例索引)。

## 仓库内文档与示例(先自查,再查 iWiki)

本仓库自带两套 Markdown,改动前应先就近查阅:

- **`docs/mkdocs/{zh,en}/`** — 面向用户的正式文档发布站(由 `docs/mkdocs.yml` 组织),每个核心模块一篇:`agent.md`、`runner.md`、`graph.md`、`model.md`、`tool.md`、`session.md`、`memory.md`、`knowledge/`、`skill.md`、`evolution.md`、`evaluation.md`、`a2a.md`、`agui.md`、`gateway.md`、`openclaw-runtime.md`、`planner.md`、`codeexecutor.md`、`observability.md`、`error-handling.md` 等;`blog/` 有框架全景与各能力的深度长文。
- **`docs/notes/`** — 工程实战笔记/踩坑记录(不进发布站,偏内部 know-how),如 `codebuddy-gateway.md`(CodeBuddy 内网网关直连)。新增此类经验写到这里并更新其 `README.md` 索引;**笔记里一律用占位符,不写真实密钥**。
- **`examples/`(~90 个,独立 module)** — 按目标找入口:第一个多轮 Agent→`runner`;图工作流→`graph`;流式 UI→`agui`;A2A→`a2aagent`;RAG→`knowledge`;评测/提示词迭代→`evaluation`;Skills→`skillrun`;自进化→`evolution`;中断取消→`cancelrun`/`steer`;人在回路→`humaninloop`。

> 仓库内文档与 iWiki 冲突时,仍**以最新的 iWiki 文档为准**(见下节)。

## 知识来源:必须以 iWiki 官方文档为准

tRPC-Go 及 tRPC-Agent-Go 是腾讯内部框架,其权威文档在 iWiki 上持续更新。**当涉及 tRPC-Go / tRPC-Agent-Go 的概念、API、设计、约定时,不要凭训练记忆作答或臆测,必须先用 `iwiki-cli` 查阅官方文档核对**(并可对照本地 `trpc-agent-go/` 源码)。模型内置知识可能过时或与内部实现不符。

读取 iWiki 的能力由 `iwiki-doc` skill 提供(已安装于 `.claude/skills/iwiki-doc/`,依赖 `iwiki-cli`)。命令的完整用法见该 skill 的 `references/scenario_search_and_read.md`。

### 关键文档锚点(已验证)

tRPC 知识库:`spacekey = tRPC`,`spaceid = 47448153`

| 文档 | 文档 ID | 说明 |
|------|---------|------|
| **tRPC-Go**(根节点) | `89292279` | 整个 tRPC-Go 文档树的根 |
| **tRPC-Agent-Go 框架** | `4015773479` | ⭐ 本项目最核心参考,Agent 框架的官方设计 |

`tRPC-Agent-Go 框架`(`4015773479`)子树涵盖框架各模块,搭建底层时应优先对照:
Get-Started、Agent、Graph、Multi-Agent、Event、Runner、Model、Tools、Session、Memory、
Callbacks、Code Executor、Planner、Skill、Artifacts、Debug、A2A、Knowledge、Custom-Agent、
Plugin、Team、Evaluation、HTTP Service、Gateway、可观测、trpc agent 命令行工具 等。

### 如何正确阅读 tRPC-Go 文档

> CLI 路径:Windows 下为 `%LOCALAPPDATA%\iwiki-cli\iwiki-cli.exe`(已加入用户 PATH;若当前 shell 未生效用绝对路径)。下文统一写 `iwiki-cli`。

1. **展开目录树**,定位相关模块(`tree` 用文档 ID 作 `--parent`,不是 spacekey):
   ```bash
   iwiki-cli tree --parent 4015773479      # 列出 tRPC-Agent-Go 各模块
   iwiki-cli tree --parent <子节点ID>       # 标题带 [+] 表示还有下级,可继续展开
   ```

2. **读取文档正文**(返回 Markdown):
   ```bash
   iwiki-cli get <doc_id>
   ```

3. **先看元数据**确认版本与类型(更新时间越新越可信;若 `content_type` 为 `TXDOC`/`TXEXCEL` 需改用 wecom-cli,见 skill 文档):
   ```bash
   iwiki-cli metadata <doc_id>
   ```

4. **按关键词检索**(务必限定到 tRPC 空间,避免噪声):
   ```bash
   iwiki-cli search "关键词" --spaces 47448153 --limit 5
   iwiki-cli search "完整问题句子" --ai --limit 5    # 关键词搜不到时用 AI 语义搜索
   ```

### 工作约定

- **优化/修改某个模块前**,先 `get` 对应的 iWiki 文档并对照 `trpc-agent-go/` 本地源码,以官方设计为准再动手,避免与框架既定语义冲突。
- 引用文档内容时,标注来源文档 ID 或 URL(`https://iwiki.woa.com/p/<doc_id>`),便于复核。
- 文档与本仓库实现冲突时,**以最新的 iWiki 文档为准**并指出差异,不要默默按记忆实现。
- tRPC-Go 区分 v1 / v2:`trpc-go-cmdline` 工具的 v2 与项目用的 trpc-go 版本无关,**目前不推荐项目使用 trpc-go v2**(来源:文档 `118272478` / `99485252`)。涉及版本时务必查文档确认。
