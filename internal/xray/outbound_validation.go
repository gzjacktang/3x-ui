package xray

import (
	"encoding/base64"
	"net/netip"
	"strings"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/vless"
	vlessEncryption "github.com/xtls/xray-core/proxy/vless/encryption"
	vlessOutbound "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/proxy/wireguard"
	"github.com/xtls/xray-core/transport/internet/splithttp"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// validateXhttpDownloadSettings refuses what conf.Build() lets through but the core's XHTTP
// dialer panics on at every dial: a downloadSettings block without an address or not on xhttp.
func validateXhttpDownloadSettings(settings *serial.TypedMessage) error {
	if settings == nil {
		return nil
	}
	instance, err := settings.GetInstance()
	if err != nil {
		return nil
	}
	sender, ok := instance.(*proxyman.SenderConfig)
	if !ok {
		return nil
	}
	transport, err := sender.StreamSettings.GetEffectiveTransportSettings()
	if err != nil {
		return nil
	}
	xhttp, ok := transport.(*splithttp.Config)
	if !ok || xhttp.DownloadSettings == nil {
		return nil
	}
	if xhttp.DownloadSettings.Address == nil {
		return common.NewError("xhttp downloadSettings has no address")
	}
	transport, _ = xhttp.DownloadSettings.GetEffectiveTransportSettings()
	if _, ok := transport.(*splithttp.Config); !ok {
		return common.NewError("xhttp downloadSettings network is not xhttp")
	}
	return nil
}

// validateHandlerSettings refuses what conf.Build() lets through but the core parses only
// in the handler constructor, where one failure aborts the whole core start.
func validateHandlerSettings(settings *serial.TypedMessage) error {
	if settings == nil {
		return nil
	}
	instance, err := settings.GetInstance()
	if err != nil {
		return nil
	}
	switch config := instance.(type) {
	case *wireguard.DeviceConfig:
		return validateWireguardClient(config)
	case *vlessOutbound.Config:
		return validateVlessEncryption(config)
	case *shadowsocks_2022.ClientConfig:
		return validateShadowsocks2022Key(config)
	}
	return nil
}

// validateWireguardClient mirrors proxy/wireguard.NewClient; remoteDNS goes to
// netip.MustParseAddr there, so a non-IP entry ("local" before 26.9.30) is a crash.
func validateWireguardClient(device *wireguard.DeviceConfig) error {
	if !device.IsClient {
		return nil
	}
	if len(device.Peers) == 0 {
		return common.NewError("wireguard outbound has no peers")
	}
	for _, peer := range device.Peers {
		if peer.PublicKey == "" {
			return common.NewError("wireguard peer without publicKey")
		}
		if peer.Endpoint == "" {
			return common.NewError("wireguard peer without endpoint")
		}
	}
	for _, local := range device.Endpoint {
		if _, err := netip.ParseAddr(local); err == nil {
			continue
		}
		if _, err := netip.ParsePrefix(local); err != nil {
			return common.NewErrorf("wireguard address entry %q is neither an IP nor a prefix", local)
		}
	}
	for _, server := range device.DNS {
		if _, err := netip.ParseAddr(server); err != nil {
			return common.NewErrorf("wireguard remoteDNS entry %q is not an IP address", server)
		}
	}
	return nil
}

// validateVlessEncryption mirrors proxy/vless/outbound.New, which parses the padding spec
// and the X25519 / ML-KEM-768 keys of "encryption" through ClientInstance.Init.
func validateVlessEncryption(config *vlessOutbound.Config) error {
	if config.Vnext == nil || config.Vnext.User == nil {
		return nil
	}
	user, err := config.Vnext.User.ToMemoryUser()
	if err != nil {
		return err
	}
	account, ok := user.Account.(*vless.MemoryAccount)
	if !ok || account.Encryption == "" || account.Encryption == "none" {
		return nil
	}
	var keys [][]byte
	for part := range strings.SplitSeq(account.Encryption, ".") {
		key, _ := base64.RawURLEncoding.DecodeString(part)
		keys = append(keys, key)
	}
	if err := (&vlessEncryption.ClientInstance{}).Init(keys, account.XorMode, account.Seconds, account.Padding); err != nil {
		return common.NewErrorf("vless encryption is unusable: %v", err)
	}
	return nil
}

// validateShadowsocks2022Key mirrors shadowsocks_2022.NewClient: every ":"-separated key
// is base64 of the cipher's key length, and chacha20-poly1305 takes a single key.
func validateShadowsocks2022Key(config *shadowsocks_2022.ClientConfig) error {
	method, err := shadowsocks_2022.GetCipherMethod(config.Method)
	if err != nil {
		return err
	}
	keys, err := shadowsocks_2022.ParsePSKList(config.Key, method.KeySaltLength)
	if err != nil {
		return common.NewErrorf("shadowsocks 2022 key is unusable for %s: %v", config.Method, err)
	}
	if method.IsChaCha && len(keys) > 1 {
		return common.NewError("shadowsocks 2022 chacha20-poly1305 does not support multiple keys")
	}
	return nil
}
