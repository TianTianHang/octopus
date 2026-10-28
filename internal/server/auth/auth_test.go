package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/golang-jwt/jwt/v5"
)

func setupAuthTestDB(t *testing.T) {
	t.Helper()
	if db.GetDB() != nil {
		_ = db.Close()
	}
	path := filepath.Join(t.TempDir(), "auth-test.db")
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
}

// tokenExpiresIn 解出 token 的剩余有效期。
func tokenExpiresIn(t *testing.T, token string) time.Duration {
	t.Helper()
	claims := &jwt.RegisteredClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return getJWTSecret(), nil
	}); err != nil {
		t.Fatalf("parse token failed: %v", err)
	}
	if claims.ExpiresAt == nil {
		t.Fatal("token has no expiry")
	}
	return time.Until(claims.ExpiresAt.Time)
}

func TestGenerateJWTTokenUsesSeconds(t *testing.T) {
	setupAuthTestDB(t)

	cases := []struct {
		name string
		in   int
		want time.Duration
	}{
		// 86400 必须是 1 天而不是 60 天: 前端一直按秒传值。
		{name: "one day", in: 86400, want: 24 * time.Hour},
		{name: "trusted device is 30 days", in: -1, want: 30 * 24 * time.Hour},
		{name: "default is 15 minutes", in: 0, want: 15 * time.Minute},
		// 其它负数过去会签出没有 ExpiresAt 的永久 token。
		{name: "other negative falls back to default", in: -7, want: 15 * time.Minute},
		{name: "longer than the cap is clamped", in: 10 * 365 * 24 * 60 * 60, want: 30 * 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, _, err := GenerateJWTToken(tc.in)
			if err != nil {
				t.Fatalf("GenerateJWTToken(%d) failed: %v", tc.in, err)
			}
			got := tokenExpiresIn(t, token)
			if diff := got - tc.want; diff > time.Minute || diff < -time.Minute {
				t.Errorf("expires in %v, want ~%v", got, tc.want)
			}
			if !VerifyJWTToken(token) {
				t.Error("generated token does not verify")
			}
		})
	}
}
