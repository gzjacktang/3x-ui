package job

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"gorm.io/gorm"
)

func TestMtprotoPollingSleepsUntilConfigured(t *testing.T) {
	t.Setenv("XUI_BIN_FOLDER", t.TempDir())
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	openDB := func(name string) *gorm.DB {
		t.Helper()
		if err := database.InitDB(filepath.Join(dbDir, name)); err != nil {
			t.Fatal(err)
		}
		db := database.GetDB()
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		return db
	}
	countInboundReads := func(db *gorm.DB) *int {
		t.Helper()
		var reads int
		if err := db.Callback().Query().Before("gorm:query").Register("test_count_mtproto_reads", func(tx *gorm.DB) {
			if tx.Statement.Table == "inbounds" {
				reads++
			}
		}); err != nil {
			t.Fatal(err)
		}
		return &reads
	}
	db := openDB("starting.db")
	reads := countInboundReads(db)
	job := NewMtprotoJob()
	job.Run()
	initial := *reads
	job.Run()
	if initial == 0 || *reads != initial {
		t.Fatalf("empty database: startup read count=%d, after idle tick=%d", initial, *reads)
	}

	svc := &service.InboundService{}
	ib := &model.Inbound{UserId: 1, Protocol: model.MTProto, Port: 46001, Settings: `{"clients":[]}`, StreamSettings: `{}`}
	if _, _, err := svc.AddInbound(ib); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		before := *reads
		job.Run()
		if *reads <= before {
			t.Fatal("saving a disabled local MTProto inbound must wake reconciliation for quota-reset recovery")
		}
	}
	if _, err := svc.DelInbound(ib.Id); err != nil {
		t.Fatal(err)
	}
	job.Run()
	afterDelete := *reads
	job.Run()
	if *reads != afterDelete {
		t.Fatal("deleting the last local MTProto inbound must return reconciliation to idle")
	}

	restored := openDB("restored.db")
	if err := restored.Create(&model.Inbound{UserId: 1, Tag: "restored-mtproto", Protocol: model.MTProto, Port: 46002}).Error; err != nil {
		t.Fatal(err)
	}
	restoredReads := countInboundReads(restored)
	job.Run()
	if *restoredReads == 0 {
		t.Fatal("database restore must wake reconciliation independently of an inbound save")
	}
}
