# Axonhub Responses Custom Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the P1 review issue by adopting Axonhub's structured handling of OpenAI Responses custom tool call history, so Responses requests can degrade through Chat/Messages paths without silently dropping required context.

**Architecture:** Preserve the existing octopus direction that Responses-native tool definitions may degrade through downstream Chat/Messages channels, but stop treating custom tool call input/output as unknown raw-only history. Model `custom_tool_call` and `custom_tool_call_output` as first-class internal request history, replay them exactly on Responses outbound, and use explicit fallback behavior on non-Responses outbound instead of silent loss.

**Tech Stack:** Go, octopus internal transformer model, OpenAI Responses inbound/outbound transformers, OpenAI Chat Completions outbound transformer, Anthropic Messages outbound transformer, Go unit/relay tests.

---

## Context

The current patch removed the broad passthrough guard for Responses-native tools/items. That enables a Responses client to reach downstream models whose native interface is Chat Completions or Anthropic Messages.

The review P1 points out a real remaining bug: some Responses input items cannot currently be represented by octopus `model.Message`. When such an item appears in `input`, `convertItemToMessage` returns `nil, nil`; Chat and Anthropic outbound only read `request.Messages`, so the item disappears from the prompt. A downstream model then answers without seeing required tool execution context.

The concrete example is a custom tool result from Codex-style Responses history:

```json
{
  "type": "custom_tool_call_output",
  "call_id": "call_patch_001",
  "output": "Patch applied successfully."
}
```

In the current octopus code, `function_call_output` is represented, but `custom_tool_call` and `custom_tool_call_output` are not.

## Axonhub Source Evidence

Axonhub unstable source inspected at `/tmp/axonhub-inspect`, branch `unstable`.

Axonhub does not solve this by restoring a broad "Responses-native requires passthrough" guard. Instead, it adds Responses custom tool semantics to its internal model.

Key source points:

- `/tmp/axonhub-inspect/llm/transformer/openai/responses/inbound.go:536` converts `custom_tool_call` into an internal assistant message with a `responses_custom_tool` tool call.
- `/tmp/axonhub-inspect/llm/transformer/openai/responses/inbound.go:574` converts `custom_tool_call_output` into an internal `role="tool"` message with `ToolCallID` and output content.
- `/tmp/axonhub-inspect/llm/tools.go:249` defines `ResponseCustomTool`, including grammar format.
- `/tmp/axonhub-inspect/llm/tools.go:270` defines `ResponseCustomToolCall`, including `call_id`, `name`, and freeform `input`.
- `/tmp/axonhub-inspect/llm/transformer/openai/responses/inbound.go:709` converts Responses `tools:[{"type":"custom"}]` into internal `responses_custom_tool`.
- `/tmp/axonhub-inspect/llm/transformer/openai/responses/outbound_convert.go:108` tracks whether a tool result should re-emit as `function_call_output` or `custom_tool_call_output`.
- `/tmp/axonhub-inspect/llm/transformer/openai/outbound_convert.go:60` filters non-function tool definitions when sending OpenAI Chat Completions.

The Axonhub integration test records the intended normalized shape:

- `/tmp/axonhub-inspect/llm/transformer/openai/responses/inbound_integration_test.go:72` expects four internal messages: system, user, assistant custom tool call, tool output.
- `/tmp/axonhub-inspect/llm/transformer/openai/responses/inbound_integration_test.go:90` asserts the assistant tool call type is `responses_custom_tool`.
- `/tmp/axonhub-inspect/llm/transformer/openai/responses/inbound_integration_test.go:96` asserts the custom tool output is preserved as a `tool` message.

## Axonhub Strategy

Axonhub separates three concerns:

1. Raw replay for fields that cannot be represented.
2. Structured internal representation for Responses concepts that can be represented safely.
3. Provider-specific outbound filtering for endpoints that do not accept those concepts.

For P1, the important choice is item 2. A `custom_tool_call_output` is not treated as an opaque unsupported item because it has a clear Chat-like semantic shape: it is a tool result associated with a call id. Axonhub maps it into the same internal history lane used by function tool results.

The custom tool call itself needs extra structure because OpenAI Chat function calls require JSON arguments, while Responses custom tools use freeform input. Axonhub therefore adds `ResponseCustomToolCall` beside the normal function call fields instead of stuffing the freeform patch text into `Function.Arguments`.

## Octopus Current State

Relevant octopus code:

- `internal/transformer/inbound/openai/response.go:1345` has `convertItemToMessage`.
- `internal/transformer/inbound/openai/response.go:1382` handles `function_call`.
- `internal/transformer/inbound/openai/response.go:1397` handles `function_call_output`.
- `internal/transformer/inbound/openai/response.go:1424` currently returns `nil, nil` for unknown item types.
- `internal/transformer/inbound/openai/response.go:942` defines `ResponsesItem`, but it does not currently have an `Input *string` field for `custom_tool_call`.
- `internal/transformer/inbound/openai/response.go:1520` converts Responses tools, but currently only handles `function` and `image_generation`.
- `internal/transformer/model/model.go:1713` defines `model.Tool` without a Responses custom tool field.
- `internal/transformer/model/model.go:1795` defines `model.ToolCall` without a Responses custom tool call field.
- `internal/transformer/outbound/openai/chat.go:242` already filters non-function tool definitions for Chat Completions.
- `internal/transformer/outbound/openai/response.go:1154` always emits tool calls as `function_call`.
- `internal/transformer/outbound/openai/response.go:1212` always emits tool results as `function_call_output`.

Existing tests from the current patch:

- `internal/transformer/inbound/openai/response_test.go:55` currently says unsupported input items degrade without passthrough, but it does not assert that the item remains visible in `Messages`.
- `internal/relay/relay_test.go` includes degradation tests for native Responses tools, but it does not cover custom tool call history.

## Design Decision

Adopt Axonhub's structured model for known Responses custom tool history:

- Add an internal constant `ToolTypeResponsesCustomTool = "responses_custom_tool"`.
- Add `ResponseCustomTool` and `ResponseCustomToolFormat` to represent `tools:[{"type":"custom"}]`.
- Add `ResponseCustomToolCall` to represent assistant custom tool calls.
- Add `ResponseCustomTool *ResponseCustomTool` to `model.Tool`.
- Add `ResponseCustomToolCall *ResponseCustomToolCall` to `model.ToolCall`.
- Convert `custom_tool_call` to an assistant message with `ToolCalls`.
- Convert `custom_tool_call_output` to a tool message with `ToolCallID`.
- Convert Responses outbound back to `custom_tool_call` and `custom_tool_call_output` when the associated call was a Responses custom tool call.

Do not silently drop still-unknown input items. After the known custom tool cases are modeled, remaining unknown item types need explicit behavior:

- Same Responses outbound can keep using raw input replay.
- Non-Responses outbound should either receive a synthetic text fallback or the request should require Responses passthrough for that item type.

For P1 specifically, the minimum safe fix is to model `custom_tool_call` and `custom_tool_call_output` and add tests that prove their content is preserved on degraded paths.

## Important Caveat From Axonhub

Axonhub filters `responses_custom_tool` definitions out of OpenAI Chat Completions outbound because Chat only accepts function tools. However, a direct copy of its generic `ToolCallFromLLM` behavior can still produce a historical Chat `tool_calls` entry with type `responses_custom_tool`, which OpenAI Chat may not accept.

For octopus, do not assume every Axonhub outbound detail is safe to copy. The safer rule is:

- Responses outbound: emit true `custom_tool_call` and `custom_tool_call_output`.
- Chat/Anthropic outbound: do not send unsupported custom tool definitions as callable tools.
- Chat/Anthropic historical context: preserve information explicitly. Either serialize custom tool calls/results into text context, or transform only when the target protocol accepts the resulting shape.

This document keeps P1 scoped to "do not silently drop Responses custom tool history." P2, where `tool_choice` survives after filtering tools, is adjacent but should be handled as a separate review item.

---

## File Structure

- Modify: `internal/transformer/model/model.go`
  - Add Responses custom tool constants and internal structs.
  - Extend `Tool` and `ToolCall` with optional Responses custom fields.

- Modify: `internal/transformer/inbound/openai/response.go`
  - Add `Input *string` to `ResponsesItem`.
  - Convert Responses custom tools to internal tools.
  - Convert `custom_tool_call` and `custom_tool_call_output` to internal messages.

- Modify: `internal/transformer/outbound/openai/response.go`
  - Add `Input *string` to outbound `ResponsesItem`.
  - Emit `custom_tool_call` from internal `ResponseCustomToolCall`.
  - Emit `custom_tool_call_output` for matching tool result messages.

- Modify: `internal/transformer/outbound/openai/chat.go`
  - Keep filtering non-function tool definitions.
  - Add explicit historical fallback for `responses_custom_tool` tool calls if Chat rejects native-shaped custom tool calls.

- Modify: `internal/transformer/outbound/anthropic/messages.go`
  - Keep filtering unsupported tool definitions.
  - Add explicit historical fallback for Responses custom tool calls/results if Anthropic cannot accept the native shape.

- Modify: `internal/transformer/inbound/openai/response_test.go`
  - Add failing tests for custom tool call/output preservation.

- Modify: `internal/transformer/outbound/openai/response_test.go`
  - Add Responses replay test for custom tool call/output.

- Modify: `internal/relay/relay_test.go`
  - Add degraded Chat/Anthropic relay tests proving custom tool output is visible downstream.

---

### Task 1: Lock P1 With an Inbound Failing Test

**Files:**
- Modify: `internal/transformer/inbound/openai/response_test.go`

- [ ] **Step 1: Add a failing test for custom tool history**

Append this test near the existing Responses passthrough/degradation tests:

```go
func TestConvertToInternalRequestPreservesCustomToolCallHistory(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: main.py\n+print(\"hello\")\n*** End Patch\n"

	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{
			{
				Type: "message",
				Role: "user",
				Content: &ResponsesInput{Items: []ResponsesItem{{
					Type: "input_text",
					Text: stringPtr("apply this patch"),
				}}},
			},
			{
				Type:   "custom_tool_call",
				CallID: "call_patch_001",
				Name:   "apply_patch",
				Input:  stringPtr(patch),
			},
			{
				Type:   "custom_tool_call_output",
				CallID: "call_patch_001",
				Output: &ResponsesInput{Text: stringPtr("Patch applied successfully.")},
			},
		}},
		Tools: []ResponsesTool{{
			Type:        "custom",
			Name:        "apply_patch",
			Description: "Use apply_patch.",
			Format: &ResponsesCustomToolFormat{
				Type:       "grammar",
				Syntax:     "lark",
				Definition: "start: PATCH",
			},
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("custom tool history should degrade structurally without forcing passthrough")
	}
	if len(internalReq.Messages) != 3 {
		t.Fatalf("expected user, assistant custom tool call, and tool output messages, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[1].Role != "assistant" || len(internalReq.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected assistant custom tool call, got %#v", internalReq.Messages[1])
	}

	toolCall := internalReq.Messages[1].ToolCalls[0]
	if toolCall.ID != "call_patch_001" || toolCall.Type != model.ToolTypeResponsesCustomTool {
		t.Fatalf("expected responses custom tool call, got %#v", toolCall)
	}
	if toolCall.ResponseCustomToolCall == nil ||
		toolCall.ResponseCustomToolCall.Name != "apply_patch" ||
		toolCall.ResponseCustomToolCall.Input != patch {
		t.Fatalf("expected custom tool call payload to be preserved, got %#v", toolCall.ResponseCustomToolCall)
	}

	if internalReq.Messages[2].Role != "tool" ||
		internalReq.Messages[2].ToolCallID == nil ||
		*internalReq.Messages[2].ToolCallID != "call_patch_001" ||
		internalReq.Messages[2].Content.Content == nil ||
		*internalReq.Messages[2].Content.Content != "Patch applied successfully." {
		t.Fatalf("expected custom tool output to become a tool message, got %#v", internalReq.Messages[2])
	}

	if len(internalReq.Tools) != 1 || internalReq.Tools[0].Type != model.ToolTypeResponsesCustomTool {
		t.Fatalf("expected custom tool definition to be represented internally, got %#v", internalReq.Tools)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails for the current bug**

Run:

```bash
GOCACHE=/tmp/octopus-go-build GOMODCACHE=/tmp/octopus-go-mod go test ./internal/transformer/inbound/openai -run TestConvertToInternalRequestPreservesCustomToolCallHistory -count=1
```

Expected before implementation: fail because `ResponsesItem` lacks `Input`, `model.ToolTypeResponsesCustomTool`, and `ResponseCustomToolCall`, or because custom tool items are dropped.

### Task 2: Add Internal Responses Custom Tool Types

**Files:**
- Modify: `internal/transformer/model/model.go`

- [ ] **Step 1: Add constants and structs**

Add near the existing tool model definitions:

```go
const ToolTypeResponsesCustomTool = "responses_custom_tool"

// ResponseCustomTool represents an OpenAI Responses custom tool.
// Custom tools use freeform input rather than JSON function arguments.
type ResponseCustomTool struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	Format      *ResponseCustomToolFormat `json:"format,omitempty"`
}

type ResponseCustomToolFormat struct {
	Type       string `json:"type"`
	Syntax     string `json:"syntax,omitempty"`
	Definition string `json:"definition,omitempty"`
}

type ResponseCustomToolCall struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Input  string `json:"input"`
}
```

- [ ] **Step 2: Extend `Tool`**

Change `model.Tool` to include:

```go
ResponseCustomTool *ResponseCustomTool `json:"response_custom_tool,omitempty"`
```

- [ ] **Step 3: Extend `ToolCall`**

Change `model.ToolCall` to include:

```go
ResponseCustomToolCall *ResponseCustomToolCall `json:"response_custom_tool_call,omitempty"`
```

- [ ] **Step 4: Run model package tests**

Run:

```bash
GOCACHE=/tmp/octopus-go-build GOMODCACHE=/tmp/octopus-go-mod go test ./internal/transformer/model -count=1
```

Expected after this task: pass.

### Task 3: Convert Responses Custom Tool History Inbound

**Files:**
- Modify: `internal/transformer/inbound/openai/response.go`
- Modify: `internal/transformer/inbound/openai/response_test.go`

- [ ] **Step 1: Add freeform input to `ResponsesItem`**

Add to `ResponsesItem` with the function call fields:

```go
Input *string `json:"input,omitempty"`
```

- [ ] **Step 2: Convert `custom_tool_call` and `custom_tool_call_output`**

Add cases to `convertItemToMessage`:

```go
case "custom_tool_call":
	input := ""
	if item.Input != nil {
		input = *item.Input
	}
	return &model.Message{
		Role: "assistant",
		ToolCalls: []model.ToolCall{
			{
				ID:   item.CallID,
				Type: model.ToolTypeResponsesCustomTool,
				ResponseCustomToolCall: &model.ResponseCustomToolCall{
					CallID: item.CallID,
					Name:   item.Name,
					Input:  input,
				},
			},
		},
	}, nil

case "custom_tool_call_output":
	if item.Output == nil {
		return nil, fmt.Errorf("custom_tool_call_output item must have non-nil output")
	}
	msg := &model.Message{
		Role:       "tool",
		ToolCallID: lo.ToPtr(item.CallID),
		Content:    convertInputToMessageContent(*item.Output),
	}
	if item.Name != "" {
		msg.ToolCallName = lo.ToPtr(item.Name)
	}
	return msg, nil
```

- [ ] **Step 3: Convert Responses `custom` tool definitions**

Add a `case "custom"` branch to `convertToolsToInternal`:

```go
case "custom":
	customTool := &model.ResponseCustomTool{
		Name:        tool.Name,
		Description: tool.Description,
	}
	if tool.Format != nil {
		customTool.Format = &model.ResponseCustomToolFormat{
			Type:       tool.Format.Type,
			Syntax:     tool.Format.Syntax,
			Definition: tool.Format.Definition,
		}
	}
	result = append(result, model.Tool{
		Type:               model.ToolTypeResponsesCustomTool,
		ResponseCustomTool: customTool,
	})
```

- [ ] **Step 4: Replace the old unsupported-item expectation**

The existing `TestConvertToInternalRequestDoesNotRequirePassthroughForUnsupportedInputItem` uses `apply_patch_call_output`, which is not the OpenAI Responses item name used by Axonhub. Keep it only if it asserts explicit fallback behavior. For P1, the required test is `custom_tool_call_output`.

Change the old test so it no longer proves silent loss acceptable. A safe assertion is:

```go
if len(internalReq.Messages) == 0 && len(internalReq.RawInputItems) == 0 {
	t.Fatalf("unsupported responses input item must not disappear completely")
}
```

- [ ] **Step 5: Run inbound tests**

Run:

```bash
GOCACHE=/tmp/octopus-go-build GOMODCACHE=/tmp/octopus-go-mod go test ./internal/transformer/inbound/openai -count=1
```

Expected after this task: pass.

### Task 4: Replay Custom Tool History On Responses Outbound

**Files:**
- Modify: `internal/transformer/outbound/openai/response.go`
- Modify: `internal/transformer/outbound/openai/response_test.go`

- [ ] **Step 1: Add freeform input to outbound `ResponsesItem`**

Add to outbound `ResponsesItem` with the function call fields:

```go
Input *string `json:"input,omitempty"`
```

- [ ] **Step 2: Emit `custom_tool_call` for custom tool calls**

In `convertAssistantMessageToResponses`, branch before the current function-call append:

```go
for _, tc := range msg.ToolCalls {
	if tc.ResponseCustomToolCall != nil {
		items = append(items, ResponsesItem{
			Type:   "custom_tool_call",
			CallID: tc.ResponseCustomToolCall.CallID,
			Name:   tc.ResponseCustomToolCall.Name,
			Input:  lo.ToPtr(tc.ResponseCustomToolCall.Input),
		})
		continue
	}

	items = append(items, ResponsesItem{
		Type:      "function_call",
		CallID:    tc.ID,
		Name:      tc.Function.Name,
		Arguments: tc.Function.Arguments,
	})
}
```

- [ ] **Step 3: Emit `custom_tool_call_output` for matching tool results**

Add a helper that builds a call-id map from previous assistant messages:

```go
func responsesToolResultTypeByCallID(msgs []model.Message) map[string]string {
	result := make(map[string]string)
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ResponseCustomToolCall != nil && tc.ResponseCustomToolCall.CallID != "" {
				result[tc.ResponseCustomToolCall.CallID] = "custom_tool_call_output"
				continue
			}
			if tc.ID != "" {
				result[tc.ID] = "function_call_output"
			}
		}
	}
	return result
}
```

Use that map inside `convertInputFromMessages` when converting `role="tool"` messages:

```go
toolResultTypes := responsesToolResultTypeByCallID(msgs)
```

Then call a typed converter:

```go
itemType := "function_call_output"
if msg.ToolCallID != nil {
	if mapped, ok := toolResultTypes[*msg.ToolCallID]; ok {
		itemType = mapped
	}
}
items = append(items, convertToolMessageToResponsesWithType(msg, itemType))
```

Implement:

```go
func convertToolMessageToResponsesWithType(msg model.Message, itemType string) ResponsesItem {
	item := convertToolMessageToResponses(msg)
	item.Type = itemType
	return item
}
```

- [ ] **Step 4: Add a Responses outbound test**

Add to `internal/transformer/outbound/openai/response_test.go`:

```go
func TestConvertToResponsesRequestReplaysCustomToolCallHistory(t *testing.T) {
	req := &model.InternalLLMRequest{
		Model:        "gpt-4o",
		RawAPIFormat: model.APIFormatOpenAIResponse,
		Messages: []model.Message{
			{
				Role: "assistant",
				ToolCalls: []model.ToolCall{{
					ID:   "call_patch_001",
					Type: model.ToolTypeResponsesCustomTool,
					ResponseCustomToolCall: &model.ResponseCustomToolCall{
						CallID: "call_patch_001",
						Name:   "apply_patch",
						Input:  "*** Begin Patch\n*** End Patch\n",
					},
				}},
			},
			{
				Role:       "tool",
				ToolCallID: stringPtr("call_patch_001"),
				Content:    model.MessageContent{Content: stringPtr("Patch applied successfully.")},
			},
		},
	}

	payload := convertToResponsesRequest(req)
	if len(payload.Input.Items) != 2 {
		t.Fatalf("expected custom tool call and output items, got %#v", payload.Input.Items)
	}
	if payload.Input.Items[0].Type != "custom_tool_call" ||
		payload.Input.Items[0].Name != "apply_patch" ||
		payload.Input.Items[0].Input == nil ||
		*payload.Input.Items[0].Input == "" {
		t.Fatalf("expected custom tool call item, got %#v", payload.Input.Items[0])
	}
	if payload.Input.Items[1].Type != "custom_tool_call_output" ||
		payload.Input.Items[1].Output == nil ||
		payload.Input.Items[1].Output.Text == nil ||
		*payload.Input.Items[1].Output.Text != "Patch applied successfully." {
		t.Fatalf("expected custom tool output item, got %#v", payload.Input.Items[1])
	}
}
```

- [ ] **Step 5: Run outbound Responses tests**

Run:

```bash
GOCACHE=/tmp/octopus-go-build GOMODCACHE=/tmp/octopus-go-mod go test ./internal/transformer/outbound/openai -run 'TestConvertToResponsesRequest(ReplaysCustomToolCallHistory|PreservesRawResponseTools|PreservesRawInputItems)' -count=1
```

Expected after this task: pass.

### Task 5: Preserve Context On Non-Responses Outbound

**Files:**
- Modify: `internal/transformer/outbound/openai/chat.go`
- Modify: `internal/transformer/outbound/anthropic/messages.go`
- Modify: `internal/relay/relay_test.go`

- [ ] **Step 1: Decide explicit fallback shape**

Use a compact text fallback for historical Responses custom tool calls on protocols that do not support them as native tool calls. This avoids sending invalid `tool_calls` entries to Chat Completions while still preserving context.

Fallback text:

```text
Responses custom tool call apply_patch (call_id: call_patch_001):
*** Begin Patch
...
*** End Patch
```

Tool result fallback:

```text
Responses custom tool result (call_id: call_patch_001):
Patch applied successfully.
```

- [ ] **Step 2: Add a Chat outbound sanitizer**

Before building Chat messages, transform custom tool call history into plain text messages for Chat-only targets:

```go
func normalizeResponsesCustomToolHistoryForChat(messages []model.Message) []model.Message {
	normalized := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			var kept []model.ToolCall
			var textParts []string
			for _, tc := range msg.ToolCalls {
				if tc.ResponseCustomToolCall == nil {
					kept = append(kept, tc)
					continue
				}
				textParts = append(textParts, fmt.Sprintf(
					"Responses custom tool call %s (call_id: %s):\n%s",
					tc.ResponseCustomToolCall.Name,
					tc.ResponseCustomToolCall.CallID,
					tc.ResponseCustomToolCall.Input,
				))
			}
			msg.ToolCalls = kept
			if len(textParts) > 0 {
				msg.Content = appendTextContent(msg.Content, strings.Join(textParts, "\n\n"))
			}
		}

		if msg.Role == "tool" && msg.ToolCallID != nil {
			msg.Role = "user"
			msg.Content = appendTextContent(model.MessageContent{}, fmt.Sprintf(
				"Responses custom tool result (call_id: %s):\n%s",
				*msg.ToolCallID,
				messageContentText(msg.Content),
			))
			msg.ToolCallID = nil
			msg.ToolCallName = nil
		}

		normalized = append(normalized, msg)
	}
	return normalized
}
```

Implement `appendTextContent` and `messageContentText` locally or reuse existing helpers if present. The helper must be deterministic and must not drop existing assistant text.

- [ ] **Step 3: Apply equivalent fallback in Anthropic outbound**

Anthropic `tool_use` also expects JSON input and a declared tool schema. For Responses custom tool history from a non-Anthropic client, use the same text fallback before `convertMessages` builds blocks.

Keep ordinary function tool calls unchanged.

- [ ] **Step 4: Add relay tests for degraded context**

Add a Chat relay test with a request body containing:

```json
{
  "model": "relay-openai-responses-to-chat",
  "input": [
    {"type":"message","role":"user","content":[{"type":"input_text","text":"what happened?"}]},
    {"type":"custom_tool_call","call_id":"call_patch_001","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n"},
    {"type":"custom_tool_call_output","call_id":"call_patch_001","output":"Patch applied successfully."}
  ],
  "tools": [
    {"type":"custom","name":"apply_patch","format":{"type":"grammar","syntax":"lark","definition":"start: PATCH"}}
  ]
}
```

Assert the captured Chat upstream payload contains both `custom tool call apply_patch` and `Patch applied successfully.`, and that it does not contain a `tools` entry for `apply_patch`.

Add the same assertion for an Anthropic `/messages` downstream payload.

- [ ] **Step 5: Run relay tests**

Run:

```bash
GOCACHE=/tmp/octopus-go-build GOMODCACHE=/tmp/octopus-go-mod go test ./internal/relay -run 'Responses.*CustomTool|NativeResponsesTool' -count=1
```

Expected after this task: pass.

### Task 6: Full Verification

**Files:**
- No new files.

- [ ] **Step 1: Run focused transformer and relay test suite**

Run:

```bash
GOCACHE=/tmp/octopus-go-build GOMODCACHE=/tmp/octopus-go-mod go test ./internal/transformer/inbound/openai ./internal/transformer/outbound/openai ./internal/transformer/outbound/anthropic ./internal/transformer/model ./internal/relay
```

Expected: all listed packages pass.

- [ ] **Step 2: Check the worktree**

Run:

```bash
git status --short
```

Expected: only the intended implementation files and this plan are changed, plus any unrelated pre-existing dirty files that were present before this plan:

```text
internal/conf/version.go
internal/update/update.go
web/src/lib/info.ts
```

---

## Self-Review

Spec coverage:

- The document records Axonhub's approach with source paths and behavior.
- The plan addresses P1 by ensuring `custom_tool_call_output` is represented as a tool message instead of being silently dropped.
- The plan preserves the user's direction that Responses-native requests can degrade to Chat/Messages instead of being rejected wholesale.
- The plan explicitly flags the Axonhub outbound caveat rather than blindly copying behavior that may still be invalid for Chat.

No placeholder scan:

- The plan contains no open-ended implementation placeholders.
- Each implementation task has exact files, code snippets, commands, and expected outcomes.

Type consistency:

- Internal custom tool type name is consistently `ToolTypeResponsesCustomTool`.
- Struct fields are consistently `ResponseCustomTool`, `ResponseCustomToolFormat`, and `ResponseCustomToolCall`.
- Responses wire item names are consistently `custom_tool_call` and `custom_tool_call_output`.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-21-axonhub-responses-custom-tools.md`.

Two execution options:

1. Subagent-Driven (recommended): dispatch a fresh subagent per task, review between tasks, fastest feedback loop.
2. Inline Execution: execute tasks in this session using `superpowers:executing-plans`, with checkpoints after each task.
