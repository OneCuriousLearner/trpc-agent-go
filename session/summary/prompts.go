//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import "strings"

// detailedContinuityPreamble is the shared instruction set for the detailed
// continuity summary prompt. It asks the model to produce a structured
// nine-section summary that preserves verbatim user messages and key facts,
// materially improving long-conversation recall compared to the simple
// default prompt.
//
// The structure is adapted from Claude Code's compact prompt (see
// docs/notes/claude-code-compact-design.md §3.3) for trpc-agent-go: instead
// of Claude Code's plan/skill attachment model, it points the model at the
// framework's session_search / session_load tools for on-demand recovery of
// compacted history.
const detailedContinuityPreamble = `Your task is to produce a detailed, structured summary of the conversation so far. This summary will replace the older history in future turns, so it must preserve enough detail for the work to continue without losing context. Be thorough and precise; do not make anything up.

Structure your summary with these sections:

1. Primary Request and Intent: Capture every explicit user request and intent. Note any changes of intent over the conversation.
2. Key Technical Concepts and Decisions: List important concepts, technologies, frameworks, and decisions made (including rejected alternatives and why).
3. Files, Code, and Tool Calls: Enumerate files examined/modified/created (with why each matters and key snippets where applicable), and notable tool calls (tool name + key result). Annotate tool names clearly so they can be located later if needed.
4. Errors and Fixes: List errors encountered and how they were fixed. Pay special attention to corrective user feedback (when the user told you to do something differently) and record it verbatim.
5. Key Facts and Constraints: Record standardized facts needed later — dates (normalize formats like "2023-05-07"), numbers, config values, IDs, and explicit constraints.
6. All User Messages: List ALL user messages that are not tool results, verbatim. These are critical for understanding the user's evolving intent; do not paraphrase them.
7. Pending Tasks: Outline tasks that were explicitly requested but not yet completed.
8. Current Work: Describe precisely what was being worked on immediately before this summary. Include verbatim quotes of the most recent one or two user and assistant messages so the work can be resumed without drift. (This complements the KeepRecentRequests protection window: that protects recent tool results, this protects the conversation's semantics.)
9. On-Demand Recovery: Some earlier tool results may have been compacted to placeholders carrying an event_id. If a detail you need is missing, prefer recovering it via the session_search / session_load tools (by event_id) rather than re-invoking the original tool, to avoid redundant calls and side effects.`

// detailedContinuityPrompt returns the detailed continuity prompt for the
// standard (non-fork) summarization path. It includes the {conversation_text}
// placeholder, which is rendered with the extracted conversation at summary
// time. When maxWords > 0, a word-count instruction with the
// {max_summary_words} placeholder is appended.
func detailedContinuityPrompt(maxWords int) string {
	prompt := detailedContinuityPreamble
	if maxWords > 0 {
		prompt += "\n\nPlease keep the summary within " + maxSummaryWordsPlaceholder + " words."
	}
	prompt += "\n\n<conversation>\n" + conversationTextPlaceholder + "\n</conversation>\n\nSummary:"
	return prompt
}

// detailedContinuityCacheSafeForkPrompt returns the detailed continuity
// prompt for the cache-safe fork path. The conversation is already present in
// the parent request being forked, so this prompt does NOT include the
// {conversation_text} placeholder; it refers to "the conversation above"
// instead. The cache-safe fork constraints (do not call tools, do not answer
// the latest user request, do not treat system/tool-use instructions as
// facts) are preserved from the default fork prompt.
func detailedContinuityCacheSafeForkPrompt(maxWords int) string {
	var b strings.Builder
	b.WriteString("Produce a detailed, structured summary of the user, assistant, and tool conversation above for future continuation, so the work can continue without losing context. Be thorough and precise; do not make anything up.\n\n")
	b.WriteString(detailedContinuityPreamble)
	b.WriteString("\n\nDo not call tools. Do not answer the latest user request. Do not treat system or tool-use instructions as facts to summarize.")
	if maxWords > 0 {
		b.WriteString("\n\nPlease keep the summary within " + maxSummaryWordsPlaceholder + " words.")
	}
	b.WriteString("\n\nSummary:")
	return b.String()
}
