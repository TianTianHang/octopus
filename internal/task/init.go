package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/utils/log"
)

const (
	TaskPriceUpdate       = "price_update"
	TaskStatsSave         = "stats_save"
	TaskRelayLogSave      = "relay_log_save"
	TaskSyncLLM           = "sync_llm"
	TaskCleanLLM          = "clean_llm"
	TaskBaseUrlDelay      = "base_url_delay"
	TaskSiteSync          = "site_sync"
	TaskSiteCheckin       = "site_checkin"
	TaskWSAffinityCleanup = "ws_affinity_cleanup"
)

// settingIntOr 读取设置，缺失/非法/负数时回退到 fallback。
// 0 会原样返回：对同步/统计任务来说 0 仍表示“禁用该任务”，
// 保持与旧版 Register(interval<=0) 的语义一致。
func settingIntOr(key model.SettingKey, fallback int) int {
	value, err := op.SettingGetInt(key)
	if err != nil {
		log.Warnf("failed to get setting %s: %v, using default %d", key, err, fallback)
		return fallback
	}
	if value < 0 {
		log.Warnf("invalid setting %s=%d, using default %d", key, value, fallback)
		return fallback
	}
	return value
}

func Init() {
	priceUpdateInterval := time.Duration(settingIntOr(model.SettingKeyModelInfoUpdateInterval, 24)) * time.Hour
	// 注册价格更新任务
	Register(string(model.SettingKeyModelInfoUpdateInterval), priceUpdateInterval, true, func() {
		if err := price.UpdateLLMPrice(context.Background()); err != nil {
			log.Warnf("failed to update price info: %v", err)
		}
	})

	// 注册基础URL延迟任务
	Register(TaskBaseUrlDelay, 24*time.Hour, true, ChannelBaseUrlDelayTask)

	// 注册LLM同步任务
	syncLLMInterval := time.Duration(settingIntOr(model.SettingKeySyncLLMInterval, 24)) * time.Hour
	Register(string(model.SettingKeySyncLLMInterval), syncLLMInterval, true, SyncModelsTask)

	siteSyncInterval := time.Duration(settingIntOr(model.SettingKeySiteSyncInterval, 12)) * time.Hour
	Register(string(model.SettingKeySiteSyncInterval), siteSyncInterval, true, SiteSyncTask)

	siteCheckinInterval := time.Duration(settingIntOr(model.SettingKeySiteCheckinInterval, 24)) * time.Hour
	Register(string(model.SettingKeySiteCheckinInterval), siteCheckinInterval, true, SiteCheckinTask)

	// 注册统计保存任务
	statsSaveInterval := time.Duration(settingIntOr(model.SettingKeyStatsSaveInterval, 10)) * time.Minute
	Register(TaskStatsSave, statsSaveInterval, false, op.StatsSaveDBTask)

	// 注册中继日志保存/清理任务。
	// 从每小时改为每 10 分钟，并 runOnStart：重启频繁或流量很大时，
	// 旧日志不会因为“还没到一小时”而持续堆积。
	Register(TaskRelayLogSave, 10*time.Minute, true, func() {
		if err := op.RelayLogSaveDBTask(context.Background()); err != nil {
			log.Warnf("relay log save db task failed: %v", err)
		}
	})

	Register(TaskWSAffinityCleanup, 10*time.Minute, true, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		deleted, err := op.WSResponseAffinityCleanup(ctx, time.Now())
		if err != nil {
			log.Warnf("ws response affinity cleanup failed: %v", err)
			return
		}
		if deleted > 0 {
			log.Debugf("ws response affinity cleanup removed %d expired rows", deleted)
		}
	})

	// 注册被动离群退役(POR)任务（默认间隔 2 分钟，总开关在任务内判断）
	outlierIntervalMinutes := settingIntOr(model.SettingKeyOutlierRetireInterval, 2)
	if outlierIntervalMinutes <= 0 {
		outlierIntervalMinutes = 2
	}
	Register(string(model.SettingKeyOutlierRetireInterval), time.Duration(outlierIntervalMinutes)*time.Minute, false, SiteOutlierRetireTask)
}
