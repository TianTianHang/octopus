package openai

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/model"
)

func TestChatOutboundAcceptHeaderFollowsStream(t *testing.T) {
	stream := true
	outbound := &ChatOutbound{}

	req, err := outbound.TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:    "gpt-4o",
		Stream:   &stream,
		Messages: []model.Message{{Role: "user", Content: model.MessageContent{Content: stringPtr("hi")}}},
	}, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest(stream) error = %v", err)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("streaming Accept = %q, want text/event-stream", got)
	}

	req, err = outbound.TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:    "gpt-4o",
		Messages: []model.Message{{Role: "user", Content: model.MessageContent{Content: stringPtr("hi")}}},
	}, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest(non-stream) error = %v", err)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("non-streaming Accept = %q, want application/json", got)
	}
}

func TestResponsesOutboundAcceptHeaderFollowsStream(t *testing.T) {
	stream := true
	outbound := &ResponseOutbound{}

	req, err := outbound.TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:  "gpt-5",
		Stream: &stream,
	}, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest(stream) error = %v", err)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("streaming Accept = %q, want text/event-stream", got)
	}

	req, err = outbound.TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model: "gpt-5",
	}, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest(non-stream) error = %v", err)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("non-streaming Accept = %q, want application/json", got)
	}
}

func TestResponsesRawRequestAcceptHeaderFollowsStreamFlag(t *testing.T) {
	outbound := &ResponseOutbound{}

	req, err := outbound.TransformRequestRaw(context.Background(),
		[]byte(`{"model":"gpt-5","stream":true,"input":"hi"}`), "gpt-5", "https://example.com/v1", "sk-test", nil)
	if err != nil {
		t.Fatalf("TransformRequestRaw(stream) error = %v", err)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("raw streaming Accept = %q, want text/event-stream", got)
	}

	req, err = outbound.TransformRequestRaw(context.Background(),
		[]byte(`{"model":"gpt-5","input":"hi"}`), "gpt-5", "https://example.com/v1", "sk-test", nil)
	if err != nil {
		t.Fatalf("TransformRequestRaw(non-stream) error = %v", err)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("raw non-streaming Accept = %q, want application/json", got)
	}
}

func TestChatOutboundForwardsProviderExtensionsVerbatim(t *testing.T) {
	stream := false
	request := &model.InternalLLMRequest{
		Model:              "deepseek-chat",
		Stream:             &stream,
		Thinking:           json.RawMessage(`{"type":"enabled","budget_tokens":1024}`),
		ChatTemplateKwargs: json.RawMessage(`{"enable_thinking":true}`),
		Messages:           []model.Message{{Role: "user", Content: model.MessageContent{Content: stringPtr("hi")}}},
	}

	req, err := (&ChatOutbound{}).TransformRequest(context.Background(), request, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	if got := string(payload["thinking"]); got != `{"type":"enabled","budget_tokens":1024}` {
		t.Errorf("thinking = %s, want the client payload verbatim", got)
	}
	if got := string(payload["chat_template_kwargs"]); got != `{"enable_thinking":true}` {
		t.Errorf("chat_template_kwargs = %s, want the client payload verbatim", got)
	}
}

func TestChatOutboundOmitsProviderExtensionsWhenUnset(t *testing.T) {
	request := &model.InternalLLMRequest{
		Model:    "gpt-4o",
		Messages: []model.Message{{Role: "user", Content: model.MessageContent{Content: stringPtr("hi")}}},
	}
	req, err := (&ChatOutbound{}).TransformRequest(context.Background(), request, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	for _, field := range []string{"thinking", "chat_template_kwargs"} {
		if _, ok := payload[field]; ok {
			t.Errorf("%s must be omitted when the client did not send it", field)
		}
	}
}
