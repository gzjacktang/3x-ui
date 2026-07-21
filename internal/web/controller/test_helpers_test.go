package controller

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/op/go-logging"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	xuilogger "github.com/mhsanaei/3x-ui/v3/internal/logger"
)

func newHostTestDB(t *testing.T) {
	t.Helper()
	xuilogger.InitLogger(logging.ERROR)
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })
}

type hostEnvelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}
