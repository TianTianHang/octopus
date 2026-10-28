package op

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

// setupAPIKeyTest 重置进程内缓存并准备一个干净的数据库，避免测试之间互相看到对方的 key。
func setupAPIKeyTest(t *testing.T) context.Context {
	t.Helper()
	setupOpTestDB(t)
	apiKeyCache = cache.New[int, model.APIKey](16)
	apiKeyIDMap = cache.New[string, int](16)
	return context.Background()
}

func TestAPIKeyCreateAcceptsCustomValue(t *testing.T) {
	ctx := setupAPIKeyTest(t)
	prefix := APIKeyValuePrefix()
	custom := prefix + "custom-value-1234"

	key := &model.APIKey{Name: "custom", APIKey: custom, Enabled: true}
	if err := APIKeyCreate(key, ctx); err != nil {
		t.Fatalf("APIKeyCreate(custom) failed: %v", err)
	}

	got, err := APIKeyGetByAPIKey(custom, ctx)
	if err != nil {
		t.Fatalf("custom key does not authenticate: %v", err)
	}
	if got.ID != key.ID || got.Name != "custom" {
		t.Errorf("resolved key = %+v, want the created key", got)
	}
}

func TestAPIKeyCreateRejectsInvalidValues(t *testing.T) {
	ctx := setupAPIKeyTest(t)
	prefix := APIKeyValuePrefix()

	existing := &model.APIKey{Name: "existing", APIKey: prefix + "existing-value", Enabled: true}
	if err := APIKeyCreate(existing, ctx); err != nil {
		t.Fatalf("seed APIKeyCreate failed: %v", err)
	}

	cases := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "wrong prefix", value: "sk-other-abcdefgh"},
		{name: "too short after prefix", value: prefix + "abc"},
		{name: "contains whitespace", value: prefix + "abc def ghi"},
		{name: "too long", value: prefix + strings.Repeat("a", apiKeyValueMaxLength)},
		{name: "duplicate value", value: existing.APIKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := &model.APIKey{Name: tc.name, APIKey: tc.value, Enabled: true}
			if err := APIKeyCreate(key, ctx); err == nil {
				t.Fatalf("APIKeyCreate accepted invalid value %q", tc.value)
			}
		})
	}
}

func TestAPIKeyUpdateReindexesChangedValue(t *testing.T) {
	ctx := setupAPIKeyTest(t)
	prefix := APIKeyValuePrefix()
	oldValue := prefix + "old-value-1234"
	newValue := prefix + "new-value-5678"

	key := &model.APIKey{Name: "renamable", APIKey: oldValue, Enabled: true}
	if err := APIKeyCreate(key, ctx); err != nil {
		t.Fatalf("APIKeyCreate failed: %v", err)
	}

	updated := &model.APIKey{ID: key.ID, Name: "renamable", APIKey: newValue, Enabled: true}
	if err := APIKeyUpdate(updated, ctx); err != nil {
		t.Fatalf("APIKeyUpdate failed: %v", err)
	}

	if _, err := APIKeyGetByAPIKey(oldValue, ctx); err == nil {
		t.Error("the superseded key still authenticates")
	}
	got, err := APIKeyGetByAPIKey(newValue, ctx)
	if err != nil {
		t.Fatalf("new key does not authenticate: %v", err)
	}
	if got.ID != key.ID {
		t.Errorf("new key resolved to id %d, want %d", got.ID, key.ID)
	}

	var stored model.APIKey
	if err := dbpkg.GetDB().First(&stored, key.ID).Error; err != nil {
		t.Fatalf("reload key failed: %v", err)
	}
	if stored.APIKey != newValue {
		t.Errorf("stored value = %q, want %q", stored.APIKey, newValue)
	}
}

func TestAPIKeyUpdateKeepsValueWhenOmitted(t *testing.T) {
	ctx := setupAPIKeyTest(t)
	value := APIKeyValuePrefix() + "keep-value-1234"

	key := &model.APIKey{Name: "before", APIKey: value, Enabled: true}
	if err := APIKeyCreate(key, ctx); err != nil {
		t.Fatalf("APIKeyCreate failed: %v", err)
	}

	updated := &model.APIKey{ID: key.ID, Name: "after", APIKey: "", Enabled: true}
	if err := APIKeyUpdate(updated, ctx); err != nil {
		t.Fatalf("APIKeyUpdate without a value failed: %v", err)
	}

	got, err := APIKeyGet(key.ID, ctx)
	if err != nil {
		t.Fatalf("APIKeyGet failed: %v", err)
	}
	if got.APIKey != value {
		t.Errorf("value changed to %q, want the previous value", got.APIKey)
	}
	if got.Name != "after" {
		t.Errorf("name = %q, want the update to apply", got.Name)
	}
}

func TestAPIKeyUpdateRejectsAnotherKeyValue(t *testing.T) {
	ctx := setupAPIKeyTest(t)
	prefix := APIKeyValuePrefix()

	first := &model.APIKey{Name: "first", APIKey: prefix + "first-value-1234", Enabled: true}
	if err := APIKeyCreate(first, ctx); err != nil {
		t.Fatalf("create first failed: %v", err)
	}
	second := &model.APIKey{Name: "second", APIKey: prefix + "second-value-1234", Enabled: true}
	if err := APIKeyCreate(second, ctx); err != nil {
		t.Fatalf("create second failed: %v", err)
	}

	updated := &model.APIKey{ID: second.ID, Name: "second", APIKey: first.APIKey, Enabled: true}
	if err := APIKeyUpdate(updated, ctx); err == nil {
		t.Fatal("update accepted a value already used by another key")
	}
}
