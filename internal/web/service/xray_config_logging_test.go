package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestGetXrayConfigAggregatesDisabledClientLogs(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		clients, inbounds, excluded int
		affected                    int
		shared                      bool
	}{
		{"many disabled clients", 1000, 4, 1000, 4, false},
		{"mixed clients", 8, 4, 3, 2, false},
		{"manual disable is not traffic exclusion", 8, 4, 0, 0, false},
		{"shared client counts as two entries", 4, 2, 1, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupSettingTestDB(t)
			ds := seedScaleDataset(t, tc.clients, tc.inbounds)
			db := database.GetDB()
			if tc.shared {
				var record model.ClientRecord
				if err := db.Where("email = ?", ds.emails[0]).First(&record).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.ClientInbound{ClientId: record.Id, InboundId: ds.inboundIds[1]}).Error; err != nil {
					t.Fatal(err)
				}
				/* Keep settings.clients and the shared traffic backfill in sync. */
				sharedClients := append(append([]model.Client(nil), ds.perInbound[1]...), ds.perInbound[0][0])
				if err := db.Model(&model.Inbound{}).Where("id = ?", ds.inboundIds[1]).Update("settings", clientsSettings(t, sharedClients)).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.excluded < tc.clients {
				if err := db.Model(&model.ClientRecord{}).Where("email = ?", ds.emails[tc.clients-1]).Update("enable", false).Error; err != nil {
					t.Fatal(err)
				}
			}
			svc := &XrayService{}
			want, err := svc.GetXrayConfig()
			if err != nil {
				t.Fatal(err)
			}
			blocked := make(map[string]bool, tc.excluded)
			for _, email := range ds.emails[:tc.excluded] {
				blocked[email] = true
			}
			for i := range want.InboundConfigs {
				var settings map[string]any
				if err := json.Unmarshal(want.InboundConfigs[i].Settings, &settings); err != nil {
					t.Fatal(err)
				}
				if clients, ok := settings["clients"].([]any); ok {
					var keep []any
					for _, raw := range clients {
						client := raw.(map[string]any)
						if !blocked[client["email"].(string)] {
							keep = append(keep, client)
						}
					}
					settings["clients"] = keep
					body, err := json.MarshalIndent(settings, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					want.InboundConfigs[i].Settings = body
				}
			}
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if tc.excluded > 0 {
				if err := db.Model(&xray.ClientTraffic{}).Where("email IN ?", ds.emails[:tc.excluded]).Update("enable", false).Error; err != nil {
					t.Fatal(err)
				}
			}
			for generation := range 2 {
				marker := fmt.Sprintf("%s generation %d", t.Name(), generation)
				logger.Info(marker)
				got, err := svc.GetXrayConfig()
				if err != nil {
					t.Fatal(err)
				}
				gotJSON, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				if string(gotJSON) != string(wantJSON) {
					t.Fatal("generated configuration changed beyond disabled client exclusions")
				}
				lines := configLogsSince(t, marker)
				var summaries int
				for _, line := range lines {
					if strings.Contains(line, "Remove Inbound User") || strings.Contains(line, "@scale") || strings.Contains(line, "sub-000") {
						t.Fatalf("per-client log emitted: %s", line)
					}
					if strings.Contains(line, "Xray config generated:") {
						summaries++
						entries := tc.excluded
						if tc.shared {
							entries++
						}
						if want := fmt.Sprintf("excluded %d disabled client entries across %d inbounds", entries, tc.affected); !strings.HasSuffix(line, want) {
							t.Errorf("wrong exclusion summary: %s", line)
						}
					}
				}
				wantSummaries := 0
				if tc.excluded > 0 {
					wantSummaries = 1
				}
				if summaries != wantSummaries {
					t.Errorf("summary count = %d, want %d", summaries, wantSummaries)
				}
			}
		})
	}
}

func TestGetXrayConfigFailedGenerationHasNoExclusionLog(t *testing.T) {
	setupSettingTestDB(t)
	ds := seedScaleDataset(t, 1, 1)
	db := database.GetDB()
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", ds.emails[0]).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", ds.inboundIds[0]).Update("stream_settings", `{"network":"tcp","security":"tls","tlsSettings":{}}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&model.InboundFallback{}); err != nil {
		t.Fatal(err)
	}
	marker := t.Name()
	logger.Info(marker)
	cfg, err := (&XrayService{}).GetXrayConfig()
	if cfg != nil || err == nil {
		t.Fatalf("expected missing fallback table to abort generation, got %v, %v", cfg, err)
	}
	for _, line := range configLogsSince(t, marker) {
		if strings.Contains(line, "Xray config generated:") || strings.Contains(line, "Remove Inbound User") {
			t.Fatalf("failed generation logged client exclusions: %s", line)
		}
	}
}

func configLogsSince(t *testing.T, marker string) []string {
	t.Helper()
	var lines []string
	for _, line := range logger.GetLogs(10000, "DEBUG") {
		if strings.HasSuffix(line, " INFO - "+marker) {
			return lines
		}
		lines = append(lines, line)
	}
	t.Fatal("log boundary not found")
	return nil
}
