package totp

import (
	"encoding/base32"
	"strings"
	"time"

	"github.com/xlzd/gotp"
)

const SkewWindows = 1

func NormalizeSecret(secret string) string {
	return strings.TrimRight(strings.ToUpper(strings.Join(strings.Fields(secret), "")), "=")
}

func ValidSecret(secret string) bool {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(NormalizeSecret(secret))
	return err == nil && len(key) > 0
}

func VerifyWithSkew(secret, code string, now time.Time) bool {
	if !ValidSecret(secret) {
		return false
	}
	totp := gotp.NewDefaultTOTP(NormalizeSecret(secret))
	for i := -SkewWindows; i <= SkewWindows; i++ {
		if totp.AtTime(now.Add(time.Duration(i*30)*time.Second)) == code {
			return true
		}
	}
	return false
}
