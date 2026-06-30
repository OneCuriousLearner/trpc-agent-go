//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codebuddy

import (
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// options holds configuration for a CodeBuddy gateway model.
type options struct {
	// apiKey is the ck_ access key. Falls back to CODEBUDDY_API_KEY env.
	apiKey string
	// baseURL overrides the gateway base URL entirely. Falls back to
	// CODEBUDDY_BASE_URL env, then the environment default.
	baseURL string
	// environment selects the default base URL when baseURL is unset.
	// One of "internal" (default) or "overseas". Falls back to
	// CODEBUDDY_INTERNET_ENVIRONMENT env.
	environment string
	// headers are extra request headers merged on top of the required
	// gateway credential headers.
	headers map[string]string
	// openAIOptions are passthrough options forwarded to the underlying
	// openai.Model (e.g. WithChannelBufferSize, callbacks, token tailoring).
	openAIOptions []openai.Option
}

// Option configures a CodeBuddy gateway model.
type Option func(*options)

// WithAPIKey sets the ck_ access key. When unset, CODEBUDDY_API_KEY is used.
func WithAPIKey(key string) Option {
	return func(o *options) {
		o.apiKey = key
	}
}

// WithBaseURL overrides the gateway base URL entirely. The OpenAI SDK appends
// "/chat/completions", so pass the base up to and including the version
// segment, e.g. "https://copilot.tencent.com/v2".
//
// Use this for ioa / private deployments whose domains are deployment-specific.
func WithBaseURL(url string) Option {
	return func(o *options) {
		o.baseURL = url
	}
}

// WithEnvironment selects the default gateway when no base URL is supplied.
// Accepts "internal" (China region, default) or "overseas" (international).
func WithEnvironment(env string) Option {
	return func(o *options) {
		o.environment = env
	}
}

// WithHeaders adds extra request headers. They are merged on top of (and can
// override) the required gateway credential headers.
func WithHeaders(headers map[string]string) Option {
	return func(o *options) {
		if o.headers == nil {
			o.headers = make(map[string]string, len(headers))
		}
		for k, v := range headers {
			o.headers[k] = v
		}
	}
}

// WithOpenAIOptions forwards raw options to the underlying openai.Model,
// enabling reuse of the full openai provider feature set (callbacks, channel
// buffer size, token tailoring, etc.).
func WithOpenAIOptions(opts ...openai.Option) Option {
	return func(o *options) {
		o.openAIOptions = append(o.openAIOptions, opts...)
	}
}
