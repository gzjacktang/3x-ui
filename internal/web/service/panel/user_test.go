package panel

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/xlzd/gotp"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

func TestCheckUserTwoFactorSecretForms(t *testing.T) {
	cases := []struct {
		name       string
		stored     string
		codeSecret string
		wantOK     bool
		wantErr    string
	}{
		{name: "canonical", stored: "JBSWY3DPEHPK3PXP", codeSecret: "JBSWY3DPEHPK3PXP", wantOK: true},
		{name: "lower case", stored: "jbswy3dpehpk3pxp", codeSecret: "JBSWY3DPEHPK3PXP", wantOK: true},
		{name: "grouped and padded", stored: " jbsw y3dp ehpk 3pxp== ", codeSecret: "JBSWY3DPEHPK3PXP", wantOK: true},
		{name: "not base32", stored: "totp-secret", codeSecret: "JBSWY3DPEHPK3PXP", wantErr: "invalid 2fa code"},
		{name: "empty secret", stored: "", codeSecret: "", wantErr: "invalid 2fa code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
				t.Fatalf("InitDB: %v", err)
			}
			t.Cleanup(func() {
				if err := database.CloseDB(); err != nil {
					t.Fatal(err)
				}
			})

			svc := &UserService{}
			if err := svc.UpdateFirstUser("admin", "s3cret-Pass"); err != nil {
				t.Fatalf("UpdateFirstUser: %v", err)
			}
			if err := svc.settingService.SetTwoFactorEnable(true); err != nil {
				t.Fatal(err)
			}
			if err := svc.settingService.SetTwoFactorToken(tc.stored); err != nil {
				t.Fatal(err)
			}
			code := gotp.NewDefaultTOTP(tc.codeSecret).AtTime(time.Now())
			user, err := svc.CheckUser("admin", "s3cret-Pass", code)
			if tc.wantOK {
				if err != nil || user == nil {
					t.Fatalf("CheckUser(%q) = (%v, %v), want authenticated user", tc.stored, user, err)
				}
				return
			}
			if user != nil || err == nil || err.Error() != tc.wantErr {
				t.Fatalf("CheckUser(%q) = (%v, %v), want nil user and error %q", tc.stored, user, err, tc.wantErr)
			}
		})
	}
}
