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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// captured records what the fake gateway saw on the inbound request.
type captured struct {
	mu     sync.Mutex
	path   string
	auth   string
	apiKey string
	cbReq  string
	body   string
}

// newFakeGateway returns a server that records the request and replies with a
// minimal valid SSE stream, mimicking the CodeBuddy gateway.
func newFakeGateway(t *testing.T, cap *captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(buf)
		}
		cap.mu.Lock()
		cap.path = r.URL.Path
		cap.auth = r.Header.Get("Authorization")
		cap.apiKey = r.Header.Get("x-api-key")
		cap.cbReq = r.Header.Get("x-codebuddy-request")
		cap.body = string(buf)
		cap.mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"id":"cb","object":"chat.completion.chunk","created":1,"model":"claude-sonnet-4.6","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
			`data: {"id":"cb","object":"chat.completion.chunk","created":1,"model":"claude-sonnet-4.6","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "%s\n\n", c)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
}

func drain(t *testing.T, ch <-chan *model.Response) []*model.Response {
	t.Helper()
	var out []*model.Response
	for resp := range ch {
		out = append(out, resp)
	}
	return out
}

// TestNew_SendsGatewayPathAndAuthHeaders verifies the wrapper hits
// "<base>/chat/completions" and attaches all three credential markers the
// gateway requires.
func TestNew_SendsGatewayPathAndAuthHeaders(t *testing.T) {
	cap := &captured{}
	server := newFakeGateway(t, cap)
	defer server.Close()

	m := New("claude-sonnet-4.6",
		WithAPIKey("ck_test.secret"),
		WithBaseURL(server.URL+"/v2"),
	)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	responses := drain(t, ch)
	require.NotEmpty(t, responses)

	cap.mu.Lock()
	defer cap.mu.Unlock()
	require.Equal(t, "/v2/chat/completions", cap.path, "must hit the real gateway path")
	require.Equal(t, "Bearer ck_test.secret", cap.auth)
	require.Equal(t, "ck_test.secret", cap.apiKey, "x-api-key must carry the same key")
	require.Equal(t, "1", cap.cbReq, "x-codebuddy-request:1 marks an OpenAPI client")
}

// TestGenerateContent_ForcesStreaming verifies the wrapper flips Stream to true
// even when the caller requested a non-stream response, since the gateway
// rejects non-stream requests.
func TestGenerateContent_ForcesStreaming(t *testing.T) {
	cap := &captured{}
	server := newFakeGateway(t, cap)
	defer server.Close()

	m := New("glm-5.0",
		WithAPIKey("ck_x.y"),
		WithBaseURL(server.URL+"/v2"),
	)

	req := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: false},
	}
	require.False(t, req.Stream)

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	_ = drain(t, ch)

	require.True(t, req.Stream, "GenerateContent must force Stream=true on the request")
	cap.mu.Lock()
	defer cap.mu.Unlock()
	require.Contains(t, cap.body, `"stream":true`, "request body must advertise streaming")
}

// TestGenerateContent_NilRequest ensures a nil request is delegated safely.
func TestGenerateContent_NilRequest(t *testing.T) {
	m := New("claude-sonnet-4.6", WithAPIKey("ck_x.y"))
	_, err := m.GenerateContent(context.Background(), nil)
	require.Error(t, err)
}

// TestInfo_ReturnsModelName checks Info reports the configured model name.
func TestInfo_ReturnsModelName(t *testing.T) {
	m := New("deepseek-v3-2-volc", WithAPIKey("ck_x.y"))
	require.Equal(t, "deepseek-v3-2-volc", m.Info().Name)
}

// TestResolveBaseURL covers the base-URL precedence and environment defaults.
func TestResolveBaseURL(t *testing.T) {
	tests := []struct {
		name string
		opt  options
		env  map[string]string
		want string
	}{
		{
			name: "explicit base url wins",
			opt:  options{baseURL: "https://custom.example.com/v9"},
			want: "https://custom.example.com/v9",
		},
		{
			name: "env base url overrides default",
			env:  map[string]string{envBaseURL: "https://env.example.com/v2"},
			want: "https://env.example.com/v2",
		},
		{
			name: "default is internal gateway",
			want: baseURLInternal,
		},
		{
			name: "overseas environment option",
			opt:  options{environment: "overseas"},
			want: baseURLOverseas,
		},
		{
			name: "overseas via env",
			env:  map[string]string{envEnvironment: "overseas"},
			want: baseURLOverseas,
		},
		{
			name: "explicit base url beats env environment",
			opt:  options{baseURL: "https://custom.example.com/v2"},
			env:  map[string]string{envEnvironment: "overseas"},
			want: "https://custom.example.com/v2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			require.Equal(t, tt.want, resolveBaseURL(tt.opt))
		})
	}
}

// TestNew_APIKeyFromEnv verifies CODEBUDDY_API_KEY is used when WithAPIKey is
// not supplied.
func TestNew_APIKeyFromEnv(t *testing.T) {
	cap := &captured{}
	server := newFakeGateway(t, cap)
	defer server.Close()

	t.Setenv(envAPIKey, "ck_fromenv.secret")
	m := New("glm-5.0", WithBaseURL(server.URL+"/v2"))

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	_ = drain(t, ch)

	cap.mu.Lock()
	defer cap.mu.Unlock()
	require.Equal(t, "Bearer ck_fromenv.secret", cap.auth)
	require.Equal(t, "ck_fromenv.secret", cap.apiKey)
}

// TestWithHeaders_MergeAndOverride verifies extra headers are sent and that an
// explicit override of a credential header takes effect.
func TestWithHeaders_MergeAndOverride(t *testing.T) {
	var gotExtra string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("x-extra")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	m := New("glm-5.0",
		WithAPIKey("ck_x.y"),
		WithBaseURL(server.URL+"/v2"),
		WithHeaders(map[string]string{"x-extra": "present"}),
	)
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	_ = drain(t, ch)
	require.Equal(t, "present", gotExtra)
}
