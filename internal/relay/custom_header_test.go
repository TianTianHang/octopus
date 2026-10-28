package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/gin-gonic/gin"
)

func TestResolveCustomHeaderValue(t *testing.T) {
	client := http.Header{}
	client.Set("X-Request-Id", "req-123")
	client.Set("X-Tenant", "acme")

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "literal value is untouched", value: "static-value", want: "static-value"},
		{name: "single placeholder", value: "{client_header:X-Request-Id}", want: "req-123"},
		{name: "placeholder inside a prefix", value: "Bearer {client_header:X-Tenant}", want: "Bearer acme"},
		{name: "case insensitive name", value: "{client_header:x-request-id}", want: "req-123"},
		{name: "multiple placeholders", value: "{client_header:X-Tenant}:{client_header:X-Request-Id}", want: "acme:req-123"},
		{name: "missing client header resolves empty", value: "prefix-{client_header:X-Missing}", want: "prefix-"},
		{name: "unclosed placeholder stays literal", value: "{client_header:X-Request-Id", want: "{client_header:X-Request-Id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveCustomHeaderValue(tc.value, client); got != tc.want {
				t.Fatalf("resolveCustomHeaderValue(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestResolveCustomHeaderValueHandlesNilHeader(t *testing.T) {
	if got := resolveCustomHeaderValue("{client_header:X-Request-Id}", nil); got != "" {
		t.Fatalf("nil client header resolved to %q, want empty", got)
	}
	if got := resolveCustomHeaderValue("static", nil); got != "static" {
		t.Fatalf("literal value with nil header = %q", got)
	}
}

func TestCopyHeadersAppliesResolvedCustomHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Client-Trace", "trace-1")

	ra := &relayAttempt{
		relayRequest: &relayRequest{c: c},
		channel: &dbmodel.Channel{CustomHeader: []dbmodel.CustomHeader{
			{HeaderKey: "X-Upstream-Trace", HeaderValue: "trace:{client_header:X-Client-Trace}"},
			{HeaderKey: "", HeaderValue: "ignored"},
		}},
	}

	upstream := httptest.NewRequest(http.MethodPost, "https://upstream.example", nil)
	ra.copyHeaders(upstream)

	if got := upstream.Header.Get("X-Upstream-Trace"); got != "trace:trace-1" {
		t.Fatalf("upstream header = %q, want the resolved client header", got)
	}
}
