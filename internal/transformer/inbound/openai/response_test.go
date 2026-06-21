package openai

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/model"
)

func TestConvertToInternalRequestPreservesRawInputItems(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{
			{Type: "input_text", Text: stringPtr("hello")},
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if len(internalReq.RawInputItems) == 0 {
		t.Fatalf("expected raw input items to be preserved")
	}

	var items []map[string]any
	if err := json.Unmarshal(internalReq.RawInputItems, &items); err != nil {
		t.Fatalf("unmarshal raw input items failed: %v", err)
	}
	if len(items) != 1 || items[0]["type"] != "input_text" {
		t.Fatalf("expected original raw input items to be kept, got %#v", items)
	}
	if internalReq.TransformOptions.ArrayInputs == nil || !*internalReq.TransformOptions.ArrayInputs {
		t.Fatalf("expected array input flag to stay true")
	}
}

func TestConvertToInternalRequestDoesNotRequirePassthroughForUnsupportedToolType(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Text: stringPtr("hello")},
		Tools: []ResponsesTool{{
			Type: "apply_patch",
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected unsupported responses tool to degrade without passthrough requirement")
	}
}

func TestConvertToInternalRequestDoesNotRequirePassthroughForUnsupportedInputItem(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{{
			Type:   "apply_patch_call_output",
			CallID: "apc_123",
		}}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected unsupported responses input item to degrade without passthrough requirement")
	}
}

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

func TestResponseInboundTransformRequestPreservesRawResponsesTools(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"input":"hello",
		"tools":[{
			"type":"custom",
			"name":"apply_patch",
			"format":{"type":"grammar","syntax":"lark","definition":"start: PATCH"}
		},{
			"type":"tool_search",
			"execution":"client"
		}]
	}`)

	internalReq, err := (&ResponseInbound{}).TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected responses-native tools to degrade without passthrough requirement")
	}

	var tools []map[string]any
	if err := json.Unmarshal(internalReq.OpenAIRawResponseTools(), &tools); err != nil {
		t.Fatalf("unmarshal raw response tools failed: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected raw response tools to be preserved, got %#v", tools)
	}
	format, ok := tools[0]["format"].(map[string]any)
	if !ok || format["syntax"] != "lark" || format["definition"] != "start: PATCH" {
		t.Fatalf("expected custom grammar format to be preserved, got %#v", tools[0])
	}
	if tools[1]["type"] != "tool_search" || tools[1]["execution"] != "client" {
		t.Fatalf("expected raw-only tool to be preserved, got %#v", tools[1])
	}
}

func TestConvertToInternalRequestDoesNotMarkPassthroughForSupportedFileAndAudioInputs(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{
			{
				Type: "message",
				Role: "user",
				Content: &ResponsesInput{Items: []ResponsesItem{
					{Type: "input_file", FileID: stringPtr("file_123")},
					{Type: "input_audio", InputAudio: &ResponsesInputAudio{Format: "wav", Data: "AAA="}},
				}},
			},
		}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected supported file/audio inputs to stay normalized without passthrough")
	}
	if len(internalReq.Messages) != 1 || len(internalReq.Messages[0].Content.MultipleContent) != 2 {
		t.Fatalf("expected supported file/audio inputs to normalize into message content, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[0].Content.MultipleContent[0].Type != "file" {
		t.Fatalf("expected file content part, got %#v", internalReq.Messages[0].Content.MultipleContent[0])
	}
	if internalReq.Messages[0].Content.MultipleContent[1].Type != "input_audio" {
		t.Fatalf("expected input_audio content part, got %#v", internalReq.Messages[0].Content.MultipleContent[1])
	}
}

func TestConvertToInternalRequestNormalizesTopLevelInputFile(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: ResponsesInput{Items: []ResponsesItem{{
			Type:     "input_file",
			FileID:   stringPtr("file_456"),
			Filename: stringPtr("notes.txt"),
		}}},
	}

	internalReq, err := convertToInternalRequest(req)
	if err != nil {
		t.Fatalf("convertToInternalRequest failed: %v", err)
	}
	if internalReq.HasOpenAIResponsesPassthrough() {
		t.Fatalf("expected top-level input_file to stay normalized without passthrough")
	}
	if len(internalReq.Messages) != 1 {
		t.Fatalf("expected one normalized message, got %#v", internalReq.Messages)
	}
	if internalReq.Messages[0].Role != "user" {
		t.Fatalf("expected top-level input_file to default to user role, got %#v", internalReq.Messages[0].Role)
	}
	if len(internalReq.Messages[0].Content.MultipleContent) != 1 || internalReq.Messages[0].Content.MultipleContent[0].Type != "file" {
		t.Fatalf("expected top-level input_file to become file content, got %#v", internalReq.Messages[0].Content)
	}
	if internalReq.Messages[0].Content.MultipleContent[0].File == nil || internalReq.Messages[0].Content.MultipleContent[0].File.FileID != "file_456" {
		t.Fatalf("expected normalized file reference to preserve file_id, got %#v", internalReq.Messages[0].Content.MultipleContent[0].File)
	}
}

func stringPtr(value string) *string {
	return &value
}
