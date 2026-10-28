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
		"openai": {"models": {"GPT-4O": {"id": "GPT-4O", "family": "gpt", "modalities": {"output": ["text"]}, "cost": {"input": 1.5, "output": 6, "cache_read": 0.75}}}},
		"anthropic": {"models": {"claude-sonnet-4": {"id": "claude-sonnet-4", "family": "claude-sonnet", "modalities": {"output": ["text"]}, "cost": {"input": 3, "output": 15}}}},
		"acme": {"models": {"acme-model": {"id": "acme-model", "family": "acme", "modalities": {"output": ["text"]}, "cost": {"input": 99, "output": 99}}}}
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

func TestParseLLMPriceKeepsOnlyTextOutputAndOwnFamilies(t *testing.T) {
	body := []byte(`{
		"openai": {"models": {
			"gpt-4o": {"id": "gpt-4o", "family": "gpt", "modalities": {"output": ["text"]}, "cost": {"input": 1}},
			"gpt-image-1": {"id": "gpt-image-1", "family": "gpt-image", "modalities": {"output": ["image"]}, "cost": {"input": 40}},
			"text-embedding-3-small": {"id": "text-embedding-3-small", "family": "text-embedding", "modalities": {"output": ["text"]}, "cost": {"input": 0.02}}
		}},
		"google": {"models": {
			"gemini-embedding-001": {"id": "gemini-embedding-001", "family": "gemini", "modalities": {"output": ["text"]}, "cost": {"input": 0.15}},
			"gemini-2.5-flash-tts": {"id": "gemini-2.5-flash-tts", "family": "gemini-flash", "modalities": {"output": ["audio"]}, "cost": {"input": 1}}
		}},
		"alibaba": {"models": {
			"qwen-max": {"id": "qwen-max", "family": "qwen", "modalities": {"output": ["text"]}, "cost": {"input": 2}},
			"glm-5": {"id": "glm-5", "family": "glm", "modalities": {"output": ["text"]}, "cost": {"input": 0.6}}
		}},
		"zhipuai": {"models": {
			"glm-5": {"id": "glm-5", "family": "glm", "modalities": {"output": ["text"]}, "cost": {"input": 0.7}}
		}},
		"xiaomi": {"models": {
			"mimo-v2.6-pro": {"id": "mimo-v2.6-pro", "family": "mimo", "modalities": {"output": ["text"]}, "cost": {"input": 1.1}}
		}}
	}`)

	prices, err := parseLLMPrice(body)
	if err != nil {
		t.Fatalf("parseLLMPrice() error = %v", err)
	}

	// 图片/音频等非文本输出按 token 计价没有意义, 必须丢弃。
	for _, dropped := range []string{"gpt-image-1", "gemini-2.5-flash-tts"} {
		if _, ok := prices[dropped]; ok {
			t.Errorf("%s has non-text output and must not receive a token price", dropped)
		}
	}
	// 嵌入模型与文本模型一样按 token 计价, 保留。
	for _, kept := range []string{"gpt-4o", "text-embedding-3-small", "gemini-embedding-001", "qwen-max", "mimo-v2.6-pro"} {
		if _, ok := prices[kept]; !ok {
			t.Errorf("%s must be priced", kept)
		}
	}
	// glm-5 同时挂在 alibaba 与 zhipuai 下; 只接受自研厂商的价格。
	if got := prices["glm-5"]; got.Input != 0.7 {
		t.Errorf("glm-5 price = %+v, want the zhipuai price 0.7", got)
	}
}

func TestParseLLMPriceInvalidJSON(t *testing.T) {
	if _, err := parseLLMPrice([]byte(`{`)); err == nil {
		t.Fatal("parseLLMPrice() error = nil, want parse error")
	}
}
