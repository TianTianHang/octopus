package op

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/bestruirui/octopus/internal/utils/log"
)

var settingCache = cache.New[model.SettingKey, string](16)

func SettingList(ctx context.Context) ([]model.Setting, error) {
	settings := make([]model.Setting, 0, settingCache.Len())
	for key, value := range settingCache.GetAll() {
		settings = append(settings, model.Setting{
			Key:   key,
			Value: value,
		})
	}
	return settings, nil
}

func SettingGetString(key model.SettingKey) (string, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return "", fmt.Errorf("setting not found")
	}
	return setting, nil
}

func SettingSetString(key model.SettingKey, value string) error {
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("setting not found")
	}
	if valueCache == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("failed to set setting: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to set setting, key not found")
	}
	settingCache.Set(key, value)
	return nil
}

func SettingGetInt(key model.SettingKey) (int, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return 0, fmt.Errorf("setting not found")
	}
	return strconv.Atoi(setting)
}

func SettingGetBool(key model.SettingKey) (bool, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		for _, defaultSetting := range model.DefaultSettings() {
			if defaultSetting.Key == key {
				return strconv.ParseBool(defaultSetting.Value)
			}
		}
		return false, fmt.Errorf("setting not found")
	}
	return strconv.ParseBool(setting)
}

func SettingSetInt(key model.SettingKey, value int) error {
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("setting not found")
	}
	valueCacheNum, err := strconv.Atoi(valueCache)
	if err != nil {
		return fmt.Errorf("failed to set setting: %w", err)
	}
	if valueCacheNum == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("failed to set setting: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to set setting, key not found")
	}
	settingCache.Set(key, strconv.Itoa(value))
	return nil
}

func settingRefreshCache(ctx context.Context) error {
	db := db.GetDB().WithContext(ctx)

	var settings []model.Setting
	if err := db.Find(&settings).Error; err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	existingKeys := make(map[model.SettingKey]bool)
	for _, setting := range settings {
		existingKeys[setting.Key] = true
	}

	defaultSettings := model.DefaultSettings()
	missingSettings := make([]model.Setting, 0, len(defaultSettings))

	for _, defaultSetting := range defaultSettings {
		if !existingKeys[defaultSetting.Key] {
			missingSettings = append(missingSettings, defaultSetting)
		}
	}

	if len(missingSettings) > 0 {
		if err := db.CreateInBatches(missingSettings, len(missingSettings)).Error; err != nil {
			return fmt.Errorf("failed to create missing settings: %w", err)
		}
		settings = append(settings, missingSettings...)
	}
	for _, setting := range settings {
		// 旧版本/导入数据里可能存着 0 或越界值。运行时虽然会回退默认值，
		// 但这里顺手落库修正，让设置页展示和实际清理策略一致。
		switch setting.Key {
		case model.SettingKeyRelayLogKeepPeriod:
			value, err := strconv.Atoi(setting.Value)
			if err != nil || value < model.RelayLogMinKeepPeriodDays || value > model.RelayLogMaxKeepPeriodDays {
				setting.Value = strconv.Itoa(model.RelayLogDefaultKeepPeriodDays)
				if updateErr := db.Model(&model.Setting{}).Where("key = ?", setting.Key).Update("Value", setting.Value).Error; updateErr != nil {
					log.Warnf("normalize %s failed: %v", setting.Key, updateErr)
				}
			}
		case model.SettingKeyRelayLogMaxSizeMB:
			value, err := strconv.Atoi(setting.Value)
			if err != nil || value < 0 || value > model.RelayLogMaxSizeMBLimit {
				setting.Value = strconv.Itoa(model.RelayLogDefaultMaxSizeMB)
				if updateErr := db.Model(&model.Setting{}).Where("key = ?", setting.Key).Update("Value", setting.Value).Error; updateErr != nil {
					log.Warnf("normalize %s failed: %v", setting.Key, updateErr)
				}
			}
		}
		settingCache.Set(setting.Key, setting.Value)
	}
	return nil
}
