package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestTrafficResetPreservesManualStops(t *testing.T) {
	for _, reset := range []string{"single", "bulk", "inbound", "all"} {
		t.Run(reset, func(t *testing.T) {
			setupBulkDB(t)
			svc, inbounds := &ClientService{}, &InboundService{}
			ib := seedLocalDisabledClient(t, svc, 53301, "", "manual@x", 1000, 0, 10, 20)
			var err error
			switch reset {
			case "single":
				_, err = svc.ResetTrafficByEmail(inbounds, "manual@x")
			case "bulk":
				_, err = svc.BulkResetTraffic(inbounds, []string{"manual@x"})
			case "inbound":
				err = svc.ResetAllClientTraffics(inbounds, ib.Id)
			case "all":
				_, err = svc.ResetAllTraffics()
			}
			if err != nil {
				t.Fatal(err)
			}
			assertEnableEverywhere(t, svc, inbounds, ib.Id, "manual@x", false)
			if row := trafficOf(t, "manual@x"); row.Up != 0 || row.Down != 0 {
				t.Fatalf("traffic not reset: %+v", row)
			}
		})
	}
}

func TestAutoRenewPreservesManualStopAcrossSharedInbounds(t *testing.T) {
	for _, lifecycleStop := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "expiry"}[lifecycleStop], func(t *testing.T) {
			setupBulkDB(t)
			svc, inbounds := &ClientService{}, &InboundService{}
			db := database.GetDB()
			expiry := time.Now().Add(-time.Hour).UnixMilli()
			client := model.Client{Email: "renew@x", ID: "11111111-1111-1111-1111-111111111111", Enable: false, ExpiryTime: expiry, Reset: 30}
			ibA := mkInbound(t, 53302, model.VLESS, clientsSettings(t, []model.Client{client}))
			ibB := mkInbound(t, 53303, model.VLESS, clientsSettings(t, []model.Client{client}))
			for _, ib := range []*model.Inbound{ibA, ibB} {
				if err := svc.SyncInbound(nil, ib.Id, []model.Client{client}); err != nil {
					t.Fatal(err)
				}
			}
			stamp := expiry - 10000
			if lifecycleStop {
				stamp = expiry + 1000
			}
			if err := db.Model(&model.ClientRecord{}).Where("email = ?", client.Email).
				Updates(map[string]any{"enable": false, "manual_disabled": !lifecycleStop, "updated_at": stamp}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&xray.ClientTraffic{Email: client.Email, InboundId: ibA.Id, Enable: false, ExpiryTime: expiry, Reset: 30, Up: 10}).Error; err != nil {
				t.Fatal(err)
			}
			if _, count, err := inbounds.autoRenewClients(db); err != nil || count != 1 {
				t.Fatalf("renew: count=%d err=%v", count, err)
			}
			for _, ib := range []*model.Inbound{ibA, ibB} {
				assertEnableEverywhere(t, svc, inbounds, ib.Id, client.Email, lifecycleStop)
			}
			if row := trafficOf(t, client.Email); row.ExpiryTime <= time.Now().UnixMilli() || row.Up != 0 {
				t.Fatalf("period did not renew: %+v", row)
			}
		})
	}
}

func TestRuntimeInboundFiltersSharedDisabledClientByEmail(t *testing.T) {
	setupBulkDB(t)
	svc := &InboundService{}
	ib := &model.Inbound{Id: 92, Protocol: model.VLESS, Settings: `{"clients":[{"email":"shared@x","enable":true},{"email":"manual@x","enable":false},{"email":"on@x","enable":true}]}`}
	if err := database.GetDB().Create(&xray.ClientTraffic{Email: "shared@x", InboundId: 11, Enable: false}).Error; err != nil {
		t.Fatal(err)
	}
	built, err := svc.buildRuntimeInboundForAPI(database.GetDB(), ib)
	if err != nil {
		t.Fatal(err)
	}
	clients, err := svc.GetClients(built)
	if err != nil || len(clients) != 1 || clients[0].Email != "on@x" {
		t.Fatalf("runtime clients=%+v err=%v", clients, err)
	}
}
