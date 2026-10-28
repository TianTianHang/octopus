package openai

import (
	"context"
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
