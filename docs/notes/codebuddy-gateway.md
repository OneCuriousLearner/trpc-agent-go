# CodeBuddy 内网模型网关接入实战

> 最后更新:2026-06-30 · 基于 CodeBuddy CLI v2.113.0 / 网关 `copilot.tencent.com` 抓包验证。
> 内部服务会演进,引用前请核对当前行为。

## 这篇解决什么

你手上只有一个 CodeBuddy 的 **CLI 访问密钥**(`ck_` 前缀),想让 tRPC-Agent-Go
直接发对话请求,**绕开 CodeBuddy CLI 和它的 agent 体系**,只复用底层模型通道。

结论:能做到,且框架已内置 `model/codebuddy` provider。下面是来龙去脉与一手数据。

---

## 1. 关键认知:`ck_` 是 CLI 密钥,不是 OpenAPI 应用凭据

CodeBuddy 有两类完全不同的凭据,**容易混淆**:

| 凭据 | 形态 | 用途 | 创建入口 |
|------|------|------|----------|
| **访问密钥 (Access Key)** | `ck_<id>.<secret>` | CodeBuddy **CLI** 登录(env `CODEBUDDY_API_KEY`) | `https://tencent.sso.copilot.tencent.com/profile/keys` |
| OpenAPI 应用凭据 | `client_id` + `client_secret` | OpenAPI / OAuth 应用集成(Background Agent 等) | `.../profile/apps` |

官方 usercenter 文案原文:"生成的 API 密钥为 **CodeBuddyCLI** 提供安全访问"。

> 因此,拿 `ck_` 去直连那些要求 OpenAPI 应用身份的端点会失败(见 §4 的 `11101`)。
> 但 `ck_` 在底层就是一个 Bearer 凭据,可直接打模型网关——只要带对头。

---

## 2. 真实调用方式 (ground truth)

通过对 CLI 做本地 TLS 终止 MITM 抓包(方法见 §5)得到的真实请求:

```
POST https://copilot.tencent.com/v2/chat/completions
Authorization: Bearer ck_xxxxxxxx.xxxxxxxx
x-api-key:      ck_xxxxxxxx.xxxxxxxx        # 同一个 ck_ key 再放一份
x-codebuddy-request: 1                      # 标识 "OpenAPI client",最易漏
Content-Type: application/json
```

请求体是**标准 OpenAI Chat Completions** 格式。

**三个硬性要点:**

1. **路径是 `/v2/chat/completions`**——不是文档里出现的 `/v2/openapi/chat/completions`(那是另一套要 OpenAPI 应用凭据的端点)。
2. **三个认证 / 标识头缺一不可**:`Authorization: Bearer`、`x-api-key`、`x-codebuddy-request: 1`。只带 Bearer 会被判为"非 OpenAPI client"。
3. **必须流式** `"stream": true`。非流式直接被拒:`Non-stream chat request is currently not supported`。

其余业务头(`x-product`、`x-ide-type`、`x-agent-intent` 等)CLI 会带,但**非必需**,精简掉仍成功。

### 环境与 host

| 环境 (`CODEBUDDY_INTERNET_ENVIRONMENT`) | Host | 验证状态 |
|------|------|----------|
| `internal`(中国版,默认) | `copilot.tencent.com` | ✅ 已验证 |
| `overseas`(海外版) | `www.codebuddy.ai` | 二进制可见,未实测 |
| `ioa`(iOA 内网版) | 部署相关,需显式配 base_url | 未验证 |

CLI 由 connect 代理抓到 `internal` 模式连的就是 `copilot.tencent.com:443`。

### 最小 curl 复现

```bash
CK="ck_xxxxxxxx.xxxxxxxx"   # 你的访问密钥,勿提交到仓库
curl -sS -N "https://copilot.tencent.com/v2/chat/completions" \
  -H "Authorization: Bearer $CK" \
  -H "x-api-key: $CK" \
  -H "x-codebuddy-request: 1" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{"model":"claude-sonnet-4.6","stream":true,
       "messages":[{"role":"user","content":"say hi"}]}'
```

---

## 3. 可用模型 (实测于 internal 环境)

CLI 报告的完整清单(`model/codebuddy/models.go` 里有导出常量):

- **Claude**: `claude-sonnet-4.6`、`claude-sonnet-4.6-1m`、`claude-opus-4.8(-1m)`、`claude-opus-4.7(-1m)`、`claude-opus-4.6(-1m)`、`claude-haiku-4.5`
- **Gemini**: `gemini-3.1-pro`、`gemini-3.5-flash`、`gemini-2.5-pro`
- **GPT**: `gpt-5.5`、`gpt-5.4`、`gpt-5.3-codex`、`gpt-5.1-codex`、`gpt-5.1-codex-mini`
- **GLM**: `glm-5.2-ioa`、`glm-5v-turbo-ioa`、`glm-5.0-ioa`、`glm-4.7-ioa`
- **MiniMax**: `minimax-m3-ioa`、`minimax-m2.7-ioa`、`minimax-m2.5-ioa`
- **Kimi**: `kimi-k2.6-ioa`
- **Hunyuan**: `hy3-preview-agent-ioa`
- **DeepSeek**: `deepseek-v4-pro-ioa`、`deepseek-v4-flash-ioa`、`deepseek-v3-2-volc-ioa`

注意:

- **`claude-sonnet-4.6` 用点号,不是 `4-6`**。错写连字符会报 `11102 model service info not found`。
- **`-ioa` 后缀**标记 iOA 部署 ID。在 `internal` 网关上,部分模型**带不带后缀都能用**(实测 `glm-5.0-ioa` 和 `glm-5.0` 均通,`deepseek-v3-2-volc-ioa` 和 `deepseek-v3-2-volc` 均通)。不确定时用清单里的精确 ID。
- `-1m` 变体是 100 万 token 上下文窗口。
- 能否访问取决于网关环境和密钥额度。

---

## 4. 错误码对照 (踩坑记录)

抓包 / 直连过程中遇到的网关错误码:

| code | msg | 真实含义 | 解法 |
|------|-----|----------|------|
| `11101` | `unauthorized: request is not from an OpenAPI client` | **缺 `x-codebuddy-request: 1` 头**(或路径用了 `/v2/openapi/...` 要求应用凭据) | 补头 + 用 `/v2/chat/completions` |
| `11102` | `model [...] service info not found` | 模型名拼错 / 该环境无此模型 | 用 §3 的精确 ID |
| `11127` | `messages length must be at least 2` | **不是模型问题**——部分模型(如 opus)要求 ≥2 条消息 | system + user 两条 |
| `11133` | `Invalid request parameters` (`max_output_tokens` ...) | **不是模型问题**——`max_tokens` 太小触发约束 | 给合理的 `max_tokens`(如 ≥64) |
| `Non-stream chat request is currently not supported` | — | 用了非流式 | `"stream": true` |

> 教训:`11127`/`11133` 第一眼像"模型不可用",其实是请求参数约束。用最小 payload(如 `max_tokens:8`、单条 message)探测模型可用性会误判——要用合理 payload 复测。

另一个易踩的死路:直连 `https://www.codebuddy.cn/v2/openapi/chat/completions`
用 `Bearer ck_` → `11101`。那个端点要 OpenAPI 应用凭据(`client_id`+`client_secret`),
和 `ck_` 是两套东西。**正确路径是 `copilot.tencent.com/v2/chat/completions`**。

---

## 5. 抓包方法 (如何重抓其它环境的端点)

CodeBuddy CLI(`codebuddy`)是 node 打包的单 ELF。要拿到它真实发出的请求:

**关键性质:**

- CLI 读 `HTTPS_PROXY` / `https_proxy`,且信任 `NODE_EXTRA_CA_CERTS` 指定的 CA。
- 本环境 `strace` 抓不到外连(疑似 io_uring),所以用代理而非 strace。

**两步抓法:**

1. **抓 host**(明文 CONNECT 阶段):起一个 CONNECT 代理,设 `HTTPS_PROXY=http://127.0.0.1:<port>`,跑 `codebuddy -p "hi" --output-format text`。代理日志里能看到 `CONNECT copilot.tencent.com:443`。

2. **抓完整 URL + 请求头 + body**(需 TLS 终止):
   ```bash
   # 自签 CA + 给目标域签叶子证书
   openssl genrsa -out ca.key 2048
   openssl req -x509 -new -nodes -key ca.key -sha256 -days 3 -out ca.crt -subj "/CN=mitm-ca"
   # ...给 copilot.tencent.com 签 leaf 证书(SAN=DNS:copilot.tencent.com)...
   ```
   起一个 TLS 终止 MITM 代理(接 CONNECT → 用 leaf 证书对客户端伪装 → 解明文记录 → 再 TLS 转发上游),然后:
   ```bash
   export HTTPS_PROXY=http://127.0.0.1:<port>
   export NODE_EXTRA_CA_CERTS=/path/to/ca.crt
   export CODEBUDDY_API_KEY=ck_...
   export CODEBUDDY_INTERNET_ENVIRONMENT=internal
   codebuddy -p "hi" --output-format text --permission-mode bypassPermissions
   ```
   即可看到真实的 `POST /v2/chat/completions` 行 + 全部头 + body。

> 还有个更轻的法子探"路径后缀":设 `CODEBUDDY_BASE_URL=http://127.0.0.1:<port>` 指向一个明文 HTTP 捕获器,CLI 会把 `<base>/chat/completions` 打过来——能看到它在 base 后只拼 `/chat/completions`,从而推断 internal 默认 base 是 `.../v2`。
>
> 抓完记得清理临时进程、自签证书、CLI 调试日志(如 `examples/claudecode/log/`)。

---

## 6. 在 tRPC-Agent-Go 里用 (model/codebuddy)

已固化为正式 provider,与 openai / anthropic / hunyuan 并列。它是 `model/openai`
的薄封装:预设网关 base_url、注入两个必需头、强制 `stream=true`。

```go
import "trpc.group/trpc-go/trpc-agent-go/model/codebuddy"

m := codebuddy.New("claude-sonnet-4.6",
    codebuddy.WithAPIKey(os.Getenv("CODEBUDDY_API_KEY")))
```

或经统一工厂:

```go
import "trpc.group/trpc-go/trpc-agent-go/model/provider"

m, _ := provider.Model("codebuddy", "claude-sonnet-4.6",
    provider.WithAPIKey(os.Getenv("CODEBUDDY_API_KEY")))
```

**环境变量**(`codebuddy.New` 会读):

| 变量 | 含义 | 默认 |
|------|------|------|
| `CODEBUDDY_API_KEY` | `ck_` 访问密钥 | (必需) |
| `CODEBUDDY_BASE_URL` | 完全覆盖网关 base URL(ioa / 私有部署用) | — |
| `CODEBUDDY_INTERNET_ENVIRONMENT` | `internal` / `overseas` | `internal` |

可运行 demo:`examples/codebuddydirect/`(含多轮**工具调用**示例,已实测
`claude-sonnet-4.6`、`glm-5.0` 连续调用 calculator 成功)。

### 设计要点

- CodeBuddy 网关是纯 OpenAI 兼容,所以**不重复造轮子**——内部委托配置好的
  `openai.Model`,只补三处差异(base_url / 两个头 / 强制 stream)。
- `internal`/`overseas` 用默认域名;ioa / 私有部署因域名随部署而异,要求显式传 base_url(不瞎猜)。
- `model/openai` 的 `WithHeaders(map)` 逐个走 `openaiopt.WithHeader` 注入每请求;
  `WithBaseURL("https://copilot.tencent.com/v2")` 后 SDK 自动拼 `/chat/completions`。

---

## 附:claudecode agent 接 CodeBuddy CLI 的兼容 bug

如果改走"CLI 子进程"方式(`agent/claudecode` + `-claude-bin codebuddy`),有个坑:

`agent/claudecode` 的 `runWithSession` 是 **resume-first**:先 `--resume <id>`,
**仅当进程退出码非 0** 才 fallback 到 `--session-id` 创建新会话。但 CodeBuddy CLI
在 `--resume` 一个**不存在**的 session 时退出码仍是 `0`(原版 Claude Code 是非 0),
导致 agent 误以为 resume 成功,把 `No conversation found...` 当结果返回,**永不创建会话**。

要支持 CodeBuddy CLI,需让 fallback 判定不只看退出码,还要看 stdout 是否含
`No conversation found` 之类标志。——不过既然有 §6 的纯 HTTP 直连,通常没必要走 CLI。
