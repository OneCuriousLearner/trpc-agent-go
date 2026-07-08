# 工程笔记 (notes)

这里收录开发 / 运维 tRPC-Agent-Go 时积累的**实战 know-how 与踩坑记录**——偏内部经验，不进 mkdocs 发布站（`docs/mkdocs/`），也不算面向用户的正式文档。

> 面向用户的正式文档在 `docs/mkdocs/{en,zh}/`,由 `docs/mkdocs.yml` 组织。

## 索引

| 笔记 | 内容 |
|------|------|
| [TODO.md](TODO.md) | ⭐ 框架优化待办 backlog(对标 Claude Code 等取经)。当前 T1–T8:上下文压缩升级(prompt+熔断器完成)、CodeBuddy 可信度评估、统一 token 入口(已完成)、流式 usage 修复(已完成)、benchmark 钉本地(已完成)、stream-only 不鲁棒(已复核)、空 completion 死循环(已修复)、summary_ondemand 取经。随挖掘持续追加。 |
| [benchmark-exploration.md](benchmark-exploration.md) | ⭐ benchmark(memory/summary/knowledge)实跑探索分析报告:工作流 / 测试数据 / 实测结果 / 踩坑 / 由此逼出的框架级问题(T4/T6)/ 方法论收获。 |
| [claude-code-compact-design.md](claude-code-compact-design.md) | ⭐ Claude Code 上下文压缩(compact)设计精华:5 层分级流水线逐层拆解 + 横切设计(四级阈值/熔断器/两段式 prompt/缓存感知分流/PTL 重试/post-compact 附件恢复) + 对照 trpc-agent-go 现状标注 gap + 给 T1 的启示。T1 设计参照。 |
| [codebuddy-gateway.md](codebuddy-gateway.md) | CodeBuddy 内网模型网关接入实战:真实端点 / 必需请求头 / 抓包方法 / 错误码对照 / §4b token usage 不可信 / §4c 网关无 embedding + Ollama 无 key 方案 / `model/codebuddy` provider 用法。 |
| [shell-snapshot-guard.md](shell-snapshot-guard.md) | gvm cd 钩子与 tclaude bash 快照守卫:`_encode/_decode: command not found` 刷屏的根因(grep `^_[^_]` 误丢 gvm 函数)、`CLAUDECODE` 守卫方案(`unset -f cd`)、生效条件与验证。CLAUDE.md 仅留浓缩版指向本文。 |
| [module-organization.md](module-organization.md) | ⭐ Tools/MCP/Skill/Session/Memory 五模块组织方式地图:目录分工 + 核心接口签名 + 横切控制时序(filter→permission→retry→callbacks)+ MCP 两范式对比 + skill 三层信息模型 + session 后端复用 + memory 两模式 + Runner 总装注入与生命周期调用顺序 + 六条模块间组合链。带 file:line,改代码前定位用。 |
| [context-management.md](context-management.md) | ⭐ 上下文管理:一次 LLM 调用里有什么(RequestProcessor 链 + 实测 dump 对照)+ 接近极限怎么裁(Pass 0/1/2 → 同步摘要兜底 → model 层 MiddleOut 三层时序)+ 怎么把上下文结构导出给前端(BeforeModel 回调是真源、OTel、TokenCounter、agui gap 与方案)+ 实测验证(CodeBuddy 网关真实连通 + mock model 完整 tool 循环 + 压缩占位符带 event_id 可恢复)。带 file:line。 |

## 约定

- 这些笔记可能随内部服务演进而过时,引用时注意核对日期。
- **不要把任何真实密钥 / token 写进这里**(仓库会被检索)。示例一律用占位符。
