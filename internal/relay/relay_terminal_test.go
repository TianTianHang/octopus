package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

// newConvertedStreamAttempt 构造一个走"跨协议转换"路径的 attempt:
// 客户端协议由 inboundType 决定, 上游协议由 channelType 决定, 两者不同即不会走透传。
func newConvertedStreamAttempt(inboundType inbound.InboundType, channelType outbound.OutboundType, writer StreamWriter) *relayAttempt {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	internalReq := &model.InternalLLMRequest{Model: "test-model", Stream: boolPtr(true)}
	req := &relayRequest{
		c:               c,
		inAdapter:       inbound.Get(inboundType),
		internalRequest: internalReq,
		metrics:         NewRelayMetrics(1, internalReq.Model, nil, internalReq),
		apiKeyID:        1,
		requestModel:    internalReq.Model,
		streamWriter:    writer,
	}
	return &relayAttempt{
		relayRequest: req,
		outAdapter:   outbound.Get(channelType),
		channel:      &dbmodel.Channel{Name: "converted", Type: channelType},
	}
}

// runStreamWithDeadline 在后台执行流处理, 若上游在终态事件后仍不关连接, 超时即判定失败。
func runStreamWithDeadline(t *testing.T, ra *relayAttempt, ctx context.Context, body *stallUntilCancelBody) (string, error) {
	t.Helper()

	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}

	done := make(chan error, 1)
	go func() {
		done <- ra.handleStreamResponse(ctx, response)
	}()

	select {
	case err := <-done:
		return ra.getStreamWriter().(*notifyStreamWriter).buf.String(), err
	case <-time.After(3 * time.Second):
		t.Fatal("stream handler kept waiting for upstream EOF after a terminal event")
		return "", nil
	}
}

func TestHandleStreamResponseFinishesOnAnthropicTerminal(t *testing.T) {
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
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &notifyStreamWriter{header: http.Header{}}
	ra := newConvertedStreamAttempt(inbound.InboundTypeOpenAIChat, outbound.OutboundTypeAnthropic, writer)

	out, err := runStreamWithDeadline(t, ra, ctx, &stallUntilCancelBody{ctx: ctx, data: []byte(rawSSE)})
	if err != nil {
		t.Fatalf("terminal stream returned error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected converted chat output to contain the text delta, got %q", out)
	}
	if !ra.streamPayloadWritten.Load() {
		t.Error("payload flag not set for a stream that wrote content")
	}
}

func TestHandleStreamResponseFinishesOnChatDone(t *testing.T) {
	rawSSE := strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &notifyStreamWriter{header: http.Header{}}
	ra := newConvertedStreamAttempt(inbound.InboundTypeAnthropic, outbound.OutboundTypeOpenAIChat, writer)

	out, err := runStreamWithDeadline(t, ra, ctx, &stallUntilCancelBody{ctx: ctx, data: []byte(rawSSE)})
	if err != nil {
		t.Fatalf("terminal stream returned error: %v", err)
	}
	if !strings.Contains(out, "message_stop") {
		t.Fatalf("expected anthropic client output to end with message_stop, got %q", out)
	}
}

func TestUpstreamStreamTerminalEvent(t *testing.T) {
	cases := []struct {
		name        string
		channelType outbound.OutboundType
		data        string
		want        bool
	}{
		{name: "chat done", channelType: outbound.OutboundTypeOpenAIChat, data: " [DONE] ", want: true},
		{name: "chat content chunk", channelType: outbound.OutboundTypeOpenAIChat, data: `{"choices":[]}`},
		{name: "anthropic message_stop", channelType: outbound.OutboundTypeAnthropic, data: `{"type":"message_stop"}`, want: true},
		{name: "anthropic error", channelType: outbound.OutboundTypeAnthropic, data: `{"type":"error","error":{"type":"overloaded_error"}}`, want: true},
		{name: "anthropic delta is not terminal", channelType: outbound.OutboundTypeAnthropic, data: `{"type":"message_delta"}`},
		{name: "responses completed", channelType: outbound.OutboundTypeOpenAIResponse, data: `{"type":"response.completed"}`, want: true},
		{name: "responses created is not terminal", channelType: outbound.OutboundTypeOpenAIResponse, data: `{"type":"response.created"}`},
		// Gemini 只能等 EOF, 任何 data 都不算终态。
		{name: "gemini never terminal", channelType: outbound.OutboundTypeGemini, data: `{"candidates":[]}`},
		{name: "malformed json", channelType: outbound.OutboundTypeAnthropic, data: `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstreamStreamTerminalEvent(tc.channelType, tc.data); got != tc.want {
				t.Fatalf("upstreamStreamTerminalEvent(%v, %q) = %v, want %v", tc.channelType, tc.data, got, tc.want)
			}
		})
	}
}
