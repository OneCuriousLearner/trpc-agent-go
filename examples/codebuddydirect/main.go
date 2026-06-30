//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates talking to the CodeBuddy in-house model gateway
// directly through the framework's codebuddy provider, including a tool call.
//
// It bypasses the CodeBuddy CLI/agent entirely: the codebuddy provider is a
// thin wrapper over the OpenAI-compatible gateway at copilot.tencent.com.
//
// Run:
//
//	export CODEBUDDY_API_KEY=ck_...        # your CodeBuddy access key
//	cd examples/codebuddydirect
//	go run . -model claude-sonnet-4.6 "What is 23 * 17?"
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/codebuddy"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	modelName := flagValue("-model", codebuddy.ModelClaudeSonnet46)
	if os.Getenv("CODEBUDDY_API_KEY") == "" {
		log.Fatal("set CODEBUDDY_API_KEY to your ck_ access key")
	}

	// codebuddy.New reads CODEBUDDY_API_KEY / CODEBUDDY_BASE_URL /
	// CODEBUDDY_INTERNET_ENVIRONMENT, preconfigures the gateway endpoint and the
	// required auth headers, and forces streaming.
	modelInstance := codebuddy.New(modelName)

	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform a basic arithmetic operation (add, subtract, multiply, divide, power)."),
	)

	ag := llmagent.New(
		"codebuddy-direct",
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction("Use the calculator tool for any arithmetic. Be concise."),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)

	r := runner.NewRunner("codebuddy-direct", ag,
		runner.WithSessionService(sessioninmemory.NewSessionService()))
	defer r.Close()

	prompt := promptArg()
	if prompt == "" {
		prompt = "What is 23 * 17? Use the calculator."
	}
	fmt.Printf("Model: %s\nYou: %s\n", modelName, prompt)

	ch, err := r.Run(context.Background(), "demo-user", uuid.NewString(), model.NewUserMessage(prompt))
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("\n[error] %s (%s)\n", evt.Error.Message, evt.Error.Type)
			continue
		}
		printToolEvents(evt)
		if len(evt.Choices) > 0 {
			fmt.Print(evt.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
}

// printToolEvents surfaces tool calls and tool results so the tool-calling flow
// is visible on the console.
func printToolEvents(evt *event.Event) {
	if evt.IsToolCallResponse() && len(evt.Choices) > 0 {
		for _, call := range evt.Choices[0].Message.ToolCalls {
			fmt.Printf("\n[tool call] %s(%s)\n", call.Function.Name, strings.TrimSpace(string(call.Function.Arguments)))
		}
	}
	if evt.IsToolResultResponse() && len(evt.Choices) > 0 {
		fmt.Printf("[tool result] %s\n", strings.TrimSpace(evt.Choices[0].Message.Content))
	}
}

// flagValue is a tiny flag reader that avoids the flag package so positional
// prompt args can follow the model flag.
func flagValue(name, def string) string {
	for i, a := range os.Args[1:] {
		if a == name && i+2 <= len(os.Args)-1 {
			return os.Args[i+2]
		}
	}
	return def
}

// promptArg returns the trailing positional arguments joined as the prompt.
func promptArg() string {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-model" {
			i++ // skip the model value
			continue
		}
		return strings.Join(args[i:], " ")
	}
	return ""
}
