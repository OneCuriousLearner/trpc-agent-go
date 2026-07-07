//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requiredSectionTitles are the nine section titles the detailed continuity
// prompt must surface to the model.
var requiredSectionTitles = []string{
	"Primary Request and Intent",
	"Key Technical Concepts and Decisions",
	"Files, Code, and Tool Calls",
	"Errors and Fixes",
	"Key Facts and Constraints",
	"All User Messages",
	"Pending Tasks",
	"Current Work",
	"On-Demand Recovery",
}

func TestDetailedContinuityPrompt_ContainsRequiredSections(t *testing.T) {
	got := detailedContinuityPrompt(0)
	for _, title := range requiredSectionTitles {
		assert.Contains(t, got, title, "detailed prompt should list section: %s", title)
	}
	assert.Contains(t, got, conversationTextPlaceholder,
		"standard variant must carry the {conversation_text} placeholder")
	assert.NotContains(t, got, maxSummaryWordsPlaceholder,
		"word placeholder must be absent when maxWords == 0")
	assert.True(t, strings.HasSuffix(got, "Summary:"),
		"prompt should end with the Summary: continuation marker")
}

func TestDetailedContinuityPrompt_IncludesWordPlaceholderWhenMaxWordsSet(t *testing.T) {
	got := detailedContinuityPrompt(200)
	assert.Contains(t, got, maxSummaryWordsPlaceholder)
}

func TestDetailedContinuityCacheSafeForkPrompt_NoConversationPlaceholder(t *testing.T) {
	got := detailedContinuityCacheSafeForkPrompt(0)
	assert.NotContains(t, got, conversationTextPlaceholder,
		"cache-safe fork variant must NOT carry the {conversation_text} placeholder "+
			"(the conversation comes from the parent request)")
	for _, title := range requiredSectionTitles {
		assert.Contains(t, got, title, "fork variant should list section: %s", title)
	}
	// Cache-safe fork constraints must be preserved.
	assert.Contains(t, got, "Do not call tools")
	assert.Contains(t, got, "Do not answer the latest user request")
}

func TestWithDetailedContinuityPrompt_OverridesBothPaths(t *testing.T) {
	s := NewSummarizer(&fakeModel{}, WithDetailedContinuityPrompt()).(*sessionSummarizer)

	assert.Equal(t, detailedContinuityPrompt(0), s.prompt)
	assert.Equal(t, detailedContinuityCacheSafeForkPrompt(0), s.cacheSafeForkPrompt)
	assert.True(t, s.useDetailedPrompt)
}

func TestWithDetailedContinuityPrompt_RespectsMaxSummaryWordsAnyOrder(t *testing.T) {
	t.Run("detailed before maxwords", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{},
			WithDetailedContinuityPrompt(),
			WithMaxSummaryWords(200),
		).(*sessionSummarizer)
		assert.Contains(t, s.prompt, maxSummaryWordsPlaceholder)
		assert.Contains(t, s.cacheSafeForkPrompt, maxSummaryWordsPlaceholder)
	})

	t.Run("maxwords before detailed", func(t *testing.T) {
		s := NewSummarizer(&fakeModel{},
			WithMaxSummaryWords(200),
			WithDetailedContinuityPrompt(),
		).(*sessionSummarizer)
		assert.Contains(t, s.prompt, maxSummaryWordsPlaceholder)
		assert.Contains(t, s.cacheSafeForkPrompt, maxSummaryWordsPlaceholder)
	})
}

func TestWithDetailedContinuityPrompt_DefaultUnchanged(t *testing.T) {
	s := NewSummarizer(&fakeModel{}).(*sessionSummarizer)
	assert.Equal(t, getDefaultSummarizerPrompt(0), s.prompt,
		"without the option the default prompt must be used")
	assert.Equal(t, getDefaultCacheSafeForkPrompt(0), s.cacheSafeForkPrompt)
	assert.False(t, s.useDetailedPrompt)
}

func TestWithDetailedContinuityPrompt_ExplicitWithPromptTakesPrecedence(t *testing.T) {
	const custom = "Conversation:\n{conversation_text}\n\nSummary:"
	s := NewSummarizer(&fakeModel{},
		WithDetailedContinuityPrompt(),
		WithPrompt(custom),
	).(*sessionSummarizer)
	assert.Equal(t, custom, s.prompt,
		"an explicit WithPrompt must win over the detailed variant on the standard path")
	// The fork path is not overridden by WithPrompt, so detailed still applies there.
	assert.Equal(t, detailedContinuityCacheSafeForkPrompt(0), s.cacheSafeForkPrompt)
}

// TestWithDetailedContinuityPrompt_RendersWithoutError is a smoke test that
// the detailed prompt, once wired through the summarizer, renders correctly
// via the existing buildSummaryRequest path (no new rendering logic added).
func TestWithDetailedContinuityPrompt_RendersWithoutError(t *testing.T) {
	s := NewSummarizer(&fakeModel{},
		WithDetailedContinuityPrompt(),
		WithMaxSummaryWords(100),
	).(*sessionSummarizer)

	req, err := s.buildSummaryRequest(t.Context(), "user said this, assistant did that")
	require.NoError(t, err)
	require.NotNil(t, req)
	require.NotEmpty(t, req.Messages)
	// The rendered user prompt must contain the conversation text and a section title.
	joined := req.Messages[len(req.Messages)-1].Content
	assert.Contains(t, joined, "user said this, assistant did that")
	assert.Contains(t, joined, "Primary Request and Intent")
	assert.NotContains(t, joined, conversationTextPlaceholder,
		"placeholder must be rendered away")
}
