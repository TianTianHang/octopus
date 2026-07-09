package helper

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func requestBody(t *testing.T, req *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return string(body)
}

func TestApplyParamDropRemovesTopLevelKeys(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://upstream.test/v1/chat/completions", strings.NewReader(`{"model":"gpt","prompt_cache_key":"cache","messages":[],"store":true}`))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	drop := `["prompt_cache_key","store"]`

	if err := ApplyParamDrop(req, &drop); err != nil {
		t.Fatalf("ApplyParamDrop returned error: %v", err)
	}

	got := requestBody(t, req)
	if strings.Contains(got, "prompt_cache_key") || strings.Contains(got, "store") {
		t.Fatalf("expected dropped keys to be absent, got %s", got)
	}
	if !strings.Contains(got, `"model":"gpt"`) || !strings.Contains(got, `"messages":[]`) {
		t.Fatalf("expected remaining keys to be preserved, got %s", got)
	}
}

func TestApplyParamDropIgnoresInvalidConfigAndRestoresBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://upstream.test/v1/chat/completions", strings.NewReader(`{"model":"gpt","prompt_cache_key":"cache"}`))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	drop := `{"prompt_cache_key":true}`

	if err := ApplyParamDrop(req, &drop); err != nil {
		t.Fatalf("ApplyParamDrop returned error: %v", err)
	}

	if got := requestBody(t, req); got != `{"model":"gpt","prompt_cache_key":"cache"}` {
		t.Fatalf("expected original body to be restored, got %s", got)
	}
}
