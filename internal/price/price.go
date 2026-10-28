package price

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
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
//
// 同一厂商下还要做两层过滤:
//   - 只接受带文本输出的模型。图片/音频/视频模型的计价单位与 token 不同, 套用文本价格会算错成本。
//   - 只接受该厂商自研的模型系列。聚合厂商(如 alibaba)同时托管 glm、deepseek、kimi,
//     按托管厂商的价格计费会让这些模型的价格偏离真实值, 它们应来自各自的厂商节点。
//
// 嵌入模型保留: 与本仓库的文本模型一样按 token 计价, 转发链路也会照常计费。
func parseLLMPrice(body []byte) (map[string]model.LLMPrice, error) {
	var rawPrice map[string]struct {
		Models map[string]struct {
			ID         string `json:"id"`
			Family     string `json:"family"`
			Modalities struct {
				Output []string `json:"output"`
			} `json:"modalities"`
			Cost model.LLMPrice `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &rawPrice); err != nil {
		return nil, fmt.Errorf("failed to parse LLM info: %w", err)
	}
	prices := make(map[string]model.LLMPrice)
	for _, provider := range Provider {
		familyPrefixes := developerFamilies[provider]
		for _, item := range rawPrice[provider].Models {
			modelID := strings.ToLower(item.ID)
			if modelID == "" || !slices.Contains(item.Modalities.Output, "text") {
				continue
			}
			// 未配置系列前缀的厂商不做系列过滤, 避免新增厂商时把模型全丢掉。
			if len(familyPrefixes) > 0 && !hasAnyPrefix(strings.ToLower(item.Family), familyPrefixes) {
				continue
			}
			prices[modelID] = item.Cost
		}
	}
	return prices, nil
}

// hasAnyPrefix 判断 value 是否以 prefixes 中的任一项开头。
func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// Provider 是参与价格同步的厂商, 同时也是遍历顺序。
// 顺序固定, 避免同一个模型被多个厂商收录时(例如 glm 同时挂在 zhipuai 与 alibaba 下)
// 价格随 map 遍历顺序抖动。
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
	"xiaomi",     // MiMo 系列
}

// developerFamilies 定义各厂商自研模型的 family 前缀, 用于把聚合厂商托管的第三方模型挡在外面。
var developerFamilies = map[string][]string{
	"openai":     {"gpt", "o", "text-embedding"},
	"anthropic":  {"claude"},
	"google":     {"gemini", "gemma", "lyria", "veo"},
	"deepseek":   {"deepseek"},
	"xai":        {"grok"},
	"alibaba":    {"qwen", "qvq"},
	"zhipuai":    {"glm"},
	"minimax":    {"minimax"},
	"moonshotai": {"kimi"},
	"v0":         {"v0"},
	"xiaomi":     {"mimo"},
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
