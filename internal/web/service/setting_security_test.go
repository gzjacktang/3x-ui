package service

import (
	"path/filepath"
	"testing"

	"github.com/xlzd/gotp"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func setupSettingTestDB(t *testing.T) {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.CloseDB(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestGetAllSettingViewRedactsSecrets(t *testing.T) {
	setupSettingTestDB(t)
	s := &SettingService{}
	if err := s.saveSetting("twoFactorToken", "totp-secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.saveSetting("ldapPassword", "ldap-secret"); err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.ApiToken{Name: "test", Token: "api-secret", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	view, err := s.GetAllSettingView()
	if err != nil {
		t.Fatal(err)
	}
	if view.TwoFactorToken != "" || view.LdapPassword != "" {
		t.Fatalf("settings view leaked secrets: %#v", view)
	}
	if !view.HasTwoFactorToken || !view.HasLdapPassword || !view.HasApiToken {
		t.Fatalf("settings view did not report configured secret flags: %#v", view)
	}
}

func TestUpdateAllSettingPreservesRedactedSecrets(t *testing.T) {
	setupSettingTestDB(t)
	s := &SettingService{}
	if err := s.saveSetting("ldapPassword", "ldap-secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.saveSetting("twoFactorEnable", "true"); err != nil {
		t.Fatal(err)
	}
	if err := s.saveSetting("twoFactorToken", "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}

	view, err := s.GetAllSettingView()
	if err != nil {
		t.Fatal(err)
	}
	settings := &view.AllSetting
	if err := s.UpdateAllSetting(settings, SecretClears{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLdapPassword(); got != "ldap-secret" {
		t.Fatalf("ldap password = %q, want preserved secret", got)
	}
	if got, _ := s.GetTwoFactorToken(); got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("2fa token = %q, want preserved secret", got)
	}
}

func TestUpdateAllSettingTwoFactorSecret(t *testing.T) {
	cases := []struct {
		name      string
		stored    string
		submitted string
		want      string
		wantErr   string
	}{
		{name: "canonical", submitted: "JBSWY3DPEHPK3PXP", want: "JBSWY3DPEHPK3PXP"},
		{name: "lower case and grouped", submitted: "jbsw y3dp ehpk 3pxp", want: "JBSWY3DPEHPK3PXP"},
		{name: "redacted keeps stored", stored: "ORIGINALSECRET234567", submitted: "", want: "ORIGINALSECRET234567"},
		{name: "not base32", stored: "ORIGINALSECRET234567", submitted: "totp-secret", want: "ORIGINALSECRET234567", wantErr: "two-factor secret must be a non-empty base32 key"},
		{name: "nothing stored", submitted: "", want: "", wantErr: "two-factor secret must be a non-empty base32 key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupSettingTestDB(t)
			s := &SettingService{}
			if err := s.SetTwoFactorToken(tc.stored); err != nil {
				t.Fatal(err)
			}
			view, err := s.GetAllSettingView()
			if err != nil {
				t.Fatal(err)
			}
			settings := &view.AllSetting
			settings.TwoFactorEnable = true
			settings.TwoFactorToken = tc.submitted
			err = s.UpdateAllSetting(settings, SecretClears{})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("UpdateAllSetting error = %v, want nil", err)
			}
			if tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
				t.Fatalf("UpdateAllSetting error = %v, want %q", err, tc.wantErr)
			}
			if got, _ := s.GetTwoFactorToken(); got != tc.want {
				t.Fatalf("stored 2FA secret = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUpdateAllSettingClearsFlaggedSecrets(t *testing.T) {
	setupSettingTestDB(t)
	s := &SettingService{}
	if err := s.saveSetting("ldapPassword", "ldap-secret"); err != nil {
		t.Fatal(err)
	}

	view, err := s.GetAllSettingView()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAllSetting(&view.AllSetting, SecretClears{LdapPassword: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetLdapPassword(); got != "" {
		t.Fatalf("ldap password = %q, want cleared", got)
	}

	view, err = s.GetAllSettingView()
	if err != nil {
		t.Fatal(err)
	}
	if view.HasLdapPassword {
		t.Fatal("hasLdapPassword must report false after clearing")
	}
}

func TestSanitizePublicHTTPURLBlocksPrivateAddressUnlessAllowed(t *testing.T) {
	if _, err := SanitizePublicHTTPURL("http://127.0.0.1:8080/hook", false); err == nil {
		t.Fatal("expected localhost URL to be blocked")
	}
	if got, err := SanitizePublicHTTPURL("http://127.0.0.1:8080/hook", true); err != nil || got != "http://127.0.0.1:8080/hook" {
		t.Fatalf("allowPrivate result = %q, %v", got, err)
	}
}

func TestVerifyTwoFactorCode(t *testing.T) {
	setupSettingTestDB(t)
	s := &SettingService{}
	if err := s.saveSetting("twoFactorEnable", "true"); err != nil {
		t.Fatal(err)
	}
	const token = "JBSWY3DPEHPK3PXP"
	if err := s.saveSetting("twoFactorToken", token); err != nil {
		t.Fatal(err)
	}

	if err := s.VerifyTwoFactorCode(gotp.NewDefaultTOTP(token).Now()); err != nil {
		t.Fatalf("valid code rejected: %v", err)
	}
	if err := s.VerifyTwoFactorCode("000000"); err == nil {
		t.Fatal("invalid code accepted")
	}
}
