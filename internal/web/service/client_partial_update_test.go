package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestClientPartialUpdatePreservesOmittedAndClearsExplicitFields(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	record := &model.ClientRecord{Email: "partial@x", UUID: "11111111-1111-1111-1111-111111111111", Password: "keep-password", Auth: "keep-auth", Enable: true, TotalGB: 1024, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), LimitIP: 3, Comment: "before", Reverse: `{"tag":"reverse-test"}`}
	if err := database.GetDB().Create(record).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateByEmailJSON(&InboundService{}, record.Email, []byte(`{"comment":"after"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetRecordByEmail(nil, record.Email)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enable || got.TotalGB != 1024 || got.ExpiryTime != record.ExpiryTime || got.LimitIP != 3 || got.UUID != record.UUID || got.Password != record.Password || got.Auth != record.Auth || got.Reverse != record.Reverse || got.Comment != "after" {
		t.Fatalf("omitted fields changed: %+v", got)
	}
	if _, err := svc.UpdateByEmailJSON(&InboundService{}, record.Email, []byte(`{"totalGB":0,"expiryTime":0,"limitIp":0,"reverse":null,"comment":"","enable":false}`)); err != nil {
		t.Fatal(err)
	}
	got, err = svc.GetRecordByEmail(nil, record.Email)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enable || got.TotalGB != 0 || got.ExpiryTime != 0 || got.LimitIP != 0 || got.Reverse != "" || got.Comment != "" || !got.ManualDisabled {
		t.Fatalf("explicit empty fields were ignored: %+v", got)
	}
}

func TestClientPartialUpdateRejectsNonObjects(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `"value"`, `{`} {
		if _, err := (&ClientService{}).UpdateByEmailJSON(&InboundService{}, "unused@x", []byte(body)); err == nil {
			t.Fatalf("accepted non-object %s", body)
		}
	}
}
