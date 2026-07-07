# Tools / MCP / Skill / Session / Memory 模块组织方式

> 本篇从源码层面厘清 trpc-agent-go 这 5 个模块的**目录分工、核心接口、横切机制、模块间关系**,作为改代码时"在哪改、怎么连"的地图。
> 与 `docs/mkdocs/zh/{tool,session,memory,skill}.md` 的区别:mkdocs 是面向用户的用法说明,本篇是架构层面的组织地图,带 `file:line` 便于复核。
>
> 核实日期:2026-07-07,分支 `feat/lightai`。所有行号基于当前工作树源码,以源码为准。

## 0. 先看大图:五个模块在框架分层里的位置

按 CLAUDE.md 的分层,trpc-agent-go 分"编排 / 接入 / 能力 / 协议"四层。这 5 个模块都属于**能力 / 状态层**,由 Runner 在每次 `Run` 时总装进 `agent.Invocation`,供 Agent 使用:

```
runner.Run(ctx, userID, sessionID, msg)
  │
  ├── getOrCreateSession ──────────► session.Service   (GetSession / CreateSession)
  ├── newRunInvocation ──── 注入 ──► agent.Invocation{
  │                                      SessionService,   // 挂 session.Service
  │                                      MemoryService,    // 挂 memory.Service
  │                                      Session, Tools, Plugins, ...
  │                                   }
  ├── agent.RunWithPlugins(invocation, ag) ◄── Agent 用 invocation 里的工具/状态
  │      └── llmagent 拼 prompt 时:
  │            · 注入 skill 概览 / 已加载 skill 正文(tool/skill 写的 state key)
  │            · 按 sessionService 能力动态挂 session_search/session_load
  │            · memory 工具从 inv.MemoryService 取 service
  │
  └── 事件循环 / 收尾:
         每个 event → sessionService.AppendEvent + EnqueueSummaryJob
         run 完成 → memoryService.EnqueueAutoMemoryJob
                  → ingestor.IngestSession   (mem0/tencentdb 走这条)
```

一句话:Runner 把 session/memory service 挂进 Invocation,工具再从 Invocation 反向取 service;ingestor 是 Runner 私有的收尾出口,不进 Invocation。这个"双向通道"是 5 个模块组合的总纲,详见第 7 节。

---

## 1. Tools 模块

### 1.1 目录组织

`tool/` 顶层放核心接口与横切控制,子目录是各类工具实现:

| 位置 | 职责 |
|------|------|
| `tool/tool.go` | 三大接口 `Tool`/`CallableTool`/`StreamableTool` + 元数据 `Declaration`/`Schema` |
| `tool/toolset.go` | `ToolSet` 接口(工具集生命周期:`Tools`/`Close`/`Name`) |
| `tool/filter.go` | 工具**可见性**过滤(`FilterFunc`/`NewIncludeToolNamesFilter`/`NewExcludeToolNamesFilter`) |
| `tool/permission.go` | 工具**执行权限**策略(`PermissionChecker`/`PermissionPolicy`/`AllowPermission`/`DenyPermission`/`AskPermission`) |
| `tool/retry.go` | 单次 `CallableTool` 调用的重试策略(`RetryPolicy`/`DefaultRetryOn`) |
| `tool/stream.go` + `stream_preferences.go` + `final_result.go` | 流式工具底层(channel 双向流)/ 文本转发偏好 / 最终结构化结果标记 |
| `tool/callbacks.go` | before/after-tool 回调 + `ToolResultMessages`(把结果转成回模型的 message) |
| `tool/metadata.go` | `ToolMetadata`(只读/破坏性/并发安全等)+ `MetadataProvider`/`ConcurrencyAware` |
| `tool/merge.go` | 多工具结果合并(反射驱动:string 拼/数值求和/slice 拼/map 合并/struct 递归) |
| `tool/context.go` | context 键值注入(`ToolCallIDFromContext` 等) |
| `tool/function/` | 函数工具实现,用反射把 Go struct 转 InputSchema(真正反射逻辑在 `internal/tool/tool.go`) |
| `tool/mcp/`、`tool/mcpbroker/` | MCP 两种接入范式(见第 2 节) |
| `tool/skill/` | skill 工具实现(见第 3 节) |
| `tool/agent/`、`tool/file/`、`tool/codeexec/`、`tool/hostexec/`、`tool/webfetch/`、`tool/duckduckgo/`、`tool/google/`、`tool/openapi/`、`tool/todo/`、`tool/transfer/`、`tool/awaitreply/` … | 各类内置工具 / ToolSet |

### 1.2 核心接口(`tool/tool.go`)

```go
type Tool interface { Declaration() *Declaration }                       // tool.go:17
type CallableTool interface {                                            // tool.go:23
    Call(ctx context.Context, jsonArgs []byte) (any, error)
    Tool
}
type StreamableTool interface {                                          // tool.go:34
    StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error)
    Tool
}
type Declaration struct {                                                // tool.go:44
    Name, Description string
    InputSchema, OutputSchema *Schema
}
type ToolSet interface {                                                  // toolset.go:16
    Tools(context.Context) []Tool
    Close() error
    Name() string
}
```

注意 `Tool` 接口只要求 `Declaration()`——光声明不可调用的"占位工具"是合法的(MCP broker 的 `declarationOnlyTool`、memory 的隐藏写工具都用这个)。

### 1.3 横切控制全景与执行时序

这是改工具链路时最容易踩错的地方。一次 `tool_calls` 的完整生命周期(综合 `permission.go:82` 注释、`callbacks.go`、`retry.go`):

```
1. 模型返回 tool_calls
2. (可选) JSON 修复: agent.WithToolCallArgumentsJSONRepairEnabled(true)
3. 框架把 tool_call_id 注入 ctx         (context.go ContextKeyToolCallID)
4. BeforeTool callbacks 依次执行         (callbacks.go RunBeforeTool)
   - 可改写 Arguments / 替换 ctx(须透传原 value 否则丢 ToolCallID)/ 短路 CustomResult
5. 参数定稿 → PermissionRequest.Arguments 即"修复+before-tool 定稿后"载荷 (permission.go:93)
6. Permission 检查:
   a. 工具自带 PermissionChecker.CheckPermission 先执行
   b. 运行级 PermissionPolicy.CheckToolPermission 后执行
   c. 首个非 allow 即停: deny→返回 denied 结果; ask→返回 approval_required 结果
7. 执行工具:
   - CallableTool.Call → 失败按 RetryPolicy 重试(仅 CallableTool,StreamableTool 不支持)
   - StreamableTool.StreamableCall → 返回 StreamReader,按 StreamChunk 消费
8. AfterTool callbacks                    (callbacks.go RunAfterTool,可替换 Result/请求 SkipSummarization)
9. ToolResultMessages callback            (把结果转回模型的 message,空则回退 DefaultToolMessage)
10. 框架把 tool response 发回模型继续推理
```

几个易混边界的区分(`tool.md` 反复强调):
- `agent.WithToolFilter(...)`:控制**模型能看到哪些工具**(发生在模型生成 tool_call 之前)。
- `agent.WithToolExecutionFilter(...)` / `WithExternalTools(...)`:控制已可见的 tool call **是否由框架自动执行**。
- `agent.WithToolPermissionPolicy(...)`:对框架即将执行的每个工具做**权限判断**。
- `WithAdditionalTools` / `WithExternalTools`:为本次运行追加临时可见工具(后者还声明由调用方执行)。

filter/metadata/permission 三者关系:metadata 是纯描述(框架不据此改调度),供 filter/permission/host 决策;filter 管可见性,permission 管执行许可,二者正交。

### 1.4 function 子包:反射生成 schema

`function.NewFunctionTool[I,O](fn, opts...)`(`function/function_tool.go:117`)的流程:取类型参数 `I` 的 `reflect.Type` → 调 `internal/tool/tool.go:GenerateJSONSchema` 生成 `InputSchema`(支持 `jsonschema:"description=...,pattern=...,required"` 与 `description:"..."` 两种 tag;递归类型用 `$defs`+`$ref`;自引用检测)。若用 `WithInputSchema(...)` 提供自定义 schema 则跳过自动生成。流式工具 `NewStreamableFunctionTool` 走同一套,只是 `fn` 返回 `*tool.StreamReader`。

---

## 2. MCP 模块:两种接入范式

MCP(Model Context Protocol)在框架里有两套组织,职责互补、可共存:

### 2.1 tool/mcp —— 直接挂载,远端工具升格为一等 Tool

| 文件 | 职责 |
|------|------|
| `tool/mcp/config.go` | `ConnectionConfig`(Transport/ServerURL/Headers/Command/Args/Timeout/Description/ClientInfo)+ `WithToolFilterFunc`/`WithMCPOptions`/`WithSessionReconnect`/`WithName` |
| `tool/mcp/toolset.go` | `ToolSet` 实现 + `mcpSessionManager`(连接/Init/listTools/callTool/重连/singleflight) |
| `tool/mcp/tool.go` | 把单个 MCP tool 包成 `mcpTool`(实现 `tool.Tool`+`tool.MetadataProvider`),含 annotations→metadata 映射 |
| `tool/mcp/utils.go` | MCP JSON Schema → 框架 `tool.Schema` 递归转换 |

关键点:
- 三种 Transport(`config.go:203` `validateTransport`):`stdio`/`sse`/`streamable`(`"streamable_http"` 是完整名)。
- `Init(ctx)`(`toolset.go:90`)建会话+预拉工具列表,失败即返回错误让调用方 fail fast。
- 会话重连(`WithSessionReconnect`,默认 clamp 到 [1,10]):`executeWithSessionReconnect`(`toolset.go:460`)按 `sessionReconnectErrorPatterns`(10 条错误串,含 `session_expired`/`connection refused`/`HTTP 404`)匹配才重连,`singleflight` 保证多 goroutine 只重连一次。
- MCP annotations → `ToolMetadata`(`tool.go:83`):`readOnlyHint`→`ReadOnly`、`destructiveHint`→`Destructive`、`openWorldHint`→`OpenWorld`。**只映射 server 显式返回的 hint**,未返回则零值(注意 MCP 规范里非只读工具的 destructive/openWorld 默认是 true,框架不补这个默认)。

### 2.2 tool/mcpbroker —— 按需发现,模型渐进探索

| 文件 | 职责 |
|------|------|
| `tool/mcpbroker/broker.go` | `Broker` 主体 + selector 解析(named/ad-hoc HTTP)+ 4 个 Option |
| `tool/mcpbroker/client.go` | 一次性客户端工厂 + HTTP header 注入合并 + 错误拦截 + ClientOptionsProvider |
| `tool/mcpbroker/config.go` | `ConnectionConfig` 归一化 + Transport 推断与校验 + defensive clone |
| `tool/mcpbroker/tools.go` | 4 个 broker 工具的声明(function tool 包装)+ listTools/inspectTools/callTool |

模型可见的 4 个工具(`tools.go:88` `newBrokerTools`):
- `mcp_list_servers`(`broker.go:57`)→ 列出 `WithServers` 注册的命名 server + Description
- `mcp_list_tools`(`broker.go:58`)→ 拿某 selector 的轻量工具目录(name/selector/signature/description,**不带 raw schema**)
- `mcp_inspect_tools`(`broker.go:59`)→ 只展开指定工具的 input/output schema
- `mcp_call`(`broker.go:60`)→ 调用具体 MCP tool(先 `validateCallToolArguments` 校验必填字段)

推荐流程:`mcp_list_servers` → `mcp_list_tools`(拿 selector) → `mcp_inspect_tools`(拿 schema) → `mcp_call`。selector 是统一寻址串:命名 server 用 `<server>` 或 `<server>.<tool>`,ad-hoc HTTP 用 `http(s)://...` 或 `...#tool=<tool>`(片段形式避免点分歧义)。

鉴权扩展点:`WithHTTPHeaderInjector`(运行时按 ctx 注入 token,ad-hoc target 有 sensitive header denylist 防 secret 外泄)、`WithErrorInterceptor`(把 401/403 等底层错误包装成友好信息)、`WithClientOptionsProvider`(host-trusted,可覆盖 Authorization,用于 URL allowlist 等出站策略)。

### 2.3 两者对比

| 维度 | tool/mcp(直接挂载) | tool/mcpbroker(按需发现) |
|------|------|------|
| 代码组织 | 一个 ToolSet 对应一个 MCP server,远端每个 tool 包成 `mcpTool` 作一等 Tool | 一个 Broker 只暴露 4 个固定 function tool,远端工具不直接挂载 |
| 生命周期 | 长连接(`Init` 建会话,`Tools` 每次 list 刷新,`Close` 关会话) | 一次性连接(每次 list/inspect/call 走 `withOneShotClient`,无会话复用) |
| Transport | stdio/SSE/streamable 全支持 | 命名 server 全支持;ad-hoc 模式强制只允许 HTTP |
| 权限 metadata | MCP annotations 直接映射 `ToolMetadata`,agent 据此做权限决策 | 不映射 annotations,模型只看 broker 返回的 name/description/schema 摘要 |
| 认证/Header | 静态 header + 透传 `tmcp.WithHTTPBeforeRequest` | 静态 header + `WithHTTPHeaderInjector` + `WithClientOptionsProvider` |
| 错误处理 | 自动重连重试(singleflight 去重) | `WithErrorInterceptor` 转译,无自动重连 |
| 适用场景 | 已知、可信、稳定的 MCP server,工具集有限 | 多个/动态/部分可信的 MCP server,长尾工具多、单次只命中少量 |

两者可共存:核心高频 MCP 走 `tool/mcp` 升格为一等 Tool,长尾/动态能力放 `tool/mcpbroker`。broker 反复强调是 passthrough,远端内容默认不可信,需宿主在 `WithServers`/`WithAllowAdHocHTTP`/`WithClientOptionsProvider`/`WithErrorInterceptor` 层约束。

---

## 3. Skill 模块

### 3.1 目录组织

`skill/` 放仓库抽象与状态模型,`tool/skill/` 放模型可调用的工具实现:

| 文件 | 职责 |
|------|------|
| `skill/repository.go` | `Repository` 接口 + `FSRepository`(文件系统后端)+ state key 常量 |
| `skill/context_repository.go` | `ContextRepository`(按请求/运行时构建的可变视图)+ `NewFilteredRepository` |
| `skill/scope.go` | `SkillScope`(app/user 隔离)+ `RepositoryProvider` |
| `skill/state_keys.go` | `LoadedKey`/`DocsKey`(按 agent 隔离的 state key 生成) |
| `skill/state_order.go` | `LoadedOrderKey` + LRU 式触摸顺序(配合 max-loaded-skills 淘汰) |
| `skill/url_root.go` | `resolveSkillsRoot` 支持 `http(s)://`(下载解压到缓存)/`file://`/本地路径 |
| `tool/skill/load.go` | `skill_load` 工具(写 loaded/docs state) |
| `tool/skill/select_docs.go` | `skill_select_docs` 工具(add/replace/clear 文档选择) |
| `tool/skill/list_docs.go` | `skill_list_docs` 工具(只读列文档) |
| `tool/skill/run.go` + `exec.go` + `stager.go` | `skill_run`/`skill_exec` 执行工具 + skill 物化进 workspace |

### 3.2 Repository 接口层级

```go
type Repository interface {                                    // repository.go:68
    Summaries() []Summary                                      //   概览(name+description)
    Get(name string) (*Skill, error)                           //   全量(Body+Docs)
    Path(name string) (string, error)                          //   skill 目录路径(供 staging)
}
type RootedRepository interface { Repository; Roots() []string }              // :80
type RefreshableRepository interface { Repository; Refresh() error }         // :86
type ContextRepository interface {                             // context_repository.go:23
    Repository
    SummariesForContext(ctx) []Summary                         //   按请求过滤的可见 skill
    GetForContext(ctx, name) (*Skill, error)
    PathForContext(ctx, name) (string, error)
}
```

`ContextRepository` 解决"同一进程里不同调用方只看到 skill 子集"(按用户/租户/权限过滤)。`NewFilteredRepository(base, filter)` 用 `VisibilityFilter func(ctx, Summary) bool` 包装 base。包级 `SummariesForContext`/`GetForContext`/`PathForContext` 做类型断言:repo 是 `ContextRepository` 走 context 版,否则回退普通 `Repository`。这影响 `schema.go` 的工具 schema 生成——动态可见时不生成 skill 名 enum。

`NewFSRepository` 的 roots 参数支持多目录(通用 skills + 用户私有 skills)和 HTTP(S) URL(zip/tar.gz 压缩包,`url_root.go` 下载到 `<usercache>/trpc-agent-go/skills/<sha256(url)>`,带 `.ready` 原子标记 + 路径穿越防护)。

### 3.3 三层信息模型 + state key

skill 的核心设计是"概览 → 正文 → 文档/脚本"渐进式披露,靠 session state key 串联:

- **概览层**(极低成本):仅注入 `SKILL.md` 的 name/description 到系统消息,让模型知道有哪些技能。
- **正文层**(按需):模型调 `skill_load`,框架把 `SKILL.md` 正文物化到下一次模型请求(默认追加到 system message;`WithSkillsLoadedContentInToolResults(true)` 追加到 tool result,更利于 prompt cache)。
- **文档/脚本层**:文档按 `skill_select_docs` 选择后物化;脚本不内联,在隔离 workspace 执行。

`skill_load` 的 `Call` 只回 `loaded: <name>`,真正副作用在 `StateDelta`(`load.go:151`):

```go
skill.LoadedKey(agentName, skill)  = "1"              // 已加载标记
skill.DocsKey(agentName, skill)    = "*" 或 JSON数组   // 文档选择
skill.LoadedOrderKey(agentName)    = 触摸顺序数组       // LRU 淘汰用
```

state key 按 agent 隔离(`state_keys.go`):agentName 非空时用 `temp:skill:loaded_by_agent:<agent>/<skill>`,空则回退 legacy `temp:skill:loaded:<skill>`。**按 agent 隔离的目的是多 agent 共享一个 Session 时,子 agent 的 skill_load 不污染父 agent 的 prompt**。`SkillLoadMode`(turn/once/session)控制这些 key 的生命周期——默认 `turn` 在下一轮 `Runner.Run` 开始前清空。

### 3.4 tool/skill 工具与 codeexecutor 的组合

`skill_load`/`skill_select_docs`/`skill_list_docs` 是"上下文装载"类工具(知识注入,不执行脚本)。`skill_run`(`run.go`)/`skill_exec`(`exec.go`)才是执行工具:

1. `prepareWorkspaceForRun`(`run.go:636`):用 `workspacesession.Resolver` 从 `codeexecutor.CodeExecutor` 解析出 engine + workspace。
2. `stageSkillForRun` → `SkillStager.StageSkill`(`stager.go:33`)把整个 skill 目录从 repo 物化到 workspace 的 `skills/<name>` 下(默认 `copySkillStager`,可 `WithSkillStager` 替换)。
3. `runProgram` 在 skill 根目录跑命令(默认 `bash -c`;配置 allowed/denied commands 时走无 shell 单命令模式)。
4. `prepareOutputs`/`autoExportWorkspaceOut` 收集产出 + `buildRunOutput`(截断 stdout/stderr、选 primary_output、可选 `save_as_artifacts` 存 Artifact)。
5. `StateDelta` 把 artifact 引用写进 `skill.StateKeyArtifacts`(`temp:skill:artifacts`)。

**与 codeexecutor 的组合要点**:`WithSkills(repo)` 开启但未显式传 `WithCodeExecutor(...)` 时,框架自动挂本地 code executor 让 `workspace_exec` 可用(fallback);三种情况跳过 fallback:(1)已传 `WithCodeExecutor`、(2)用了 `WithAllowedSkillTools` 精细控制、(3)显式 `WithSkillToolProfile(SkillToolProfileKnowledgeOnly)` opt-out。**`CodeExecutor` 与围栏代码自动执行是两个独立开关**:`CodeExecutor` 只让 `workspace_exec` 可用,不会自动执行模型回复里的 Markdown 围栏代码块(后者由 `EnableCodeExecutionResponseProcessor` 控制,默认 true)。

### 3.5 mcpbroker ↔ skill 的天然配合

`mcpbroker` 解决"如何连、如何看、如何调",不解决"模型为什么会想到去连这个 MCP"。skill 正好补这层信息来源:skill 可在加载后提供远端 MCP endpoint 的 URL/能力描述,模型据此调 `mcpbroker` 的 ad-hoc HTTP 动态连接。`examples/mcpbroker/basic` 演示了 `skill_load → mcp_list_tools → mcp_inspect_tools → mcp_call` 的链路。

---

## 4. Session 模块

### 4.1 目录组织

`session/` 顶层放接口与数据模型,`internal/` 放后端共享逻辑,各后端子目录只写持久化:

| 位置 | 职责 |
|------|------|
| `session/session.go` | `Service` 接口 + `Session`/`StateMap`/`Summary`/`SummaryBoundary` + 扩展接口 `SearchableService`/`WindowService` |
| `session/state.go` | `State`(带 pending delta 的状态值容器)+ state 前缀常量(`app:`/`user:`/`temp:`) |
| `session/track.go` | `Track`/`TrackEvent`(会话内独立事件流,如遥测/审计侧信道)+ `TrackService` |
| `session/hook.go` | `AppendEventHook`/`GetSessionHook`(`next()` 责任链模式) |
| `session/ingestor.go` | `Ingestor` 接口(把 session transcript 投递给外部记忆平台) |
| `session/internal/summary/` | 异步摘要 worker + dispatch policy + 增量摘要编排 + 进程内去重锁(**所有后端共享**) |
| `session/internal/sessionopt/` | ListSessions 的排序与分页(所有后端共用) |
| `session/internal/session/hook/` | Hook 责任链执行器 |
| `session/internal/window/` | 锚点事件窗口加载语义(后端 `GetEventWindow` 复用) |
| `session/inmemory/` `redis/` `postgres/` `mysql/` `clickhouse/` `mongodb/` `sqlite/` `pgvector/` | 各存储后端(只写持久化,共享逻辑在 internal/) |
| `session/summary/` | 会话摘要对外 API(`Summarizer`/`Checker`/hooks) |

### 4.2 核心接口与数据结构

`Service` 接口(`session.go:1046`,后端必须实现):

```go
type Service interface {
    CreateSession(ctx, key Key, state StateMap, options ...Option) (*Session, error)
    GetSession(ctx, key Key, options ...Option) (*Session, error)
    ListSessions(ctx, userKey UserKey, options ...Option) ([]*Session, error)
    DeleteSession(ctx, key Key, options ...Option) error
    UpdateAppState / DeleteAppState / ListAppStates / UpdateUserState / ListUserStates / DeleteUserState  // app/user 级状态
    UpdateSessionState(ctx, key Key, state StateMap) error   // 禁 app:/user: 前缀,允许 temp:
    AppendEvent(ctx, session *Session, event *event.Event, options ...Option) error
    CreateSessionSummary(ctx, sess *Session, filterKey string, force bool) error
    EnqueueSummaryJob(ctx, sess *Session, filterKey string, force bool) error
    GetSessionSummaryText(ctx, sess *Session, opts ...SummaryOption) (string, bool)
    Close() error
}
```

扩展接口(后端按能力选实现):
- `SearchableService.SearchEvents`(`session.go:987`):向量检索,**非向量后端不实现**。
- `WindowService.GetEventWindow`(`session.go:1036`):锚点事件窗口(精确加载某 event 前后 N 条)。
- `TrackService.AppendTrackEvent`(`track.go:26`):track 侧信道。

核心数据结构:
- `Session`(`session.go:61`):`ID/AppName/UserID/State StateMap/Events []event.Event/Tracks/Summaries map[string]*Summary/Hash`。`Hash` 是创建时按 `appName:userID:sessionID` 的 FNV32a 哈希,**异步任务分片用**(摘要/memory worker 按 `session.Hash % workerNum` 选通道,保证同一 session 串行)。
- `StateMap = map[string][]byte`(`session.go:29`):状态以字节存,前缀 `app:`/`user:`/`temp:` 区分作用域。
- `Summary`/`SummaryBoundary`(`session.go:586/608`):摘要 + 覆盖边界(`CutoffAt`/`LastEventID`),支持增量摘要。

### 4.3 横切能力

- **Hook(责任链)**:`AppendEventHook`/`GetSessionHook` 都是 `func(ctx, next func() error) error` 中间件。`internal/session/hook/hook.go` 的 `RunAppendEventHooks` 把 hooks 串成链,链尾 `final` 是真正落库操作;任一 hook 不调 `next()` 即中止。所有后端统一接入(inmemory 在 `AppendEvent`/`GetSession` 里调 `hook.Run*Hooks`)。典型用法:`WithAppendEventHook` 给事件自动打 `FilterKey` 标签、内容安全审计、`skill_load` 写 state 后做 max-loaded-skills 淘汰。
- **Ingestor**:`Ingestor.IngestSession(ctx, sess, opts...)`(`ingestor.go:98`),把已完成会话 transcript 投递给外部长期记忆平台(mem0/tencentdb)。**独立于 `Service`,后端不强制实现**,由 Runner 在收尾阶段调用(见第 7 节)。
- **Track**:`TrackEvent{Track, Payload json.RawMessage, Timestamp}`,track 索引存于 `State["tracks"]`。用于侧信道数据(遥测/审计/自定义追踪),不进主对话历史。

### 4.4 后端复用模式

各后端只写持久化,共享逻辑集中在 `session/internal/`:
- `internal/summary`:`AsyncSummaryWorker`(hash 分片队列,队列满回退同步)+ `SummarizeSession`(增量摘要编排)+ `summaryLockGroup`(进程内去重锁,防同一会话并发摘要)+ `DetachContext`(剥离请求取消,使摘要异步跑完)+ `CreateSessionSummaryWithCascade`(按 policy.SummaryTargets 决定生成几个摘要,单 key 优化只调一次 LLM 再复制)。
- `internal/sessionopt`:`SortByUpdatedDesc`(UpdatedAt 降序,ID 兜底)+ `ApplyListPage`。
- `internal/window`:`EventWindowFromOrderedEvents`(锚点窗口语义)。

inmemory 作代表:`apps[appName]→*appSessions`,`appSessions.sessions[userID][sessionID]→*sessionWithTTL`,TTL 包装层统一处理过期 + 后台 cleanup。`mergeState` 把 app/user state 加前缀注入 session.State,使 GetSession 返回的 session 自带合并后的全量 state。摘要三方法(`CreateSessionSummary`/`GetSessionSummaryText`/`EnqueueSummaryJob`)委托给 `internal/summary` 共享逻辑。

### 4.5 summary 子包

`session/summary/` 是会话摘要的对外 API:

- `SessionSummarizer` 接口(`summary.go:23`):`ShouldSummarize`/`Summarize`/`SetPrompt`/`SetModel`/`Metadata`。`ContextAwareSummarizer`(`summary.go:51`)扩展 `ShouldSummarizeWithContext`。
- `sessionSummarizer` 实现(`summarizer.go:211`):`model`/`prompt`/`systemPrompt`/`cacheSafeForking`/`checks []ContextChecker`/`maxSummaryWords`/`skipRecentFunc`/`preHook`/`postHook`/`modelCallbacks`/`toolCallFormatter`/`toolResultFormatter`。
- 触发 Checker(`checker.go`):`CheckEventThreshold`/`CheckTokenThreshold`/`CheckTimeThreshold`/`CheckContextThreshold`(**动态按模型 context window 比例触发**,默认 0.5,从 invocation per-run override → inv.Model → fallback 解析)。组合器 `ChecksAll`(AND)/`ChecksAny`(OR)。
- 增量摘要:`internal/summary/summary.go` 基于 `SummaryBoundary`(含 `LastEventID`/`CutoffAt`)做增量,`prependPrevSummary` 把上一轮摘要作为合成 system 事件拼到 delta 前,使 LLM 在"旧摘要+新增事件"上做增量摘要。
- Hook:`WithPreSummaryHook`(改 text/events/ctx)/`WithPostSummaryHook`(改 summary)/`WithSummaryHookAbortOnError`(默认 false 忽略错误)。
- ModelCallbacks:`WithModelCallbacks` 注入 `*model.Callbacks`,在 `model.GenerateContent` 前后跑 before/after 回调(可短路返回 CustomResponse)。
- cache-safe fork(`cache_safe_fork.go`):`WithCacheSafeForking` + parent request context,深拷贝 parent request 追加 compacting user message,复用上游 prompt cache。

---

## 5. Memory 模块

### 5.1 目录组织

| 位置 | 职责 |
|------|------|
| `memory/memory.go` | `Service` 接口 + `Entry`/`Memory`/`Key`/`UserKey`/`Metadata`/`SearchOptions` + 6 个工具名常量 |
| `memory/extractor/` | 自动记忆提取(`MemoryExtractor` 接口 + `Extractor` 实现 + Checkers + LLM 工具调用流) |
| `memory/internal/memory/` | **后端共享算法库**(GenerateMemoryID/BM25 lexical 检索/CJK 分词/Jaccard 去重/RRF 混合检索/Auto 模式默认工具开关) |
| `memory/inmemory/` `redis/` `mysql/` `mysqlvec/` `postgres/` `pgvector/` `sqlite/` `sqlitevec/` `tencentdb/` | 各存储后端(只写持久化,共享算法在 internal/) |
| `memory/tool/` | 6 个记忆工具(add/update/delete/search/load/clear) |
| `memory/mem0/` | mem0 外部平台适配(ingest-first 模式) |

### 5.2 核心接口

`Service` 接口(`memory.go:155`):

```go
type Service interface {
    AddMemory(ctx, userKey UserKey, memory string, topics []string, opts ...AddOption) error     // 幂等(SHA256 去重)
    UpdateMemory(ctx, memoryKey Key, memory string, topics []string, opts ...UpdateOption) error
    DeleteMemory(ctx, memoryKey Key) error
    ClearMemories(ctx, userKey UserKey) error
    ReadMemories(ctx, userKey UserKey, limit int) ([]*Entry, error)
    SearchMemories(ctx, userKey UserKey, query string, opts ...SearchOption) ([]*Entry, error)
    Tools() []tool.Tool                              // 暴露给 agent 的工具
    EnqueueAutoMemoryJob(ctx, sess *session.Session) error   // Auto 模式入队
    Close() error
}
```

`Entry`(`memory.go:228`):`ID/AppName/Memory *Memory/UserID/CreatedAt/UpdatedAt/Score`。`UserKey`(`:256`):`AppName/UserID`(隔离维度 `<appName, userID>`,跨会话持久)。Memory ID 基于"内容+app+user+规范化事件元数据"的 SHA256,**主题不参与 ID**——重复添加相同内容是幂等的(覆盖更新,刷新 topics 和 updated_at)。

### 5.3 两种记忆模式

代码上的分水岭是 `WithExtractor`(`inmemory/options.go:197`):

- **Agentic 模式**(extractor 为 nil):6 个工具按 `DefaultEnabledTools`(add/update/search/load)直接暴露给 agent,agent 主动调用写记忆。
- **Auto 模式**(extractor 非空,推荐):`ApplyAutoModeDefaults`(`internal/memory/memory.go:250`)调整默认 enabledTools——add/update/delete 默认开但**对 agent 隐藏**、search 暴露、load 默认关、clear 默认关。`AutoMemoryWorker`(`internal/memory/auto.go:192`)在后台跑 LLM 抽取:增量扫描(`scanDeltaSince`,只取 since 之后的 user/assistant 消息)→ `ShouldExtract` gate → `createAutoMemory`(先 `searchRelevantMemories` 取相关已有记忆喂给 extractor prompt,再 `Extract`,再 `reconcileOps` 用 Score+Jaccard 双信号去重,最后落库)。Runner 在每轮结束后调 `EnqueueAutoMemoryJob` 触发。

`Extractor`(`extractor/memory.go:43`):`model`/`prompt`/`checkers []Checker`/`enabledTools`/`modelCallbacks`。Checkers:`CheckMessageThreshold(n)`/`CheckTimeInterval(d)`/`ChecksAll`/`ChecksAny`。`ExtractionContext`:`UserKey`/`Messages`/`LastExtractAt`。

### 5.4 后端复用与工具取 service

各后端共享 `internal/memory/memory.go` 的算法:`GenerateMemoryID`、`SearchEntries`(BM25+Jaccard+RRF)、`BuildToolsList`(按模式+enabled/exposed/hidden 决定 `Tools()` 暴露哪些)。inmemory 实现 `memory.Service` 持有 `apps map[string]*appMemories`,`SearchMemories` 委托给 `imemory.SearchEntries`。

6 个工具从 ctx 取 service 的统一入口:`GetMemoryServiceFromContext`(`tool/tool.go:477`),通过 `agent.InvocationFromContext(ctx)` 取 `*agent.Invocation`,再读 `invocation.MemoryService`。`GetAppAndUserFromContext` 从 `invocation.Session.AppName/UserID` 取身份。所以工具不直接依赖任何具体后端,只面向 `memory.Service` 接口——runner 在构造 Invocation 时把 `WithMemoryService(svc)` 注入的 service 挂上去(见第 7 节)。

### 5.5 mem0/tencentdb:为何用 Ingestor 而非 MemoryService

runner 有两条独立的会后回调路径:
- `memoryService` 路径:调 `EnqueueAutoMemoryJob`,前提是后端完整实现 `memory.Service`(含 AddMemory 等可被 extractor 调用的写操作)。
- `ingestor` 路径:调 `IngestSession`,把整段对话 transcript 推给远端。

mem0 和 tencentdb 是**外部托管的长期记忆平台**:写入是"把整段对话 transcript 推给远端"(`IngestSession`),不是按条 AddMemory。所以它们实现 `session.Ingestor` 接口(只有 `IngestSession`),通过 `WithSessionIngestor` 注入,**不实现完整 `memory.Service`,也不被 framework 的 extractor 驱动**。读路径上它们各自实现 `ReadMemories`/`SearchMemories` 并通过自己的 `Tools()`(mem0 只暴露 search;tencentdb 暴露 `tdai_*` 系列)给 agent 用。

tencentdb 额外提供 `Plugin()`(`tencentdb/plugin.go`)做 `BeforeModel` 自动 recall 注入——这是 framework memory 后端没有的能力。但 tencentdb 的 `WithRecallEnabled`/`WithMemorySearchTool` 默认关闭,注释直说原因是**共享 sidecar 网关不强制 user/session 隔离,跨租户读取有风险**。这是"托管平台"与"框架自带后端"在信任边界上的本质差异。

一句话区分:`MemoryService` 表示"框架直接管理记忆",`SessionIngestor` 表示"框架把 transcript 交给外部记忆系统"。

---

## 6. 模块间关系:Runner 总装与组合点

### 6.1 Runner 如何注入 service

四个 runner option(`runner/runner.go`)把 service 存进 runner 字段:

| Option | runner 字段 | option 行 |
|--------|------------|----------|
| `WithSessionService` | `r.sessionService` | `runner.go:83` |
| `WithMemoryService` | `r.memoryService` | `runner.go:96` |
| `WithSessionIngestor` | `r.ingestor` | `runner.go:110` |
| `WithPlugins` | `r.pluginManager`(经装配,非直接字段) | `runner.go:151` |

未提供 session service 时 runner 默认建 `inmemory.NewSessionService()`(`runner.go:370`),`ownedSessionService` 标记自建,`Close()` 只关自建的。

Runner 在 `newRunInvocation`(`runner.go:756`)把 service 挂到 Invocation:

```go
invocationOpts := []agent.InvocationOptions{
    agent.WithInvocationSession(sess),
    agent.WithInvocationSessionService(r.sessionService),   // → inv.SessionService (agent/invocation.go:151)
    agent.WithInvocationMemoryService(r.memoryService),    // → inv.MemoryService  (agent/invocation.go:170)
    agent.WithInvocationPlugins(combineRunPlugins(r.pluginManager, ro.Plugins)),
    ...
}
```

**注意**:ingestor 不挂到 Invocation,是 runner 私有字段,只在 runner 收尾回调里直接调用。

### 6.2 一次 Run 的生命周期调用顺序

`Run` 入口 `runner.go:511`,按时间顺序:

1. **加载/创建会话**:`getOrCreateSession`(`runner.go:571`)→ `GetSession`(`:1164`),miss 则 `CreateSession`(`:1186`)。
2. **注册 run(绑 cancel)**:`registerRun(ro.RequestID, ..., execCancel, ...)`(`runner.go:669`),存进 `r.runs[requestID]`。
3. **挂会话事件追加器**:`attachSessionAppender`(`runner.go:720`),回调里调 `r.sessionService.AppendEvent`(`runner.go:820`)。
4. **驱动 agent**:`agent.RunWithPlugins(startCtx, invocation, ag)`(`runner.go:734`)。
5. **事件循环**:`processAgentEvents`(`runner.go:1257`)消费事件流。每个可持久化 event 先 `AppendEvent`(`runner.go:2456`),再 `EnqueueSummaryJob`(`runner.go:2505`)入队异步摘要。**摘要在逐事件追加后触发,不是 run 结束才做**。
6. **run 完成收尾**(`runner.go:2849-2856`):
   - `enqueueAutoMemoryJob` → `memoryService.EnqueueAutoMemoryJob`(`runner.go:3904`)
   - `enqueueEvolutionLearningJob`
   - `enqueueSessionIngest` → `ingestor.IngestSession`(`runner.go:3941`)

所以:session service 在整个生命周期被反复调(GetSession → AppendEvent + EnqueueSummaryJob);memory service 仅在完成阶段被 `EnqueueAutoMemoryJob` 调一次;ingestor 仅在完成阶段被 `IngestSession` 调一次。

### 6.3 六条关键组合链

| 组合链 | 怎么连 | 关键 file:line |
|--------|--------|----------------|
| **session recall 工具 ← SessionService** | `session_search`/`session_load` 从 `inv.SessionService` 做接口断言取 `SearchableService`/`WindowService`;llmagent 在 `surface_runtime.go:332` 的 `appendOnDemandSessionTools` 按 `WithEnableOnDemandSession(true)` + 后端能力**动态注册**(非向量后端不挂 `session_search`) | `internal/session/tool/recall/{search,load,types}.go`;`agent/llmagent/llm_agent.go:1163` |
| **memory 工具 ← MemoryService** | 6 个 memory 工具从 `inv.MemoryService` 取 service,从 `inv.Session` 取身份 | `memory/tool/tool.go:477`;`runner.go:774` |
| **skill_run/exec ← codeexecutor** | `workspacesession.Resolver` 从 `codeexecutor.CodeExecutor` 解析 engine+workspace,`SkillStager` 把 skill 物化到 `skills/<name>` | `tool/skill/run.go:636`;`tool/skill/stager.go:33` |
| **mcpbroker ↔ skill** | skill 提供远端 MCP endpoint 信息源,mcpbroker 的 ad-hoc HTTP 动态连接 | `tool/mcpbroker/`;第 3.5 节 |
| **context compaction ↔ session_load** | 压缩占位符带 `event_id`,模型用 `session_load` 配合 `content_offset`/`content_limit` 取回原始内容(T1 可恢复压缩闭环) | `internal/flow/processor/context_compact.go`;`internal/session/tool/recall/load.go` |
| **mem0/tencentdb ← Ingestor** | Runner 完成阶段调 `ingestor.IngestSession`,不进 Invocation | `runner.go:3941`;第 5.5 节 |

### 6.4 按请求取消(ManagedRunner.Cancel)

机制是"requestID ↔ context.CancelFunc"注册表:
- 调用方经 `agent.WithRequestID(id)` 设到 `RunOptions.RequestID`(不设则 runner 自动生成并回填进每个 emitted event)。
- `Run` 里建可取消的执行 context `execCtx, execCancel`(`newExecutionContext`,`runner.go:1001`),`registerRun` 把 `{cancel: execCancel, ...}` 存进 `r.runs[requestID]`(`runner.go:1057`)。
- `ManagedRunner.Cancel(requestID)`(`runner.go:950`)→ `lookupCancel` 查表调 `CancelFunc` → 取消 `execCtx` → agent 停下、事件流读到关闭。
- run 结束 `unregisterRun`(`runner.go:1066`)从 map 删掉,避免泄漏。

这是 CLAUDE.md 反复强调的"按请求取消"实现,对应 `agent.WithRequestID(id)` + `runner.ManagedRunner.Cancel(id)`。**要中断一次运行,取消传入的 context,而不是在消费的 `for range` 里直接 `break`**——若不再读通道但 agent 还在后台写,会阻塞并泄漏 goroutine。

---

## 7. 几个值得注意的设计点

改这些模块时,以下设计是"有意为之",别无意破坏:

1. **动态能力注册**:recall 工具不是固定挂死,而是 llmagent 每次 run 时按 `inv.SessionService` 的接口断言能力决定挂不挂、挂哪个。换非向量后端,`session_search` 自动消失。这是"session 后端能力决定 agent 可用工具"的组合点。

2. **internal/ 共享避免后端重复**:session 和 memory 都把共享逻辑(摘要编排/检索算法/分页排序)放 `internal/`,各后端只写持久化与 `CreateSummaryFunc`/`CreateSummaryFunc`。改检索/摘要逻辑优先看 `internal/`,不要去每个后端改一遍。

3. **按请求取消 + 读通道到关闭**:取消走 context.CancelFunc,agent 停下后事件流关闭,消费方要读到关闭。这是事件流驱动框架的通用约定。

4. **Ingestor vs MemoryService 的边界**:`MemoryService` = 框架直接管理记忆(完整 CRUD + Auto 抽取);`SessionIngestor` = 框架把 transcript 交给外部记忆系统(只投递,不 CRUD)。新增外部记忆平台优先实现 `Ingestor`。

5. **skill 的 agent 隔离 state key**:`temp:skill:loaded_by_agent:<agent>/<skill>` 让多 agent 共享一个 Session 时互不污染。改 skill state 相关逻辑要保留这套隔离。

6. **横切控制时序**:filter(可见性)→ JSON 修复 → ToolCallID 注入 → BeforeTool callbacks → permission(基于定稿后参数)→ 执行(retry)→ AfterTool → ToolResultMessages → 回模型。permission 的 `PermissionRequest.Arguments` 是"修复+before-tool 定稿后"的载荷,不是原始 arguments。

7. **异步任务错误可见性**(关联 TODO.md T6 问题二):memory 自动抽取/evolution 复盘跑在后台 goroutine,错误只落 WARN 日志。若要观测"后台连续失败",需加计数器/事件/回调,而非依赖日志。

---

## 8. 关键文件速查索引

| 模块 | 核心文件 | 一句话 |
|------|---------|--------|
| Tools | `tool/tool.go` | Tool/CallableTool/StreamableTool/Declaration/Schema 接口 |
| Tools | `tool/toolset.go` | ToolSet 接口 |
| Tools | `tool/filter.go` `permission.go` `retry.go` `callbacks.go` `metadata.go` `merge.go` `stream.go` | 横切控制 7 件套 |
| Tools | `tool/function/function_tool.go` + `internal/tool/tool.go` | 函数工具 + 反射 schema 生成 |
| MCP | `tool/mcp/{config,toolset,tool,utils}.go` | 直接挂载范式 |
| MCP | `tool/mcpbroker/{broker,client,config,tools}.go` | 按需发现范式 |
| Skill | `skill/repository.go` | Repository 接口 + FSRepository + state key 常量 |
| Skill | `skill/context_repository.go` `scope.go` `state_keys.go` `url_root.go` | 上下文仓库/scope/state 命名/远程仓库 |
| Skill | `tool/skill/{load,select_docs,list_docs,run,exec,stager}.go` | skill 工具实现 |
| Session | `session/session.go` | Service 接口 + Session/Summary + SearchableService/WindowService |
| Session | `session/{state,track,hook,ingestor}.go` | 状态/Track/Hook/Ingestor |
| Session | `session/internal/summary/` `sessionopt/` `session/hook/` `window/` | 后端共享逻辑 |
| Session | `session/summary/{summarizer,checker,options,hook,cache_safe_fork,dynamic}.go` | 摘要对外 API |
| Memory | `memory/memory.go` | Service 接口 + Entry/UserKey + 工具常量 |
| Memory | `memory/extractor/{memory,checker,tools,extractor}.go` | 自动提取 |
| Memory | `memory/internal/memory/{memory,auto}.go` | 共享算法 + Auto worker |
| Memory | `memory/tool/tool.go` | 6 个记忆工具 |
| Memory | `memory/{mem0,tencentdb}/` | 外部平台 Ingestor 适配 |
| Recall | `internal/session/tool/recall/{search,load,types}.go` | session_search/session_load 工具 |
| Runner | `runner/runner.go` | 总装注入 + 生命周期编排 + 按请求取消 |

> 本文行号基于 2026-07-07 的 `feat/lightai` 分支源码,后续若重构需复核。模块间组合的更细机制(如 context compaction 可恢复闭环)见 [TODO.md](TODO.md) T1 与 [claude-code-compact-design.md](claude-code-compact-design.md)。
