package link

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestParseLink_UpgradesLegacyXdnsFinalMask(t *testing.T) {
	fm := url.QueryEscape(`{"udp":[{"type":"xdns","settings":{"domains":[{"name":"t.example.com","types":[16],"edns0":1232}],"resolvers":[{"type":"udp","settings":{"addr":"8.8.8.8:53"}}]}}]}`)
	res, err := ParseLink("vless://uuid@1.2.3.4:53?type=kcp&security=none&fm=" + fm + "#dns")
	if err != nil {
		t.Fatalf("parse vless with fm: %v", err)
	}
	stream, _ := res.Outbound["streamSettings"].(map[string]any)
	got, err := json.Marshal(stream["finalmask"])
	if err != nil {
		t.Fatalf("marshal finalmask: %v", err)
	}
	want := `{"udp":[{"settings":{"domains":[{"edns0":1232,"names":["t.example.com"],"types":[16]}],"resolvers":[{"addrs":["udp://8.8.8.8:53"]}]},"type":"xdns"}]}`
	if string(got) != want {
		t.Fatalf("imported finalmask\n got: %s\nwant: %s", got, want)
	}
}
