package op

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func ensureRelayLogIndexesForTest(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := RelayLogEnsureIndexesSync(ctx); err != nil {
		t.Fatalf("RelayLogEnsureIndexesSync failed: %v", err)
	}
}

func resetRelayLogStateForTest() {
	relayLogPendingLock.Lock()
	relayLogPending = make([]model.RelayLog, 0, relayLogBatchSize)
	relayLogPendingBytes = 0
	relayLogPendingLock.Unlock()

	relayLogRecentLock.Lock()
	relayLogRecent = make([]model.RelayLog, 0, relayLogRecentMaxSize)
	relayLogRecentLock.Unlock()

	relayLogDroppedTotal.Store(0)
	relayLogLastDropWarn.Store(0)
	for {
		select {
		case <-relayLogFlushSignal:
		default:
			return
		}
	}
}

func TestRelayLogAddQueuesWithoutDBWrite(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	logCount := relayLogBatchSize + 5
	for i := 0; i < logCount; i++ {
		if err := RelayLogAdd(ctx, model.RelayLog{Time: time.Now().Unix(), RequestModelName: "gpt-4o-mini", Success: true}); err != nil {
			t.Fatalf("RelayLogAdd failed: %v", err)
		}
	}

	if got := RelayLogPendingLen(); got != logCount {
		t.Fatalf("expected pending logs to stay queued, got %d", got)
	}
	var dbCount int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.RelayLog{}).Count(&dbCount).Error; err != nil {
		t.Fatalf("count relay logs failed: %v", err)
	}
	if dbCount != 0 {
		t.Fatalf("RelayLogAdd wrote to DB synchronously, db rows=%d", dbCount)
	}
}

func TestRelayLogListDefaultsToLightFieldsAndNoContentKeyword(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	rows := []model.RelayLog{
		{ID: 101, Time: 101, RequestModelName: "gpt-visible", RequestAPIKeyName: "key-a", ChannelId: 1, ChannelName: "primary", ActualModelName: "gpt-visible", RequestContent: "hidden-needle", ResponseContent: "hidden-response", Success: true},
		{ID: 102, Time: 102, RequestModelName: "claude", RequestAPIKeyName: "key-b", ChannelId: 1, ChannelName: "secondary", ActualModelName: "claude", Error: "visible failure", RequestContent: "plain", Success: false},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	result, err := RelayLogListWithFilter(ctx, RelayLogListFilter{Page: 1, PageSize: 10, WithTotal: true})
	if err != nil {
		t.Fatalf("RelayLogListWithFilter failed: %v", err)
	}
	if result.Total != 2 || len(result.Logs) != 2 {
		t.Fatalf("unexpected list result: %+v", result)
	}
	for _, item := range result.Logs {
		if item.RequestContent != "" || item.ResponseContent != "" {
			t.Fatalf("expected list to omit content fields by default, got %+v", item)
		}
	}

	contentResult, err := RelayLogListWithFilter(ctx, RelayLogListFilter{Keyword: "hidden-needle", Page: 1, PageSize: 10, WithTotal: true})
	if err != nil {
		t.Fatalf("RelayLogListWithFilter keyword failed: %v", err)
	}
	if contentResult.Total != 0 || len(contentResult.Logs) != 0 {
		t.Fatalf("default keyword unexpectedly searched content: %+v", contentResult)
	}

	contentResult, err = RelayLogListWithFilter(ctx, RelayLogListFilter{Keyword: "hidden-needle", KeywordScope: RelayLogKeywordScopeContent, StartTime: intPtr(0), EndTime: intPtr(200), Page: 1, PageSize: 10, WithTotal: true})
	if err != nil {
		t.Fatalf("RelayLogListWithFilter content keyword failed: %v", err)
	}
	if contentResult.Total != 1 || len(contentResult.Logs) != 1 || contentResult.Logs[0].ID != 101 {
		t.Fatalf("content keyword did not find expected row: %+v", contentResult)
	}
}

func intPtr(v int) *int { return &v }

func TestRelayLogListContainsKeywordRequiresMinLength(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	_, err := RelayLogListWithFilter(ctx, RelayLogListFilter{Keyword: "ab", KeywordMode: RelayLogKeywordModeContains, Page: 1, PageSize: 10})
	if !errors.Is(err, ErrRelayLogContainsKeywordTooShort) {
		t.Fatalf("expected ErrRelayLogContainsKeywordTooShort, got %v", err)
	}
}

func TestRelayLogListPrefixKeywordMatchesModelPrefix(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	rows := []model.RelayLog{
		{ID: 401, Time: 401, RequestModelName: "gpt-4o-mini", ChannelName: "primary", Success: true},
		{ID: 402, Time: 402, RequestModelName: "claude-3", ChannelName: "secondary", Success: false},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	result, err := RelayLogListWithFilter(ctx, RelayLogListFilter{Keyword: "gpt", Page: 1, PageSize: 10, WithTotal: true})
	if err != nil {
		t.Fatalf("prefix search failed: %v", err)
	}
	if result.Total != 1 || len(result.Logs) != 1 || result.Logs[0].ID != 401 {
		t.Fatalf("prefix search returned unexpected rows: %+v", result)
	}
	if result.SearchMode != "fast" {
		t.Fatalf("expected SearchMode=fast, got %q", result.SearchMode)
	}
}

func TestRelayLogListCursorReturnsNextCursorWithoutTotal(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	rows := []model.RelayLog{
		{ID: 201, Time: 201, RequestModelName: "a", Success: true},
		{ID: 202, Time: 202, RequestModelName: "b", Success: true},
		{ID: 203, Time: 203, RequestModelName: "c", Success: true},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	first, err := RelayLogListWithFilter(ctx, RelayLogListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("RelayLogListWithFilter cursor failed: %v", err)
	}
	if first.Total != 0 || !first.HasMore || first.NextCursor == nil || len(first.Logs) != 2 || first.Logs[0].ID != 203 || first.Logs[1].ID != 202 {
		t.Fatalf("unexpected first cursor page: %+v", first)
	}
	second, err := RelayLogListWithFilter(ctx, RelayLogListFilter{Limit: 2, BeforeTime: &first.NextCursor.Time, BeforeID: &first.NextCursor.ID})
	if err != nil {
		t.Fatalf("RelayLogListWithFilter second cursor failed: %v", err)
	}
	if second.HasMore || second.NextCursor != nil || len(second.Logs) != 1 || second.Logs[0].ID != 201 {
		t.Fatalf("unexpected second cursor page: %+v", second)
	}
}

func TestRelayLogGetReturnsFullContent(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	row := model.RelayLog{ID: 301, Time: 301, RequestModelName: "gpt", RequestContent: "full request", ResponseContent: "full response", Success: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("create relay log failed: %v", err)
	}

	got, err := RelayLogGet(ctx, row.ID)
	if err != nil {
		t.Fatalf("RelayLogGet failed: %v", err)
	}
	if got.RequestContent != row.RequestContent || got.ResponseContent != row.ResponseContent {
		t.Fatalf("expected full content, got %+v", got)
	}
}

func TestRelayLogFlushPendingPersistsQueuedLogs(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	for i := 0; i < 3; i++ {
		if err := RelayLogAdd(ctx, model.RelayLog{Time: int64(100 + i), RequestModelName: "model", Success: true}); err != nil {
			t.Fatalf("RelayLogAdd failed: %v", err)
		}
	}
	if err := RelayLogFlushPending(ctx); err != nil {
		t.Fatalf("RelayLogFlushPending failed: %v", err)
	}
	if got := RelayLogPendingLen(); got != 0 {
		t.Fatalf("expected pending queue to be empty, got %d", got)
	}
	var dbCount int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.RelayLog{}).Count(&dbCount).Error; err != nil {
		t.Fatalf("count relay logs failed: %v", err)
	}
	if dbCount != 3 {
		t.Fatalf("expected 3 persisted logs, got %d", dbCount)
	}
}

func TestRelayLogCleanupTreatsZeroKeepPeriodAsDefault(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyRelayLogKeepPeriod, "0"); err != nil {
		t.Fatalf("SettingSetString failed: %v", err)
	}

	now := time.Now().Unix()
	rows := []model.RelayLog{
		{ID: 1, Time: now - int64(model.RelayLogDefaultKeepPeriodDays+1)*24*60*60, RequestModelName: "expired", Success: true},
		{ID: 2, Time: now - 60, RequestModelName: "recent", Success: true},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	ensureRelayLogIndexesForTest(t, ctx)
	if err := relayLogCleanup(ctx); err != nil {
		t.Fatalf("relayLogCleanup failed: %v", err)
	}

	var remaining []model.RelayLog
	if err := dbpkg.GetDB().WithContext(ctx).Order("id ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("query relay logs failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != 2 {
		t.Fatalf("expected only the recent log to remain, got %+v", remaining)
	}
}

func TestRelayLogCleanupDeletesRowsOlderThanKeepPeriod(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyRelayLogKeepPeriod, "2"); err != nil {
		t.Fatalf("SettingSetString failed: %v", err)
	}

	now := time.Now().Unix()
	rows := []model.RelayLog{
		{ID: 11, Time: now - 3*24*60*60, RequestModelName: "expired", Success: true},
		{ID: 12, Time: now - 12*60*60, RequestModelName: "recent", Success: true},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	ensureRelayLogIndexesForTest(t, ctx)
	if err := relayLogCleanup(ctx); err != nil {
		t.Fatalf("relayLogCleanup failed: %v", err)
	}

	var remaining []model.RelayLog
	if err := dbpkg.GetDB().WithContext(ctx).Order("id ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("query relay logs failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != 12 {
		t.Fatalf("expected only the recent log to remain, got %+v", remaining)
	}
}

func TestRelayLogCleanupTrimsToMaxSize(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	// 保留期设得足够长，确保删除只来自容量上限，而不是按天清理。
	if err := SettingSetString(model.SettingKeyRelayLogKeepPeriod, "3650"); err != nil {
		t.Fatalf("SettingSetString keep period failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyRelayLogMaxSizeMB, "1"); err != nil {
		t.Fatalf("SettingSetString max size failed: %v", err)
	}

	payload := strings.Repeat("x", 50*1024)
	now := time.Now().Unix()
	rows := make([]model.RelayLog, 0, 60)
	for i := 0; i < 60; i++ {
		rows = append(rows, model.RelayLog{
			ID:              int64(1000 + i),
			Time:            now - int64(i),
			RequestContent:  payload,
			ResponseContent: payload,
			Success:         true,
		})
	}
	if err := dbpkg.GetDB().WithContext(ctx).CreateInBatches(&rows, 20).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}
	beforeLive, err := dbpkg.LiveDataSize(ctx)
	if err != nil {
		t.Fatalf("LiveDataSize failed: %v", err)
	}
	if beforeLive <= 1<<20 {
		t.Fatalf("test data is too small: live size=%d", beforeLive)
	}

	ensureRelayLogIndexesForTest(t, ctx)
	if err := relayLogCleanup(ctx); err != nil {
		t.Fatalf("relayLogCleanup failed: %v", err)
	}

	afterLive, err := dbpkg.LiveDataSize(ctx)
	if err != nil {
		t.Fatalf("LiveDataSize failed: %v", err)
	}
	// 允许一个批次/估算误差的余量，但必须显著低于清理前并接近 1MB 上限。
	if afterLive > 2<<20 {
		t.Fatalf("live size after trim = %d, want <= 2MiB (before=%d)", afterLive, beforeLive)
	}
	var remaining int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.RelayLog{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count relay logs failed: %v", err)
	}
	if remaining == 0 {
		t.Fatalf("size cap deleted every log; expected recent rows to remain")
	}
}

func TestRelayLogCleanupSizeCapWorksWithoutTimeIndex(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyRelayLogKeepPeriod, "3650"); err != nil {
		t.Fatalf("SettingSetString keep period failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyRelayLogMaxSizeMB, "1"); err != nil {
		t.Fatalf("SettingSetString max size failed: %v", err)
	}
	ensureRelayLogIndexesForTest(t, ctx)
	if err := dbpkg.GetDB().Exec("DROP INDEX idx_relay_logs_time_id").Error; err != nil {
		t.Fatalf("drop time index failed: %v", err)
	}

	payload := strings.Repeat("x", 50*1024)
	now := time.Now().Unix()
	rows := make([]model.RelayLog, 0, 60)
	for i := 0; i < 60; i++ {
		rows = append(rows, model.RelayLog{
			ID:              int64(2000 + i),
			Time:            now - int64(i),
			RequestContent:  payload,
			ResponseContent: payload,
			Success:         true,
		})
	}
	if err := dbpkg.GetDB().WithContext(ctx).CreateInBatches(&rows, 20).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	if err := relayLogCleanup(ctx); err != nil {
		t.Fatalf("relayLogCleanup failed: %v", err)
	}

	afterLive, err := dbpkg.LiveDataSize(ctx)
	if err != nil {
		t.Fatalf("LiveDataSize failed: %v", err)
	}
	if afterLive > 2<<20 {
		t.Fatalf("live size after trim = %d, want <= 2MiB even without time index", afterLive)
	}
	var remaining int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.RelayLog{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count relay logs failed: %v", err)
	}
	if remaining == 0 {
		t.Fatalf("size cap deleted every log; expected recent rows to remain")
	}
}

func TestRelayLogClearDeletesExistingRows(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}

	now := time.Now().Unix()
	rows := []model.RelayLog{
		{ID: 201, Time: now - 3, RequestModelName: "a", Success: true},
		{ID: 202, Time: now - 2, RequestModelName: "b", Success: true},
		{ID: 203, Time: now - 1, RequestModelName: "c", Success: true},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	if err := RelayLogClear(ctx); err != nil {
		t.Fatalf("RelayLogClear failed: %v", err)
	}

	var remaining int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.RelayLog{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count relay logs failed: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected all relay logs to be cleared, got %d", remaining)
	}
}

func TestSettingRefreshCacheNormalizesInvalidRelayLogSettings(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyRelayLogKeepPeriod, "0"); err != nil {
		t.Fatalf("SettingSetString keep period failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyRelayLogMaxSizeMB, "-1"); err != nil {
		t.Fatalf("SettingSetString max size failed: %v", err)
	}
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache reload failed: %v", err)
	}
	keepPeriod, err := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		t.Fatalf("SettingGetInt keep period failed: %v", err)
	}
	if keepPeriod != model.RelayLogDefaultKeepPeriodDays {
		t.Fatalf("keep period = %d, want %d", keepPeriod, model.RelayLogDefaultKeepPeriodDays)
	}
	maxSize, err := SettingGetInt(model.SettingKeyRelayLogMaxSizeMB)
	if err != nil {
		t.Fatalf("SettingGetInt max size failed: %v", err)
	}
	if maxSize != model.RelayLogDefaultMaxSizeMB {
		t.Fatalf("max size = %d, want %d", maxSize, model.RelayLogDefaultMaxSizeMB)
	}
}

func TestRelayLogCleanupWithOptionsOverridesKeepPeriod(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	ensureRelayLogIndexesForTest(t, ctx)

	now := time.Now().Unix()
	rows := []model.RelayLog{
		{ID: 3001, Time: now - 2*24*60*60, RequestModelName: "expired", Success: true},
		{ID: 3002, Time: now - 60, RequestModelName: "recent", Success: true},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	result, err := RelayLogCleanupWithOptions(ctx, RelayLogCleanupOptions{
		KeepPeriodDays: 1,
		MaxSizeMB:      0, // 本次不启用容量上限
	})
	if err != nil {
		t.Fatalf("RelayLogCleanupWithOptions failed: %v", err)
	}
	if result.KeepPeriodDays != 1 || result.DeletedRows != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}

	var remaining []model.RelayLog
	if err := dbpkg.GetDB().WithContext(ctx).Order("id ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("query relay logs failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != 3002 {
		t.Fatalf("expected only the recent log to remain, got %+v", remaining)
	}
}

func TestRelayLogCleanupWithOptionsDryRunDoesNotDelete(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	ensureRelayLogIndexesForTest(t, ctx)

	now := time.Now().Unix()
	rows := []model.RelayLog{
		{ID: 3101, Time: now - 2*24*60*60, RequestModelName: "expired", Success: true},
		{ID: 3102, Time: now - 60, RequestModelName: "recent", Success: true},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	result, err := RelayLogCleanupWithOptions(ctx, RelayLogCleanupOptions{
		KeepPeriodDays: 1,
		MaxSizeMB:      0,
		DryRun:         true,
	})
	if err != nil {
		t.Fatalf("RelayLogCleanupWithOptions failed: %v", err)
	}
	if !result.DryRun || result.ExpiredRows != 1 || result.DeletedRows != 1 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}

	var count int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.RelayLog{}).Count(&count).Error; err != nil {
		t.Fatalf("count relay logs failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("dry-run deleted rows: remaining=%d, want 2", count)
	}
}
