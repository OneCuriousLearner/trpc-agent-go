# 工程笔记 (notes)

这里收录开发 / 运维 tRPC-Agent-Go 时积累的**实战 know-how 与踩坑记录**——偏内部经验，不进 mkdocs 发布站（`docs/mkdocs/`），也不算面向用户的正式文档。

> 面向用户的正式文档在 `docs/mkdocs/{en,zh}/`,由 `docs/mkdocs.yml` 组织。

## 索引

| 笔记 | 内容 |
|------|------|
| [codebuddy-gateway.md](codebuddy-gateway.md) | CodeBuddy 内网模型网关接入实战:真实端点 / 必需请求头 / 抓包方法 / 错误码对照 / `model/codebuddy` provider 用法。绕开 CodeBuddy CLI 与 agent,直连底层模型通道。 |

## 约定

- 这些笔记可能随内部服务演进而过时,引用时注意核对日期。
- **不要把任何真实密钥 / token 写进这里**(仓库会被检索)。示例一律用占位符。
