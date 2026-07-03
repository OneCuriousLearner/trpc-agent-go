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

## 4b. ⚠️ 网关返回的 token usage 不可信(网关协议 bug + 框架累加 bug 叠加)

> 2026-06-30 抓包 + 多模型对照 + 流式深挖实测。**这是采用 CodeBuddy Provider 的一个重大隐患,未来可能因此放弃该 Provider。**
>
> 说明:本节结论经过**两次**修正。初次只测 claude 单点,误判"虚高 68 倍且随轮次放大";第二次用非流式 curl 多模型控制实验,得出"固定加项 ~575";第三次深挖**流式**路径(框架实际走的),才找到真凶——见 §4b-2。三次数据都保留,便于理解排查过程。

**现象**:CodeBuddy 网关对 **Claude 系模型 + 带 `tools`(function calling)** 的请求,会在 `prompt_tokens` 里凭空加上**一个 ~575 token 的固定项**。框架(`model/codebuddy` / `model/openai`)只是**如实透传**网关返回的 usage,本身没有 bug——失真发生在网关侧(疑似 Anthropic 协议转换层把注入的工具说明/系统提示计入了 prompt_tokens)。

**本质:是"固定加项",不是"按比例放大"。**
- **无 tools 时所有模型都准确**(报 6~15 token,符合 ~13 token 估算)。
- 一旦带上同一个极小工具 schema,Claude 系**凭空 +575 左右**,且这是个**常量**——不随对话长度按比例增长,每轮都加这一坨。
- 所以"虚高倍数"随轮次反而**下降**(固定项被真实内容摊薄):claude 单轮 7.2x → 3 轮 3.2x。初次看到的 15675→33852 增长,其实是"固定加项 + 正常的仅追加增长",不是膨胀失控。

**模型族差异巨大(同一网关,同一请求)**:

| 等级 | 模型 | 带 tools 的固定加项 | 单轮失真 |
|------|------|---------------------|---------|
| 🔴 严重 | **Claude 全系**(sonnet-4.6 / haiku-4.5 / opus-4.6) | **+~575**(三型号数字几乎一字不差:588/587/588) | ~7x |
| 🟠 中等 | `deepseek-v3-2-volc` | +~245 | ~4x |
| 🟡 轻微 | `glm-5.0` | +~100 | ~2x |
| 🟢 基本正常 | `kimi-k2.5` / `gpt-5.5` / `gemini-3.1-pro` | +20~45 | <1x |

> Claude 三个型号加项完全相同,强烈暗示是网关在协议转换层统一注入了固定内容并计入 prompt,而非真实计费。
> (注:`claude-opus-4.8` 在该 key/环境下 baseline 即 HTTP 500,模型本身不稳,未纳入;`opus-4.6` 行为与其它 Claude 一致。)

**控制实验数据**(裸 curl,排除框架,`max_tokens=128`,estTok=请求字节/4 的粗估):

```
model                  scenario              estTok  gwPrompt  inflate
claude-sonnet-4.6      A baseline(no tools)      13        15     1.2x
claude-sonnet-4.6      B +tools(1 turn)          82       588     7.2x
claude-sonnet-4.6      C turn 1/2/3            84/172/260  591/718/845  7.0→3.2x
glm-5.0                B +tools(1 turn)          82       182     2.2x
deepseek-v3-2-volc     B +tools(1 turn)          82       327     4.0x
gemini-3.1-pro         B +tools(1 turn)          82        33     0.4x
gpt-5.5                B +tools(1 turn)          82        53     0.6x
```

**影响**:
- **依赖网关 usage 的成本核算 / 配额监控 / token 预算决策会被误导**——尤其 Claude + 工具调用 + 短对话场景失真最重(~7x)。用 gemini/gpt/kimi 则 usage 基本可用。
- token tailoring(`model/token_tailor.go` 按 token 预算裁剪)若用网关 usage 反馈会被骗。所幸框架 tailoring 用的是**本地** `TokenCounter` 估算,不读网关 usage,不受影响。
- 上下文拼接本身没问题:实测请求体证明框架是标准"仅追加"`[system][user][asst][tool]...`,每轮只增 ~370 B(见 §6)。虚高纯粹来自网关计数。

### 4b-2. 更深层根因:流式路径下的"每 chunk 重复 usage × SDK 累加"(2026-06-30 补测,修正上文)

上面的"固定加项 ~575"是用**非流式 curl** 测到的网关侧现象。但框架实际走的是**流式**(CodeBuddy 网关只支持 stream),流式路径下还叠加了一个**框架侧的健壮性 bug**,两者共同造成了 benchmark 里那种天文数字(单步 in-token 报到 **670 万**)。

**根因链(实测坐实)**:
1. **CodeBuddy 违反 OpenAI 流式协议**:标准协议里 usage 只在**最后一个 chunk**出现一次(前面的 chunk `usage=null`);而 CodeBuddy **几乎每个 chunk 都重复携带完整 usage**。实测:一个 20-chunk 的流,**19 个 chunk 都带 `prompt_tokens:562`**(同一值重复 19 次)。DeepSeek 对照:232 chunk 只有 **1 个**带 usage。
2. **OpenAI SDK 累加器无条件 `+=` 每个 chunk 的 usage**:`streamaccumulator.go` 的 `accumulateDelta` 里 `cc.Usage.PromptTokens += chunk.Usage.PromptTokens`(对 Completion/Total 同样)。在标准协议下这没问题(只加末尾那一次);遇到 CodeBuddy 每 chunk 重复 usage,就把 562 累加了 ~19 次。
3. **只在带 tools 时爆发**:实测框架跑 CodeBuddy 流式,`no-tools` prompt=198(✅ 准确),`with-tools` prompt=**6479**(真实应 ~560,虚高 ~11.5x)。带 tools 时 chunk 更多、几乎每个都重复 usage,累加倍数≈chunk 数;多轮时逐次滚雪球到几十万/几百万。两种情况框架最终都只吐 **1 个** usage event,所以虚高发生在 **SDK accumulator 内部**,不是框架吐多次。

**结论修正**:不是"网关数字本身离谱到 670万",单 chunk 的 562 是合理的。真凶是 **CodeBuddy 协议 bug(每 chunk 重复 usage)+ 框架/SDK 对非标准流式不设防(无条件累加)**。这意味着:
- 之前"框架只是如实透传、本身无 bug"的说法**不准确**——框架在流式 usage 累加上对非标准网关**不鲁棒**,这是个**可修复的框架健壮性缺陷**(见 TODO T4)。
- 修复方向:流式累加时对 usage 做"取最后一次 / 去重"而非无条件 `+=`,或识别"每 chunk 重复的相同 usage"并只计一次。
- DeepSeek usage 正确的真正原因是它**遵循标准协议**(usage 仅末尾一次),与流式/非流式无关。

**自查升级版**:除了对比请求体字节,还可直接数流式 chunk:`curl ... -N | grep -c '"prompt_tokens"'`。标准应为 1;若等于 chunk 数,即"每 chunk 重复 usage"型网关,框架累加会虚高。

**原"影响"结论仍成立**:

**怎么自查**:用 `openai.WithChatRequestJSONCallback` dump 真实请求体字节数,跟 `response.Usage.PromptTokens` 对比;或对同一模型做"无 tools vs 带 tools"两次最小请求,看 prompt 是否凭空跳几百。

**结论与取舍**:CodeBuddy 网关在 **Claude + tools** 场景下 usage 严重失真,其它模型族影响递减。短期:**不要用网关 usage 做计量/计费**(尤其 Claude),需要 token 统计就用本地 `TokenCounter` 估算。长期:这是评估是否放弃 CodeBuddy Provider、改走更可信后端(如直连 Venus/太极 OpenAI 兼容端点)的一个重要砝码。

---

## 4c. ⚠️ 网关只有 chat,没有 embedding endpoint(2026-07-03 实测)

CodeBuddy 网关**只提供 `/v2/chat/completions`,不提供 embeddings**。实测:
- `POST /v2/embeddings`、`POST /embeddings` → nginx 层直接 **401**(网关根本不路由这些路径,连应用层都到不了)
- `POST /v2/openapi/embeddings` → **404 Route Not Found**

**影响**:任何需要 embedding/向量检索的能力,在纯 CodeBuddy 环境下都**无法工作**:
- `knowledge`(RAG,建库必须 embedding)、`memory` 的 vector 后端(pgvector/sqlitevec)、`session` recall 的 pgvector 模式。
- 只有 chat/completions 驱动的能力(llmagent 问答、memory 的 inmemory 关键词后端、summary)才能纯靠 CodeBuddy 跑。

**无 key 的替代方案(已验证)**:用**本地 Ollama** 提供 embedding,完全离线免费:
1. 装 ollama(`ollama.com/download/ollama-linux-amd64.tar.zst`,解压到 /usr/local),`ollama serve`
2. `ollama pull nomic-embed-text`(~270MB,768 维)
3. 框架 embedder 用 `knowledge/embedder/ollama`(指 `http://localhost:11434`),或 RAGAS 这类要 OpenAI 协议的用 ollama 的**兼容端点** `http://localhost:11434/v1/embeddings`(实测可用)
4. chat 仍走 CodeBuddy

实测 knowledge benchmark:ollama embedding 建库(702 chunk)+ `/search` 向量检索(命中准确)+ `/answer` 简单问答(CodeBuddy chat)**全通**。回答了"构建 RAG 是否一定要外部 key"——**不需要**,本地 embedding 即可,与本地自建 RAG 无本质区别。

**对 T2 的意义**:网关无 embedding 是继"usage 不可信"之后,评估 CodeBuddy 作为唯一后端的又一减分项——RAG 类能力必须额外配 embedding 来源。

**遗留卡点(见 TODO T6)**:knowledge 的 RAG **agent+tool 循环**(llmagent + search tool)在 CodeBuddy 上会静默卡死(问题打印后无任何后续日志、无网络活动、进程不退)——而不带 tool 的简单 `/answer` 秒回。疑似 tool 调用后的续请求在 stream-only 网关上的又一处兼容问题。

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
