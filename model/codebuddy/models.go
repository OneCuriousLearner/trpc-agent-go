//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codebuddy

// Known model IDs served by the CodeBuddy gateway, as reported by the CodeBuddy
// CLI. Pass any of these strings to New as the model name. The set of models
// actually reachable depends on the gateway environment and the credential's
// entitlements; treat this list as a convenience reference, not a guarantee.
//
// Naming note: some in-house model IDs carry an "-ioa" suffix (iOA-region
// deployments). On the default "internal" gateway several of those are also
// reachable without the suffix (e.g. both "glm-5.0-ioa" and "glm-5.0" work);
// when in doubt use the exact ID from this list. The "-1m" Claude variants
// expose a 1M-token context window.
const (
	// Anthropic Claude.
	ModelClaudeSonnet46   = "claude-sonnet-4.6"
	ModelClaudeSonnet461M = "claude-sonnet-4.6-1m"
	ModelClaudeOpus48     = "claude-opus-4.8"
	ModelClaudeOpus481M   = "claude-opus-4.8-1m"
	ModelClaudeOpus47     = "claude-opus-4.7"
	ModelClaudeOpus471M   = "claude-opus-4.7-1m"
	ModelClaudeOpus46     = "claude-opus-4.6"
	ModelClaudeOpus461M   = "claude-opus-4.6-1m"
	ModelClaudeHaiku45    = "claude-haiku-4.5"

	// Google Gemini.
	ModelGemini31Pro   = "gemini-3.1-pro"
	ModelGemini35Flash = "gemini-3.5-flash"
	ModelGemini25Pro   = "gemini-2.5-pro"

	// OpenAI GPT.
	ModelGPT55          = "gpt-5.5"
	ModelGPT54          = "gpt-5.4"
	ModelGPT53Codex     = "gpt-5.3-codex"
	ModelGPT51Codex     = "gpt-5.1-codex"
	ModelGPT51CodexMini = "gpt-5.1-codex-mini"

	// Zhipu GLM (iOA deployment IDs).
	ModelGLM52      = "glm-5.2-ioa"
	ModelGLM5VTurbo = "glm-5v-turbo-ioa"
	ModelGLM50      = "glm-5.0-ioa"
	ModelGLM47      = "glm-4.7-ioa"

	// MiniMax (iOA deployment IDs).
	ModelMiniMaxM3  = "minimax-m3-ioa"
	ModelMiniMaxM27 = "minimax-m2.7-ioa"
	ModelMiniMaxM25 = "minimax-m2.5-ioa"

	// Moonshot Kimi (iOA deployment ID).
	ModelKimiK26 = "kimi-k2.6-ioa"

	// Tencent Hunyuan preview (iOA deployment ID).
	ModelHy3Preview = "hy3-preview-agent-ioa"

	// DeepSeek (iOA deployment IDs).
	ModelDeepSeekV4Pro   = "deepseek-v4-pro-ioa"
	ModelDeepSeekV4Flash = "deepseek-v4-flash-ioa"
	ModelDeepSeekV32     = "deepseek-v3-2-volc-ioa"
)
