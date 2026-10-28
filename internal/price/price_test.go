package price

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchLLMPriceBodySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != llmPriceUserAgent {
			t.Errorf("user agent = %q, want %q", got, llmPriceUserAgent)
		}
		_, _ = w.Write([]byte(`{"openai":{}}`))
	}))
	defer server.Close()

	body, err := fetchLLMPriceBody(context.Background(), server.URL, false)
	if err != nil {
		t.Fatalf("fetchLLMPriceBody() error = %v", err)
	}
	if string(body) != `{"openai":{}}` {
		t.Errorf("body = %q", body)
	}
}

func TestFetchLLMPriceBodyStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if _, err := fetchLLMPriceBody(context.Background(), server.URL, false); err == nil {
		t.Fatal("fetchLLMPriceBody() error = nil, want status error")
	}
}

func TestParseLLMPriceFiltersProvidersAndLowercases(t *testing.T) {
	body := []byte(`{
		"openai": {"models": {"GPT-4O": {"id": "GPT-4O", "cost": {"input": 1.5, "output": 6, "cache_read": 0.75}}}},
		"anthropic": {"models": {"claude-sonnet-4": {"id": "claude-sonnet-4", "cost": {"input": 3, "output": 15}}}},
		"acme": {"models": {"acme-model": {"id": "acme-model", "cost": {"input": 99, "output": 99}}}}
	}`)

	prices, err := parseLLMPrice(body)
	if err != nil {
		t.Fatalf("parseLLMPrice() error = %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("parseLLMPrice() returned %d models, want 2 (unknown providers must be dropped)", len(prices))
	}
	gpt, ok := prices["gpt-4o"]
	if !ok {
		t.Fatalf("model names must be lowercased, got %v", prices)
	}
	if gpt.Input != 1.5 || gpt.Output != 6 || gpt.CacheRead != 0.75 {
		t.Errorf("gpt-4o price = %+v", gpt)
	}
	if _, ok := prices["acme-model"]; ok {
		t.Error("model from an unlisted provider must be dropped")
	}
}

func TestParseLLMPriceInvalidJSON(t *testing.T) {
	if _, err := parseLLMPrice([]byte(`{`)); err == nil {
		t.Fatal("parseLLMPrice() error = nil, want parse error")
	}
}
