package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestStreamEventsHaveOutput(t *testing.T) {
	cases := []struct {
		name   string
		events []model.StreamEvent
		want   bool
	}{
		{name: "message start is not output", events: []model.StreamEvent{{Kind: model.StreamEventKindMessageStart}}},
		{name: "empty content block start is not output", events: []model.StreamEvent{{Kind: model.StreamEventKindContentBlockStart, ContentBlock: &model.StreamContentBlock{Type: "text"}}}},
		{name: "text delta is output", events: []model.StreamEvent{{Kind: model.StreamEventKindTextDelta}}, want: true},
		{name: "thinking delta is output", events: []model.StreamEvent{{Kind: model.StreamEventKindThinkingDelta}}, want: true},
		{name: "tool call start is output", events: []model.StreamEvent{{Kind: model.StreamEventKindToolCallStart}}, want: true},
		{name: "tool use block start is output", events: []model.StreamEvent{{Kind: model.StreamEventKindContentBlockStart, ContentBlock: &model.StreamContentBlock{Type: "tool_use"}}}, want: true},
		{name: "error is output", events: []model.StreamEvent{{Kind: model.StreamEventKindError}}, want: true},
		{name: "usage only is not output", events: []model.StreamEvent{{Kind: model.StreamEventKindUsageDelta}}},
		{name: "done is not output", events: []model.StreamEvent{{Kind: model.StreamEventKindDone}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := streamEventsHaveOutput(tc.events); got != tc.want {
				t.Fatalf("streamEventsHaveOutput() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStreamResponseHasOutput(t *testing.T) {
	text := "hello"
	empty := ""
	cases := []struct {
		name string
		resp *model.InternalLLMResponse
		want bool
	}{
		{name: "nil response", resp: nil},
		{name: "role only", resp: &model.InternalLLMResponse{Choices: []model.Choice{{Delta: &model.Message{Role: "assistant"}}}}},
		{name: "empty content", resp: &model.InternalLLMResponse{Choices: []model.Choice{{Delta: &model.Message{Content: model.MessageContent{Content: &empty}}}}}},
		{name: "text content", resp: &model.InternalLLMResponse{Choices: []model.Choice{{Delta: &model.Message{Content: model.MessageContent{Content: &text}}}}}, want: true},
		{name: "tool call", resp: &model.InternalLLMResponse{Choices: []model.Choice{{Delta: &model.Message{ToolCalls: []model.ToolCall{{ID: "call_1"}}}}}}, want: true},
		{name: "refusal", resp: &model.InternalLLMResponse{Choices: []model.Choice{{Delta: &model.Message{Refusal: "no"}}}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := streamResponseHasOutput(tc.resp); got != tc.want {
				t.Fatalf("streamResponseHasOutput() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandleStreamResponseBuffersStartupUntilOutput(t *testing.T) {
	rawSSE := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")

	ctx := context.Background()
	writer := &notifyStreamWriter{header: http.Header{}}
	ra := newConvertedStreamAttempt(inbound.InboundTypeOpenAIChat, outbound.OutboundTypeAnthropic, writer)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(rawSSE)),
	}

	if err := ra.handleStreamResponse(ctx, response); err != nil {
		t.Fatalf("handleStreamResponse error = %v", err)
	}

	out := writer.buf.String()
	textIdx := strings.Index(out, "hello")
	if textIdx < 0 {
		t.Fatalf("client never received the content delta: %q", out)
	}
	// 启动事件(角色)必须在内容之前补发, 不能被丢掉或乱序。
	if roleIdx := strings.Index(out, `"role"`); roleIdx < 0 || roleIdx > textIdx {
		t.Fatalf("buffered startup event was not flushed before the content: %q", out)
	}
}

func TestHandleStreamResponseRejectsStartupOnlyStream(t *testing.T) {
	rawSSE := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"",
	}, "\n")

	ctx := context.Background()
	writer := &notifyStreamWriter{header: http.Header{}}
	ra := newConvertedStreamAttempt(inbound.InboundTypeOpenAIChat, outbound.OutboundTypeAnthropic, writer)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(rawSSE)),
	}

	err := ra.handleStreamResponse(ctx, response)
	if !errors.Is(err, errEmptyUpstreamStream) {
		t.Fatalf("error = %v, want errEmptyUpstreamStream so the caller can retry", err)
	}
	if writer.buf.Len() != 0 {
		t.Fatalf("startup-only stream leaked bytes to the client: %q", writer.buf.String())
	}
	if writer.written {
		t.Fatal("client writer was used before real output arrived")
	}
}

func TestHandleStreamResponseFlushesStartupBeforeTerminal(t *testing.T) {
	// 内容与终态在同一批里到达: 先补发启动事件与内容, 再结束。
	rawSSE := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"quick"}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")

	ctx := context.Background()
	writer := &notifyStreamWriter{header: http.Header{}}
	ra := newConvertedStreamAttempt(inbound.InboundTypeOpenAIChat, outbound.OutboundTypeAnthropic, writer)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(rawSSE))),
	}

	if err := ra.handleStreamResponse(ctx, response); err != nil {
		t.Fatalf("handleStreamResponse error = %v", err)
	}
	if !strings.Contains(writer.buf.String(), "quick") {
		t.Fatalf("content missing from client stream: %q", writer.buf.String())
	}
}
