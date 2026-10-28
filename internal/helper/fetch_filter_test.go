package helper

import (
	"testing"

	"github.com/dlclark/regexp2"
)

func mustCompileModelFilter(t *testing.T, pattern string) *regexp2.Regexp {
	t.Helper()
	re, err := compileModelFilter(&pattern)
	if err != nil {
		t.Fatalf("compileModelFilter(%q) failed: %v", pattern, err)
	}
	return re
}

func TestCompileModelFilterEmptyPattern(t *testing.T) {
	if re, err := compileModelFilter(nil); err != nil || re != nil {
		t.Fatalf("nil pattern = (%v, %v), want (nil, nil)", re, err)
	}
	empty := "  "
	if re, err := compileModelFilter(&empty); err != nil || re != nil {
		t.Fatalf("blank pattern = (%v, %v), want (nil, nil)", re, err)
	}
}

func TestCompileModelFilterRejectsInvalidPattern(t *testing.T) {
	invalid := "([unclosed"
	if _, err := compileModelFilter(&invalid); err == nil {
		t.Fatal("invalid pattern compiled without error")
	}
}

func TestFilterModelsWithoutFiltersKeepsEverything(t *testing.T) {
	models := []string{"gpt-4o", "claude-sonnet-4", "gemini-2.5-pro"}

	got, err := filterModels(models, nil, nil)
	if err != nil {
		t.Fatalf("filterModels failed: %v", err)
	}
	if len(got) != len(models) {
		t.Fatalf("got %v, want all models", got)
	}
	for i := range models {
		if got[i] != models[i] {
			t.Fatalf("order changed: got %v", got)
		}
	}
}

func TestFilterModelsRequiresAllFiltersToMatch(t *testing.T) {
	channelFilter := mustCompileModelFilter(t, "^gpt-")
	globalFilter := mustCompileModelFilter(t, "4o$")
	models := []string{"gpt-4o", "gpt-4o-mini", "claude-4o", "claude-sonnet-4"}

	// 渠道级与全局过滤是 AND 关系：只有同时通过两者的模型才保留。
	got, err := filterModels(models, channelFilter, globalFilter)
	if err != nil {
		t.Fatalf("filterModels failed: %v", err)
	}
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got %v, want [gpt-4o]", got)
	}
}

func TestFilterModelsGlobalOnly(t *testing.T) {
	globalFilter := mustCompileModelFilter(t, "embedding")
	models := []string{"text-embedding-3-small", "gpt-4o"}

	got, err := filterModels(models, nil, globalFilter)
	if err != nil {
		t.Fatalf("filterModels failed: %v", err)
	}
	if len(got) != 1 || got[0] != "text-embedding-3-small" {
		t.Fatalf("got %v, want only the embedding model", got)
	}
}
