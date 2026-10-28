package relay

import (
	"net/http"
	"regexp"
	"strings"
)

// clientHeaderPlaceholderRe 匹配自定义 Header 值里的 {client_header:名称} 占位符。
// 名称按 http.Header 的语义大小写不敏感。
var clientHeaderPlaceholderRe = regexp.MustCompile(`\{client_header:([^}]+)\}`)

// resolveCustomHeaderValue 把渠道自定义 Header 值中的 {client_header:名称} 换成客户端请求头的实际值。
// 客户端没发该头时替换成空串, 便于用 "{client_header:X-Request-Id}" 这类写法透传可选头。
func resolveCustomHeaderValue(value string, clientHeader http.Header) string {
	if !strings.Contains(value, "{client_header:") {
		return value
	}
	return clientHeaderPlaceholderRe.ReplaceAllStringFunc(value, func(placeholder string) string {
		if clientHeader == nil {
			return ""
		}
		name := strings.TrimSpace(placeholder[len("{client_header:") : len(placeholder)-1])
		if name == "" {
			return ""
		}
		return clientHeader.Get(name)
	})
}
