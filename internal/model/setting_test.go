package model

import (
	"strings"
	"testing"
)

func TestDefaultSettingsAreValid(t *testing.T) {
	for _, setting := range DefaultSettings() {
		setting := setting
		if err := setting.Validate(); err != nil {
			t.Errorf("default setting %s=%q is invalid: %v", setting.Key, setting.Value, err)
		}
	}
}

func TestRelayLogSettingValidation(t *testing.T) {
	cases := []struct {
		name    string
		key     SettingKey
		value   string
		wantErr bool
	}{
		{name: "keep period min", key: SettingKeyRelayLogKeepPeriod, value: "1"},
		{name: "keep period default", key: SettingKeyRelayLogKeepPeriod, value: "7"},
		{name: "keep period max", key: SettingKeyRelayLogKeepPeriod, value: "3650"},
		{name: "keep period zero rejected", key: SettingKeyRelayLogKeepPeriod, value: "0", wantErr: true},
		{name: "keep period negative rejected", key: SettingKeyRelayLogKeepPeriod, value: "-1", wantErr: true},
		{name: "keep period non numeric rejected", key: SettingKeyRelayLogKeepPeriod, value: "7d", wantErr: true},
		{name: "keep period too large rejected", key: SettingKeyRelayLogKeepPeriod, value: "3651", wantErr: true},
		{name: "max size unlimited", key: SettingKeyRelayLogMaxSizeMB, value: "0"},
		{name: "max size default", key: SettingKeyRelayLogMaxSizeMB, value: "2048"},
		{name: "max size boundary", key: SettingKeyRelayLogMaxSizeMB, value: "102400"},
		{name: "max size negative rejected", key: SettingKeyRelayLogMaxSizeMB, value: "-1", wantErr: true},
		{name: "max size too large rejected", key: SettingKeyRelayLogMaxSizeMB, value: "102401", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Setting{Key: tc.key, Value: tc.value}).Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error for %s=%q", tc.key, tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error for %s=%q: %v", tc.key, tc.value, err)
			}
		})
	}
}

func TestModelFilterSettingValidation(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty disables filter", value: ""},
		{name: "valid regex", value: `^(gpt|claude)-`},
		{name: "valid alternation with groups", value: `^(?:o\d|gpt-5)`},
		{name: "invalid regex rejected", value: `([unclosed`, wantErr: true},
		{name: "too long rejected", value: `a` + strings.Repeat("b", ModelFilterMaxLength), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Setting{Key: SettingKeyModelFilter, Value: tc.value}).Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error for %q", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error for %q: %v", tc.value, err)
			}
		})
	}
}
