package handlers

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
)

func newStreamLogTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/log/stream", streamLog)
	return router
}

func TestStreamLogSendsAndFlushesInitialComment(t *testing.T) {
	server := httptest.NewServer(newStreamLogTestRouter())
	defer server.Close()

	token, err := op.RelayLogStreamTokenCreate()
	if err != nil {
		t.Fatalf("create stream token: %v", err)
	}

	resp, err := http.Get(server.URL + "/api/v1/log/stream?token=" + token)
	if err != nil {
		t.Fatalf("open log stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type = %q, want text/event-stream", got)
	}

	// 没有新日志时, 服务端也必须先写一帧注释并刷出, 否则响应头不会发出,
	// 客户端的 EventSource 会一直停在 CONNECTING。
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("read initial frame: %v", err)
	}
	if strings.TrimSpace(line) != ": connected" {
		t.Errorf("initial frame = %q, want a flush marker", line)
	}
}

func TestStreamLogRejectsUnknownToken(t *testing.T) {
	server := httptest.NewServer(newStreamLogTestRouter())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/log/stream?token=not-a-token")
	if err != nil {
		t.Fatalf("open log stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestStreamLogTokenIsSingleUse(t *testing.T) {
	server := httptest.NewServer(newStreamLogTestRouter())
	defer server.Close()

	token, err := op.RelayLogStreamTokenCreate()
	if err != nil {
		t.Fatalf("create stream token: %v", err)
	}

	first, err := http.Get(server.URL + "/api/v1/log/stream?token=" + token)
	if err != nil {
		t.Fatalf("open log stream: %v", err)
	}
	_ = first.Body.Close()

	second, err := http.Get(server.URL + "/api/v1/log/stream?token=" + token)
	if err != nil {
		t.Fatalf("reopen log stream: %v", err)
	}
	defer func() { _ = second.Body.Close() }()

	if second.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a replayed token", second.StatusCode)
	}
}

func TestWriteSSECommentFlushes(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	if err := writeSSEComment(c, "ping"); err != nil {
		t.Fatalf("writeSSEComment failed: %v", err)
	}
	if got := recorder.Body.String(); got != ": ping\n\n" {
		t.Errorf("body = %q, want a comment frame", got)
	}
	if !recorder.Flushed {
		t.Error("comment frame was written without flushing")
	}
}
