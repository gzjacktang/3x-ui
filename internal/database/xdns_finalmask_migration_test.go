package database

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const legacyXdnsFinalmask = `{"udp":[{"type":"xdns","settings":{"domains":[{"name":"t.example.com","types":[16],"edns0":1232}],"resolvers":[{"type":"udp","settings":{"addr":"8.8.8.8:53"}}]}}]}`

func assertXdnsUpgraded(t *testing.T, raw string) {
	t.Helper()
	var stream struct {
		Finalmask struct {
			UDP []struct {
				Settings map[string]any `json:"settings"`
			} `json:"udp"`
		} `json:"finalmask"`
	}
	if err := json.Unmarshal([]byte(raw), &stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.Finalmask.UDP) != 1 {
		t.Fatalf("expected one XDNS mask in %s", raw)
	}
	got, _ := json.Marshal(stream.Finalmask.UDP[0].Settings)
	const want = `{"domains":[{"edns0":1232,"names":["t.example.com"],"types":[16]}],"resolvers":[{"addrs":["udp://8.8.8.8:53"]}]}`
	if string(got) != want {
		t.Fatalf("XDNS settings = %s, want %s", got, want)
	}
}

func TestXdnsFinalmaskSeederUpgradesRetainedStoredMasksOnce(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	if err := InitDB(config.GetDBPath()); err != nil {
		if strings.Contains(err.Error(), "CGO_ENABLED=0") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	legacyStream := `{"network":"kcp","finalmask":` + legacyXdnsFinalmask + `}`
	inbound := &model.Inbound{Tag: "xdns-test", Port: 45332, Enable: true, Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: legacyStream}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatal(err)
	}
	legacyTemplate := `{"outbounds":[{"protocol":"vless","tag":"xdns-out","streamSettings":` + legacyStream + `}]} `
	seedTemplate(t, legacyTemplate)
	if err := db.Where("seeder_name = ?", "XdnsFinalmaskNamesAddrsFix").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runSeeders(false); err != nil {
		t.Fatal(err)
	}
	var stored model.Inbound
	if err := db.First(&stored, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	assertXdnsUpgraded(t, stored.StreamSettings)
	var template struct {
		Outbounds []struct {
			StreamSettings json.RawMessage `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(storedTemplate(t)), &template); err != nil || len(template.Outbounds) != 1 {
		t.Fatalf("template: %v", err)
	}
	assertXdnsUpgraded(t, string(template.Outbounds[0].StreamSettings))
	seedTemplate(t, legacyTemplate)
	if err := db.Model(&model.Inbound{}).Where("id = ?", inbound.Id).Update("stream_settings", legacyStream).Error; err != nil {
		t.Fatal(err)
	}
	if err := runSeeders(false); err != nil {
		t.Fatal(err)
	}
	if storedTemplate(t) != legacyTemplate {
		t.Fatal("completed seeder rewrote hand-edited template again")
	}
}

func TestRewriteTemplateCompatAlsoRepairsRestoredLegacyValues(t *testing.T) {
	raw := `{"outbounds":[{"protocol":"freedom","tag":"direct","settings":{"domainStrategy":"UseIPv4"}},{"protocol":"wireguard","settings":{"remoteDNS":["local"],"domainStrategy":"ForceIPv6"}},{"protocol":"dns","settings":{"nonIPQuery":"drop","blockTypes":[0]}}]}`
	rewritten, changed, err := RewriteXrayTemplateCompat(raw)
	if err != nil || !changed {
		t.Fatalf("rewrite: changed=%v err=%v", changed, err)
	}
	var template struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(rewritten), &template); err != nil {
		t.Fatal(err)
	}
	for _, outbound := range template.Outbounds {
		settings, _ := outbound["settings"].(map[string]any)
		for _, key := range []string{"domainStrategy", "nonIPQuery", "blockTypes", "remoteDNS"} {
			if _, ok := settings[key]; ok {
				t.Fatalf("legacy %s remains after rewrite: %s", key, rewritten)
			}
		}
	}
	again, didChange, err := RewriteXrayTemplateCompat(rewritten)
	if err != nil || didChange || again != rewritten {
		t.Fatalf("non-idempotent rewrite: changed=%v err=%v", didChange, err)
	}
}
