# 工程笔记 (notes)

这里收录开发 / 运维 tRPC-Agent-Go 时积累的**实战 know-how 与踩坑记录**——偏内部经验，不进 mkdocs 发布站（`docs/mkdocs/`），也不算面向用户的正式文档。

> 面向用户的正式文档在 `docs/mkdocs/{en,zh}/`,由 `docs/mkdocs.yml` 组织。

## 索引

| 笔记 | 内容 |
|------|------|
| [TODO.md](TODO.md) | ⭐ 框架优化待办 backlog(对标 Claude Code 等取经)。当前:T1 上下文压缩升级、T2 CodeBuddy 可信度评估、T3 token 累加 helper。随挖掘持续追加。 |
| [codebuddy-gateway.md](codebuddy-gateway.md) | CodeBuddy 内网模型网关接入实战:真实端点 / 必需请求头 / 抓包方法 / 错误码对照 / **§4b 网关 token usage 不可信警告** / `model/codebuddy` provider 用法。绕开 CodeBuddy CLI 与 agent,直连底层模型通道。 |

## 约定

- 这些笔记可能随内部服务演进而过时,引用时注意核对日期。
- **不要把任何真实密钥 / token 写进这里**(仓库会被检索)。示例一律用占位符。
