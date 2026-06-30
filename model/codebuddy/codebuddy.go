//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codebuddy provides a model implementation that talks to the CodeBuddy
// in-house model gateway (the same backend the CodeBuddy CLI uses).
//
// The gateway speaks the standard OpenAI Chat Completions protocol, so this
// package is a thin wrapper around the openai provider: it preconfigures the
// gateway base URL, injects the two extra authentication headers the gateway
// requires, and forces streaming (the gateway rejects non-stream requests).
//
// Ground truth captured from real CLI traffic:
//
//	POST https://copilot.tencent.com/v2/chat/completions   (stream only)
//	Authorization: Bearer <ck_ key>
//	x-api-key: <ck_ key>
//	x-codebuddy-request: 1
//
// The credential is a CodeBuddy CLI "access key" (prefix "ck_"); here it is
// reused purely as a bearer credential against the underlying gateway.
//
// Basic usage:
//
//	m := codebuddy.New("claude-sonnet-4.6",
//	    codebuddy.WithAPIKey(os.Getenv("CODEBUDDY_API_KEY")))
//	rsp, err := m.GenerateContent(ctx, req)
//
// Credentials and endpoint can also be supplied via environment variables:
//
//	CODEBUDDY_API_KEY              the ck_ access key (required if WithAPIKey unset)
//	CODEBUDDY_BASE_URL            overrides the gateway base URL entirely
//	CODEBUDDY_INTERNET_ENVIRONMENT  internal (default) | overseas; selects the
//	                              default base URL when CODEBUDDY_BASE_URL is unset
package codebuddy

import (
	"context"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

const (
	// envAPIKey is the environment variable holding the ck_ access key.
	//nolint:gosec // G101: this is the env var name, not a credential.
	envAPIKey = "CODEBUDDY_API_KEY"
	// envBaseURL overrides the gateway base URL entirely.
	envBaseURL = "CODEBUDDY_BASE_URL"
	// envEnvironment selects the default base URL (internal/overseas).
	envEnvironment = "CODEBUDDY_INTERNET_ENVIRONMENT"

	// headerAPIKey is the secondary credential header the gateway requires in
	// addition to the standard Authorization: Bearer header.
	headerAPIKey = "x-api-key"
	// headerCodeBuddyRequest marks the request as coming from an "OpenAPI
	// client"; the gateway returns 11101 (unauthorized) without it.
	headerCodeBuddyRequest = "x-codebuddy-request"

	// environmentInternal is the China-region gateway (default).
	environmentInternal = "internal"
	// environmentOverseas is the international gateway.
	environmentOverseas = "overseas"

	// baseURLInternal is the verified China-region gateway base URL.
	// The OpenAI SDK appends /chat/completions, yielding the real endpoint
	// https://copilot.tencent.com/v2/chat/completions.
	baseURLInternal = "https://copilot.tencent.com/v2"
	// baseURLOverseas is the international gateway base URL.
	baseURLOverseas = "https://www.codebuddy.ai/v2"
)

// Model wraps an openai.Model preconfigured for the CodeBuddy gateway.
//
// It implements model.Model by delegating to the embedded openai.Model, while
// forcing every request into streaming mode because the gateway only supports
// stream responses.
type Model struct {
	delegate *openai.Model
	name     string
}

// New creates a CodeBuddy gateway model for the given model name (e.g.
// "claude-sonnet-4.6", "glm-5.0", "deepseek-v3-2-volc").
func New(name string, opts ...Option) *Model {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}

	apiKey := o.apiKey
	if apiKey == "" {
		apiKey = os.Getenv(envAPIKey)
	}

	baseURL := resolveBaseURL(o)

	// Build the openai options: gateway base URL, both credential headers, and
	// any caller-supplied extra headers / passthrough openai options.
	headers := map[string]string{
		headerAPIKey:           apiKey,
		headerCodeBuddyRequest: "1",
	}
	for k, v := range o.headers {
		headers[k] = v
	}

	oaOpts := []openai.Option{
		openai.WithAPIKey(apiKey),
		openai.WithBaseURL(baseURL),
		openai.WithHeaders(headers),
	}
	oaOpts = append(oaOpts, o.openAIOptions...)

	return &Model{
		delegate: openai.New(name, oaOpts...),
		name:     name,
	}
}

// resolveBaseURL determines the gateway base URL using, in priority order:
// explicit WithBaseURL, CODEBUDDY_BASE_URL, then the environment default.
func resolveBaseURL(o options) string {
	if o.baseURL != "" {
		return o.baseURL
	}
	if env := strings.TrimSpace(os.Getenv(envBaseURL)); env != "" {
		return env
	}
	env := o.environment
	if env == "" {
		env = strings.TrimSpace(os.Getenv(envEnvironment))
	}
	switch strings.ToLower(env) {
	case environmentOverseas:
		return baseURLOverseas
	default:
		// internal is the default; ioa/private deployments must supply an
		// explicit base URL since their domains are deployment-specific.
		return baseURLInternal
	}
}

// GenerateContent implements model.Model. It forces streaming because the
// gateway rejects non-stream requests, then delegates to the openai model.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request != nil {
		request.Stream = true
	}
	return m.delegate.GenerateContent(ctx, request)
}

// Info implements model.Model.
func (m *Model) Info() model.Info {
	return m.delegate.Info()
}
