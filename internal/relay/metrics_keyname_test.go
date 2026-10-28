package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

func TestWithAttemptKeyNamesResolvesRemarks(t *testing.T) {
	ctx := setupRelayTestDB(t)

	channel := &model.Channel{
		Name:     "attempt-key-name",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: "https://example.invalid"}},
		Model:    "gpt-4o",
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-primary", Remark: "primary"},
			{Enabled: true, ChannelKey: "sk-backup", Remark: "backup"},
		},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	if len(channel.Keys) < 2 || channel.Keys[1].ID == 0 {
		t.Fatalf("channel keys were not persisted with ids: %+v", channel.Keys)
	}

	attempts := []model.ChannelAttempt{
		{ChannelID: channel.ID, ChannelKeyID: channel.Keys[1].ID},
		// 渠道已被删除: 保留尝试记录, 只是拿不到名字。
		{ChannelID: 999999, ChannelKeyID: 1},
		// 没有 key 的尝试(例如渠道级失败)不需要名字。
		{ChannelID: channel.ID},
		// Key 已被删除: 同样只保留 ID。
		{ChannelID: channel.ID, ChannelKeyID: 424242},
	}

	got := withAttemptKeyNames(ctx, attempts)
	if got[0].ChannelKeyName != "backup" {
		t.Errorf("key name = %q, want backup", got[0].ChannelKeyName)
	}
	if got[1].ChannelKeyName != "" {
		t.Errorf("unknown channel got a key name %q", got[1].ChannelKeyName)
	}
	if got[2].ChannelKeyName != "" {
		t.Errorf("attempt without a key got a key name %q", got[2].ChannelKeyName)
	}
	if got[3].ChannelKeyName != "" {
		t.Errorf("unknown key got a name %q", got[3].ChannelKeyName)
	}
}

func TestWithAttemptKeyNamesHandlesEmptyInput(t *testing.T) {
	ctx := setupRelayTestDB(t)
	if got := withAttemptKeyNames(ctx, nil); len(got) != 0 {
		t.Errorf("got %d attempts, want none", len(got))
	}
}
