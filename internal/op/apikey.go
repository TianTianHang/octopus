package op

import (
	"context"
	"fmt"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

var apiKeyCache = cache.New[int, model.APIKey](16)
var apiKeyIDMap = cache.New[string, int](16)

const (
	// apiKeyValueMaxLength 限制自定义 key 的总长度，避免把超长字符串写进每个请求的日志与配置。
	apiKeyValueMaxLength = 128
	// apiKeyValueMinSuffix 是前缀之后至少需要的字符数，过短的 key 与自动生成的水平差距太大。
	apiKeyValueMinSuffix = 8
)

// APIKeyValuePrefix 是 API Key 必须携带的固定前缀。
// 认证中间件按它快速拒绝明显无效的 key，自定义 key 也必须遵守，否则存了也用不了。
func APIKeyValuePrefix() string {
	return "sk-" + conf.APP_NAME + "-"
}

// APIKeyValidateValue 校验一个 API Key 值是否可以保存; excludeID 是正在编辑的 key ID，创建时传 0。
func APIKeyValidateValue(value string, excludeID int) error {
	if value == "" {
		return fmt.Errorf("api key must not be empty")
	}
	prefix := APIKeyValuePrefix()
	if !strings.HasPrefix(value, prefix) {
		return fmt.Errorf("api key must start with %q", prefix)
	}
	if len(value) > apiKeyValueMaxLength {
		return fmt.Errorf("api key must be at most %d characters", apiKeyValueMaxLength)
	}
	if len(value)-len(prefix) < apiKeyValueMinSuffix {
		return fmt.Errorf("api key must have at least %d characters after the %q prefix", apiKeyValueMinSuffix, prefix)
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("api key must not contain whitespace")
	}
	if id, ok := apiKeyIDMap.Get(value); ok && id != excludeID {
		return fmt.Errorf("api key already exists")
	}
	return nil
}

func APIKeyCreate(key *model.APIKey, ctx context.Context) error {
	key.APIKey = strings.TrimSpace(key.APIKey)
	if err := APIKeyValidateValue(key.APIKey, 0); err != nil {
		return err
	}
	if err := db.GetDB().WithContext(ctx).Create(key).Error; err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}
	apiKeyCache.Set(key.ID, *key)
	apiKeyIDMap.Set(key.APIKey, key.ID)
	return nil
}

func APIKeyUpdate(key *model.APIKey, ctx context.Context) error {
	existing, ok := apiKeyCache.Get(key.ID)
	if !ok {
		return fmt.Errorf("API key not found")
	}
	// 空值表示沿用原 key: 编辑表单不必回传完整的密钥。
	key.APIKey = strings.TrimSpace(key.APIKey)
	if key.APIKey == "" {
		key.APIKey = existing.APIKey
	} else if key.APIKey != existing.APIKey {
		if err := APIKeyValidateValue(key.APIKey, key.ID); err != nil {
			return err
		}
	}
	// 允许改写 api_key 列, 因此不能再 Omit("api_key")。
	if err := db.GetDB().WithContext(ctx).Save(key).Error; err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}
	if key.APIKey != existing.APIKey {
		// 认证走 value -> ID 的索引, 改值后必须同步换向, 否则旧 key 仍能认证、新 key 无法认证。
		apiKeyIDMap.Del(existing.APIKey)
		apiKeyIDMap.Set(key.APIKey, key.ID)
	}
	apiKeyCache.Set(key.ID, *key)
	return nil
}

func APIKeyList(ctx context.Context) ([]model.APIKey, error) {
	keys := make([]model.APIKey, 0, apiKeyCache.Len())
	for _, apiKey := range apiKeyCache.GetAll() {
		keys = append(keys, apiKey)
	}
	return keys, nil
}

func APIKeyGet(id int, ctx context.Context) (model.APIKey, error) {
	apiKey, ok := apiKeyCache.Get(id)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return apiKey, nil
}

func APIKeyGetByAPIKey(apiKey string, ctx context.Context) (model.APIKey, error) {
	id, ok := apiKeyIDMap.Get(apiKey)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return APIKeyGet(id, ctx)
}

func APIKeyDelete(id int, ctx context.Context) error {
	k := model.APIKey{
		ID: id,
	}
	if err := StatsAPIKeyDel(id); err != nil {
		return fmt.Errorf("failed to delete stats API key: %v", err)
	}
	result := db.GetDB().WithContext(ctx).Delete(&k)
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	if result.Error != nil {
		return fmt.Errorf("failed to delete API key: %w", result.Error)
	}
	RateLimitDel(id)
	apiKeyCache.Del(k.ID)
	apiKeyIDMap.Del(k.APIKey)
	return nil
}

func apiKeyRefreshCache(ctx context.Context) error {
	apiKeys := []model.APIKey{}
	if err := db.GetDB().WithContext(ctx).Find(&apiKeys).Error; err != nil {
		return err
	}
	for _, apiKey := range apiKeys {
		apiKeyCache.Set(apiKey.ID, apiKey)
		apiKeyIDMap.Set(apiKey.APIKey, apiKey.ID)
	}
	return nil
}
