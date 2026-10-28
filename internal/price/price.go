package price

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/client"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/log"
)

const llmPriceUrl = "https://models.dev/api.json"

// llmPriceUserAgent 是价格数据源要求的 UA: 空 UA 与 Go 默认 UA 都会被数据源拒绝。
const llmPriceUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

// fetchLLMPriceBody 拉取价格数据源正文; useProxy 决定是否走设置中的代理。
// 这里只负责单次抓取并把错误原样返回, 直连与代理的取舍留给调用方。
func fetchLLMPriceBody(ctx context.Context, url string, useProxy bool) ([]byte, error) {
	httpClient, err := client.GetHTTPClientSystemProxy(useProxy)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", llmPriceUserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, nil
}

// parseLLMPrice 把数据源正文解析成 "模型名 -> 价格" 的映射。
// 只保留 Provider 列表中的厂商: 数据源收录了数百个服务商, 全量载入会让未知厂商的模型套用错误价格。
func parseLLMPrice(body []byte) (map[string]model.LLMPrice, error) {
	var rawPrice map[string]struct {
		Models map[string]struct {
			ID   string         `json:"id"`
			Cost model.LLMPrice `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &rawPrice); err != nil {
		return nil, fmt.Errorf("failed to parse LLM info: %w", err)
	}
	prices := make(map[string]model.LLMPrice)
	for _, provider := range Provider {
		for _, item := range rawPrice[provider].Models {
			prices[strings.ToLower(item.ID)] = item.Cost
		}
	}
	return prices, nil
}

var Provider = []string{
	"openai",     // GPT 系列
	"anthropic",  // Claude 系列
	"google",     // Gemini 系列
	"deepseek",   // DeepSeek 系列
	"xai",        // Grok 系列
	"alibaba",    // Qwen 系列
	"zhipuai",    // GLM 系列
	"minimax",    // MiniMax 系列
	"moonshotai", // Kimi/Moonshot
	"v0",         // v0 系列
}

var lastUpdateTime time.Time

func UpdateLLMPrice(ctx context.Context) error {
	log.Debugf("update LLM price task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("update LLM price task finished, update time: %s", time.Since(startTime))
	}()
	// 先直连; 失败再走代理。国内网络下数据源常被墙, 而直连对无代理环境仍是首选,
	// 故不能无条件走代理(未配置代理时 GetHTTPClientSystemProxy(true) 会直接报错)。
	body, err := fetchLLMPriceBody(ctx, llmPriceUrl, false)
	if err != nil {
		log.Warnf("direct request failed, retrying with proxy: %v", err)
		if body, err = fetchLLMPriceBody(ctx, llmPriceUrl, true); err != nil {
			return err
		}
	}
	prices, err := parseLLMPrice(body)
	if err != nil {
		return err
	}
	llmPriceLock.Lock()
	for modelName, modelPrice := range prices {
		llmPrice[modelName] = modelPrice
	}
	llmPriceLock.Unlock()
	lastUpdateTime = time.Now()
	return nil
}

func GetLastUpdateTime() time.Time {
	return lastUpdateTime
}

func GetLLMPrice(modelName string) *model.LLMPrice {
	modelName = strings.ToLower(modelName)
	price, err := op.LLMGet(modelName)
	if err == nil {
		return &price
	}
	llmPriceLock.RLock()
	defer llmPriceLock.RUnlock()
	price, ok := llmPrice[modelName]
	if !ok {
		return nil
	}
	return &price
}
