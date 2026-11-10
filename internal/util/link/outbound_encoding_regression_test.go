package link

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func TestShareRemarkDecodesExactlyOnce(t *testing.T) {
	for _, scheme := range []string{"vless", "trojan", "hysteria2", "wireguard"} {
		for raw, want := range map[string]string{"a+b": "a+b", "a%2Bb": "a+b", "a%2520b": "a%20b", "x%25y": "x%y"} {
			t.Run(scheme+"/"+raw, func(t *testing.T) {
				res, err := ParseLink(scheme + "://key@127.0.0.1:443#" + raw)
				if err != nil {
					t.Fatal(err)
				}
				if res.Outbound["tag"] != want {
					t.Fatalf("tag=%q, want %q", res.Outbound["tag"], want)
				}
			})
		}
	}
}

func TestVmessPaddedURLSafeBase64(t *testing.T) {
	raw := `{"v":"2","ps":"test??","add":"example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"tcp"}`
	for name, encoding := range map[string]*base64.Encoding{"std": base64.StdEncoding, "raw": base64.RawStdEncoding, "url": base64.URLEncoding, "raw-url": base64.RawURLEncoding} {
		t.Run(name, func(t *testing.T) {
			res, err := ParseLink("vmess://" + encoding.EncodeToString([]byte(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if res.Outbound["tag"] != "test??" {
				t.Fatalf("tag=%v", res.Outbound["tag"])
			}
		})
	}
}

func TestWireguardIPv6EndpointAndSecret(t *testing.T) {
	res, err := ParseLink("wireguard://a%2Bb%3D@[2001:db8::1]:51820#peer")
	if err != nil {
		t.Fatal(err)
	}
	settings := res.Outbound["settings"].(map[string]any)
	peers := settings["peers"].([]any)
	if got := fmt.Sprint(peers[0].(map[string]any)["endpoint"]); got != "[2001:db8::1]:51820" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := settings["secretKey"]; got != "a+b=" {
		t.Fatalf("secret=%v", got)
	}
}
