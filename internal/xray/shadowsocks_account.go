package xray

import (
	"strings"

	"github.com/xtls/xray-core/proxy/shadowsocks"
)

// legacyShadowsocksAccountType is the type URL serial.ToTypedMessage stamps on
// a pre-2022 shadowsocks account, which identifies the one inbound whose user
// list tolerates duplicate emails.
const legacyShadowsocksAccountType = "xray.proxy.shadowsocks.Account"

// shadowsocks2022Ciphers are the methods that select xray's shadowsocks-2022
// inbound (sing's shadowaead_2022 list). They take a different account type
// than the legacy AEAD ciphers, and the running inbound casts the account it
// receives without checking, so a wrong guess takes the whole core down.
var shadowsocks2022Ciphers = map[string]struct{}{
	"2022-blake3-aes-128-gcm":       {},
	"2022-blake3-aes-256-gcm":       {},
	"2022-blake3-chacha20-poly1305": {},
}

// shadowsocksCipherName resolves the cipher a shadowsocks user's account must
// be built for. Panel-built user maps carry it under "cipher"; client objects
// taken verbatim from an inbound's settings carry the inbound's method under
// "method" instead (HealShadowsocksClientMethods writes it onto every
// legacy-cipher client).
func shadowsocksCipherName(user map[string]any) (string, error) {
	cipher, err := getOptionalUserString(user, "cipher")
	if err != nil {
		return "", err
	}
	if cipher != "" {
		return cipher, nil
	}
	return getOptionalUserString(user, "method")
}

// shadowsocksCipherType mirrors xray-core's infra/conf cipherFromString,
// aliases and case-insensitivity included, so the account the panel builds for
// a live user matches the one the core built for that inbound from its config.
func shadowsocksCipherType(cipher string) shadowsocks.CipherType {
	switch strings.ToLower(cipher) {
	case "aes-128-gcm", "aead_aes_128_gcm":
		return shadowsocks.CipherType_AES_128_GCM
	case "aes-256-gcm", "aead_aes_256_gcm":
		return shadowsocks.CipherType_AES_256_GCM
	case "chacha20-poly1305", "aead_chacha20_poly1305", "chacha20-ietf-poly1305":
		return shadowsocks.CipherType_CHACHA20_POLY1305
	case "xchacha20-poly1305", "aead_xchacha20_poly1305", "xchacha20-ietf-poly1305":
		return shadowsocks.CipherType_XCHACHA20_POLY1305
	default:
		return shadowsocks.CipherType_UNKNOWN
	}
}

// isShadowsocks2022Cipher reports whether the method selects the
// shadowsocks-2022 inbound rather than the legacy AEAD one.
func isShadowsocks2022Cipher(cipher string) bool {
	_, ok := shadowsocks2022Ciphers[strings.ToLower(cipher)]
	return ok
}
