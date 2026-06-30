# CodeBuddy Direct (HTTP gateway, no CLI)

This example talks to the CodeBuddy in-house model gateway
(`copilot.tencent.com`) **directly** through the framework's `model/codebuddy`
provider — bypassing the CodeBuddy CLI and its agent layer entirely.

The gateway speaks the standard OpenAI Chat Completions protocol. The provider
is a thin wrapper over `model/openai` that preconfigures:

- the gateway endpoint (`https://copilot.tencent.com/v2` → `/v2/chat/completions`),
- the two extra auth headers the gateway requires (`x-api-key` + `x-codebuddy-request: 1`),
- forced streaming (the gateway rejects non-stream requests).

## Credential

Use a CodeBuddy CLI access key (prefix `ck_`), created at
`https://tencent.sso.copilot.tencent.com/profile/keys`.

```bash
export CODEBUDDY_API_KEY="ck_..."
```

Optional environment overrides:

| Variable | Meaning | Default |
| --- | --- | --- |
| `CODEBUDDY_API_KEY` | the `ck_` access key | (required) |
| `CODEBUDDY_BASE_URL` | override the gateway base URL entirely | — |
| `CODEBUDDY_INTERNET_ENVIRONMENT` | `internal` (China) / `overseas` | `internal` |

## Run

```bash
cd examples/codebuddydirect

# Plain chat
go run . -model claude-sonnet-4.6 "用一句话介绍 tRPC-Go"

# Tool calling (the model drives the calculator function tool)
go run . -model claude-sonnet-4.6 "用计算器算 23 乘以 17，再把结果加 100"
```

## Available models

The full set reported by the CodeBuddy CLI (pass via `-model`, or reference the
exported `codebuddy.Model*` constants in code):

- **Claude**: `claude-sonnet-4.6`, `claude-sonnet-4.6-1m`, `claude-opus-4.8(-1m)`,
  `claude-opus-4.7(-1m)`, `claude-opus-4.6(-1m)`, `claude-haiku-4.5`
- **Gemini**: `gemini-3.1-pro`, `gemini-3.5-flash`, `gemini-2.5-pro`
- **GPT**: `gpt-5.5`, `gpt-5.4`, `gpt-5.3-codex`, `gpt-5.1-codex`, `gpt-5.1-codex-mini`
- **GLM**: `glm-5.2-ioa`, `glm-5v-turbo-ioa`, `glm-5.0-ioa`, `glm-4.7-ioa`
- **MiniMax**: `minimax-m3-ioa`, `minimax-m2.7-ioa`, `minimax-m2.5-ioa`
- **Kimi**: `kimi-k2.6-ioa`
- **Hunyuan**: `hy3-preview-agent-ioa`
- **DeepSeek**: `deepseek-v4-pro-ioa`, `deepseek-v4-flash-ioa`, `deepseek-v3-2-volc-ioa`

The `-ioa` suffix marks iOA-region deployment IDs. On the default `internal`
gateway some of those are also reachable without the suffix (e.g. both
`glm-5.0-ioa` and `glm-5.0` work). Reachability depends on the gateway
environment and the credential's entitlements. An unknown name returns
`11102 model service info not found`.

## Using the provider in your own code

```go
import "trpc.group/trpc-go/trpc-agent-go/model/codebuddy"

m := codebuddy.New("claude-sonnet-4.6",
    codebuddy.WithAPIKey(os.Getenv("CODEBUDDY_API_KEY")))
```

Or via the unified provider factory:

```go
import "trpc.group/trpc-go/trpc-agent-go/model/provider"

m, _ := provider.Model("codebuddy", "claude-sonnet-4.6",
    provider.WithAPIKey(os.Getenv("CODEBUDDY_API_KEY")))
```
