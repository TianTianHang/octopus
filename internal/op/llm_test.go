package op

import (
	"context"
	"testing"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

func TestLLMBatchCreateSkipsRowsInsertedConcurrently(t *testing.T) {
	setupOpTestDB(t)

	previousCache := llmModelCache
	t.Cleanup(func() { llmModelCache = previousCache })

	rows := []model.LLMInfo{{Name: "GPT-X", LLMPrice: model.LLMPrice{Input: 1}}}
	if err := LLMBatchCreate(rows, context.Background()); err != nil {
		t.Fatalf("first LLMBatchCreate failed: %v", err)
	}

	// 清空进程内缓存, 模拟另一个同步已经写入数据库、本进程还没来得及看到的并发窗口;
	// 此时再插同一模型名过去会触发主键冲突并让整批失败。
	llmModelCache = cache.New[string, model.LLMPrice](16)
	duplicate := []model.LLMInfo{{Name: "gpt-x", LLMPrice: model.LLMPrice{Input: 2}}}
	if err := LLMBatchCreate(duplicate, context.Background()); err != nil {
		t.Fatalf("conflicting batch must be tolerated, got: %v", err)
	}

	var count int64
	if err := dbpkg.GetDB().Model(&model.LLMInfo{}).Count(&count).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	if count != 1 {
		t.Errorf("llm info rows = %d, want 1 (conflicting row must not be inserted twice)", count)
	}
}
