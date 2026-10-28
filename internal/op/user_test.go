package op

import (
	"path/filepath"
	"strings"
	"testing"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func setupUserOpTestDB(t *testing.T) {
	t.Helper()

	if dbpkg.GetDB() != nil {
		_ = dbpkg.Close()
	}
	dbPath := filepath.Join(t.TempDir(), "octopus-user-test.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = dbpkg.Close()
	})
}

func TestUserSetPasswordOverwritesWithoutOldPassword(t *testing.T) {
	setupUserOpTestDB(t)
	if err := UserInit(); err != nil {
		t.Fatalf("UserInit failed: %v", err)
	}

	if err := UserSetPassword("new-secret"); err != nil {
		t.Fatalf("UserSetPassword failed: %v", err)
	}
	if err := UserVerify("admin", "new-secret"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
	if err := UserVerify("admin", "admin"); err == nil {
		t.Error("old password still accepted after reset")
	}

	var stored model.User
	if err := dbpkg.GetDB().First(&stored).Error; err != nil {
		t.Fatalf("reload user failed: %v", err)
	}
	if err := stored.ComparePassword("new-secret"); err != nil {
		t.Errorf("stored password is not a valid hash of the new password: %v", err)
	}
	if strings.Contains(stored.Password, "new-secret") {
		t.Error("password was stored in plain text")
	}
}

func TestUserSetPasswordValidatesInput(t *testing.T) {
	setupUserOpTestDB(t)
	if err := UserInit(); err != nil {
		t.Fatalf("UserInit failed: %v", err)
	}

	if err := UserSetPassword(""); err == nil {
		t.Error("empty password accepted")
	}
	if err := UserSetPassword(strings.Repeat("a", maxPasswordBytes+1)); err == nil {
		t.Error("password longer than bcrypt's 72-byte limit accepted")
	}
	if err := UserSetPassword(strings.Repeat("a", maxPasswordBytes)); err != nil {
		t.Errorf("password of exactly %d bytes rejected: %v", maxPasswordBytes, err)
	}
}
