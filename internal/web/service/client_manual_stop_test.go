package service

import (
	"bytes"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestManualStopSurvivesExpiryEditRenewalAndReset(t *testing.T) {
	setupBulkDB(t)
	db := database.GetDB()
	svc, inboundSvc := &ClientService{}, &InboundService{}
	email := "manual-sticky@x"
	client := model.Client{Email: email, ID: "11111111-1111-1111-1111-111111111111", Enable: true, TotalGB: 100, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Reset: 30}
	ib := mkInbound(t, 53401, model.VLESS, clientsSettings(t, []model.Client{client}))
	if err := svc.SyncInbound(nil, ib.Id, []model.Client{client}); err != nil {
		t.Fatal(err)
	}
	mkTraffic(t, ib.Id, email, 10, 20, 100, client.ExpiryTime, true)
	if _, _, err := svc.SetClientEnableByEmail(inboundSvc, email, false); err != nil {
		t.Fatal(err)
	}
	// Move the deadline behind the clock, then edit unrelated metadata after it.
	expired := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := svc.ResetClientExpiryTimeByEmail(inboundSvc, email, expired); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", email).Update("reset", 30).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateByEmailJSON(inboundSvc, email, []byte(`{"comment":"edited after expiry"}`)); err != nil {
		t.Fatal(err)
	}
	if _, count, err := inboundSvc.autoRenewClients(db); err != nil || count != 1 {
		t.Fatalf("auto renew count=%d err=%v", count, err)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, false)
	if _, err := svc.ResetTrafficByEmail(inboundSvc, email); err != nil {
		t.Fatal(err)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, false)
	// A deliberate on switch clears the sticky stop and restores normal service.
	if _, _, err := svc.SetClientEnableByEmail(inboundSvc, email, true); err != nil {
		t.Fatal(err)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, true)
	rec, err := svc.GetRecordByEmail(nil, email)
	if err != nil {
		t.Fatal(err)
	}
	var manual bool
	if err := db.Model(rec).Where("id = ?", rec.Id).Pluck("manual_disabled", &manual).Error; err != nil {
		t.Fatal(err)
	}
	if manual {
		t.Fatal("explicit enable kept the manual-stop marker")
	}
}

func TestExplicitFalseRenewalStaysManualUntilExplicitEnable(t *testing.T) {
	setupBulkDB(t)
	svc, inboundSvc := &ClientService{}, &InboundService{}
	email := "manual-renew@x"
	ib := seedRenewableDepletedClient(t, svc, 53402, email, 100, 0, 60, 40)
	// Repeating off on an already quota-disabled client must still mark intent.
	body := []byte(`{"totalGB":1000,"enable":false}`)
	if _, err := svc.UpdateByEmailJSON(inboundSvc, email, body); err != nil {
		t.Fatal(err)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, false)
	if _, _, err := svc.BulkSetEnable(inboundSvc, []string{email}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.BulkAdjust(inboundSvc, []string{email}, 0, 1000, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BulkResetTraffic(inboundSvc, []string{email}); err != nil {
		t.Fatal(err)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, false)
	if _, _, err := svc.BulkSetEnable(inboundSvc, []string{email}, true); err != nil {
		t.Fatal(err)
	}
	assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, true)
}

func TestSyncInboundPreservesNewExplicitDisabledState(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	client := model.Client{Email: "new-off@x", ID: "11111111-1111-1111-1111-111111111111", Enable: false}
	ib := mkInbound(t, 53403, model.VLESS, clientsSettings(t, []model.Client{client}))
	if err := svc.SyncInbound(nil, ib.Id, []model.Client{client}); err != nil {
		t.Fatal(err)
	}
	record, err := svc.GetRecordByEmail(nil, client.Email)
	if err != nil {
		t.Fatal(err)
	}
	if record.Enable {
		t.Fatal("GORM default:true replaced explicit disabled state")
	}
	var manual bool
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("id = ?", record.Id).Pluck("manual_disabled", &manual).Error; err != nil {
		t.Fatal(err)
	}
	if !manual {
		t.Fatal("new disabled client lost manual-stop marker")
	}
	encoded, err := record.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("manual")) {
		t.Fatal("internal manual-stop marker leaked into the API")
	}
}

func TestTrafficResetPreservesExplicitManualStopEvenWhenDepleted(t *testing.T) {
	for _, reset := range []string{"single", "bulk", "inbound", "all"} {
		t.Run(reset, func(t *testing.T) {
			setupBulkDB(t)
			svc, inboundSvc := &ClientService{}, &InboundService{}
			email := "manual-over-quota@x"
			ib := seedRenewableDepletedClient(t, svc, 53404, email, 100, 0, 60, 40)
			if _, _, err := svc.BulkSetEnable(inboundSvc, []string{email}, false); err != nil {
				t.Fatal(err)
			}
			var err error
			switch reset {
			case "single":
				_, err = svc.ResetTrafficByEmail(inboundSvc, email)
			case "bulk":
				_, err = svc.BulkResetTraffic(inboundSvc, []string{email})
			case "inbound":
				err = svc.ResetAllClientTraffics(inboundSvc, ib.Id)
			case "all":
				_, err = svc.ResetAllTraffics()
			}
			if err != nil {
				t.Fatal(err)
			}
			assertEnableEverywhere(t, svc, inboundSvc, ib.Id, email, false)
		})
	}
}
