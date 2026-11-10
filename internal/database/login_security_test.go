package database

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
)

func TestFirstUserIsRandomAndNotOverwritten(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("CLI initialization must not seed a login: count=%d err=%v", count, err)
	}
	username, password, err := EnsureFirstUser()
	if err != nil || username == "" || password == "" || username == "admin" || password == "admin" {
		t.Fatalf("random first credentials not created: err=%v", err)
	}
	var user model.User
	if err := db.First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.Username != username || !crypto.CheckPasswordHash(user.Password, password) {
		t.Fatal("stored credentials do not match generated login")
	}
	if u, p, err := EnsureFirstUser(); err != nil || u != "" || p != "" {
		t.Fatalf("existing login must be preserved: err=%v", err)
	}
}

func TestSessionSecretRotatesOnlyOnceOnUpgrade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	if err := db.Create(&model.Setting{Key: "secret", Value: "legacy-key"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("seeder_name = ?", "SessionSecretRotation").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runSeeders(false); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.Setting{}).Where("key = ?", "secret").Count(&count)
	if count != 0 {
		t.Fatal("legacy secret was not removed")
	}
	if err := db.Create(&model.Setting{Key: "secret", Value: "new-key"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runSeeders(false); err != nil {
		t.Fatal(err)
	}
	var setting model.Setting
	if err := db.Where("key = ?", "secret").First(&setting).Error; err != nil || setting.Value != "new-key" {
		t.Fatalf("later boot changed the replacement secret: %v", err)
	}
}
