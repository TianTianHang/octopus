package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/dlclark/regexp2"
)

const modelFetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"

// modelFilterMatchTimeout 限制单次过滤匹配的耗时。表达式由运维手写，
// 不能让一条回溯爆炸的正则拖死整个模型同步任务。
const modelFilterMatchTimeout = 2 * time.Second

func FetchModels(ctx context.Context, request model.Channel) ([]string, error) {
	client, err := ChannelHTTPClientWithContext(ctx, &request)
	if err != nil {
		return nil, err
	}
	fetchModel := make([]string, 0)
	switch request.Type {
	case outbound.OutboundTypeAnthropic:
		fetchModel, err = fetchAnthropicModels(client, ctx, request)
	case outbound.OutboundTypeGemini:
		fetchModel, err = fetchGeminiModels(client, ctx, request)
	default:
		fetchModel, err = fetchOpenAIModels(client, ctx, request)
	}
	if err != nil {
		return nil, err
	}
	channelFilter, err := compileModelFilter(request.MatchRegex)
	if err != nil {
		return nil, err
	}
	// 渠道级与全局过滤是 AND 关系：模型必须同时通过两者才保留，留空的一侧不生效。
	return filterModels(fetchModel, channelFilter, globalModelFilter())
}

// compileModelFilter 把过滤表达式编译成匹配器；空表达式返回 nil 表示不过滤。
func compileModelFilter(pattern *string) (*regexp2.Regexp, error) {
	if pattern == nil || strings.TrimSpace(*pattern) == "" {
		return nil, nil
	}
	re, err := regexp2.Compile(*pattern, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	re.MatchTimeout = modelFilterMatchTimeout
	return re, nil
}

// globalModelFilter 读取设置页维护的全局过滤表达式。
// 设置缺行或表达式非法时按不过滤处理并记一条警告：单条坏配置不应该让所有渠道的模型同步失败，
// 写入路径上的 Validate 已经拦住了新保存的非法值。
func globalModelFilter() *regexp2.Regexp {
	pattern, err := op.SettingGetString(model.SettingKeyModelFilter)
	if err != nil || strings.TrimSpace(pattern) == "" {
		return nil
	}
	re, err := compileModelFilter(&pattern)
	if err != nil {
		log.Warnf("invalid global model filter ignored: %v", err)
		return nil
	}
	return re
}

// filterModels 保留同时匹配全部过滤器的模型名；没有生效的过滤器时原样返回。
// 保持上游返回的顺序，界面上的模型排列才不会每次刷新都变。
func filterModels(models []string, filters ...*regexp2.Regexp) ([]string, error) {
	active := make([]*regexp2.Regexp, 0, len(filters))
	for _, re := range filters {
		if re != nil {
			active = append(active, re)
		}
	}
	if len(active) == 0 {
		return models, nil
	}
	matched := make([]string, 0, len(models))
	for _, name := range models {
		keep := true
		for _, re := range active {
			hit, err := re.MatchString(name)
			if err != nil {
				return nil, err
			}
			if !hit {
				keep = false
				break
			}
		}
		if keep {
			matched = append(matched, name)
		}
	}
	return matched, nil
}

// refer: https://platform.openai.com/docs/api-reference/models/list
func fetchOpenAIModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	req, _ := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		request.GetBaseUrl()+"/models",
		nil,
	)
	applyDefaultModelRequestHeaders(req, request)
	req.Header.Set("Authorization", "Bearer "+request.GetChannelKey().ChannelKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result model.OpenAIModelList
	if err := decodeModelJSONResponse(resp, &result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

// refer: https://ai.google.dev/api/models
func fetchGeminiModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	var allModels []string
	pageToken := ""

	for {
		req, _ := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			request.GetBaseUrl()+"/models",
			nil,
		)
		applyDefaultModelRequestHeaders(req, request)
		req.Header.Set("X-Goog-Api-Key", request.GetChannelKey().ChannelKey)
		if pageToken != "" {
			q := req.URL.Query()
			q.Add("pageToken", pageToken)
			req.URL.RawQuery = q.Encode()
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		var result model.GeminiModelList
		if err := decodeModelJSONResponse(resp, &result); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, m := range result.Models {
			name := strings.TrimPrefix(m.Name, "models/")
			allModels = append(allModels, name)
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	if len(allModels) == 0 {
		return fetchOpenAIModels(client, ctx, request)
	}
	return allModels, nil
}

// refer: https://platform.claude.com/docs
func fetchAnthropicModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	var allModels []string
	var afterID string
	for {
		req, _ := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			request.GetBaseUrl()+"/models",
			nil,
		)
		applyDefaultModelRequestHeaders(req, request)
		req.Header.Set("X-Api-Key", request.GetChannelKey().ChannelKey)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		q := req.URL.Query()
		if afterID != "" {
			q.Set("after_id", afterID)
		}
		req.URL.RawQuery = q.Encode()

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		var result model.AnthropicModelList
		if err := decodeModelJSONResponse(resp, &result); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, m := range result.Data {
			allModels = append(allModels, m.ID)
		}

		if !result.HasMore {
			break
		}

		afterID = result.LastID
	}
	if len(allModels) == 0 {
		return fetchOpenAIModels(client, ctx, request)
	}
	return allModels, nil
}

func applyDefaultModelRequestHeaders(req *http.Request, request model.Channel) {
	if req == nil {
		return
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", modelFetchUserAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json, text/plain, */*")
	}
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	}
	for _, header := range request.CustomHeader {
		if header.HeaderKey != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
}

func decodeModelJSONResponse(resp *http.Response, result any) error {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return formatModelHTTPError(resp.StatusCode, resp.Header.Get("Content-Type"), bodyBytes)
	}
	if len(bodyBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(bodyBytes, result); err != nil {
		if summary := extractModelHTMLResponseSummary(resp.Header.Get("Content-Type"), bodyBytes); summary != "" {
			return fmt.Errorf("decode response failed: %s", summary)
		}
		return fmt.Errorf("decode response failed: %w", err)
	}
	return nil
}

func formatModelHTTPError(statusCode int, contentType string, bodyBytes []byte) error {
	if payload, ok := parseModelErrorPayload(bodyBytes); ok {
		if message := extractModelErrorMessage(payload); message != "" {
			return fmt.Errorf("http %d: %s", statusCode, message)
		}
	}
	if summary := extractModelHTMLResponseSummary(contentType, bodyBytes); summary != "" {
		return fmt.Errorf("http %d: %s", statusCode, summary)
	}
	return fmt.Errorf("http %d: %s", statusCode, strings.TrimSpace(string(bodyBytes)))
}

func parseModelErrorPayload(bodyBytes []byte) (map[string]any, bool) {
	if len(bodyBytes) == 0 {
		return map[string]any{}, true
	}
	var payload map[string]any
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func extractModelErrorMessage(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if message, ok := payload["message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if message, ok := payload["msg"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if errorPayload, ok := payload["error"].(map[string]any); ok {
		if message, ok := errorPayload["message"].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	}
	return ""
}

func extractModelHTMLResponseSummary(contentType string, bodyBytes []byte) string {
	body := strings.TrimSpace(string(bodyBytes))
	if body == "" {
		return ""
	}
	lowered := strings.ToLower(body)
	loweredContentType := strings.ToLower(contentType)
	if !strings.Contains(loweredContentType, "text/html") && !strings.Contains(lowered, "<html") && !strings.Contains(lowered, "<!doctype") {
		if strings.Contains(lowered, "just a moment") {
			return "Just a moment..."
		}
		return ""
	}
	if start := strings.Index(lowered, "<title>"); start >= 0 {
		start += len("<title>")
		if end := strings.Index(lowered[start:], "</title>"); end >= 0 {
			title := strings.TrimSpace(body[start : start+end])
			if pipe := strings.Index(title, "|"); pipe >= 0 {
				title = strings.TrimSpace(title[:pipe])
			}
			if title != "" {
				return title
			}
		}
	}
	if strings.Contains(lowered, "just a moment") {
		return "Just a moment..."
	}
	if strings.Contains(lowered, "cloudflare tunnel error") {
		return "Cloudflare Tunnel error"
	}
	if strings.Contains(lowered, "cloudflare") {
		return "Cloudflare challenge"
	}
	return ""
}
