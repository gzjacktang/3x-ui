package service

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// A renewal may lift a lifecycle stop only when the new limits are usable.
// Off below quota/before expiry is the operator's stop and stays off.
func (s *ClientService) renewalReenables(email string, newTotal, newExpiry int64) bool {
	db := database.GetDB()
	var manualStops int64
	if err := db.Model(&model.ClientRecord{}).Where("email = ? AND manual_disabled = ?", email, true).
		Count(&manualStops).Error; err != nil || manualStops > 0 {
		return false
	}
	var traffic xray.ClientTraffic
	if err := db.Where("email = ?", email).First(&traffic).Error; err != nil || traffic.Enable {
		return false
	}
	var depleted int64
	if err := db.Model(&xray.ClientTraffic{}).
		Where(depletedCond(db)+" AND email = ?", time.Now().UnixMilli(), email).
		Count(&depleted).Error; err != nil || depleted == 0 {
		return false
	}
	// Include a master's usage in the projection, without changing local stats.
	rows := []xray.ClientTraffic{traffic}
	overlayGlobalTrafficValues(db, rows)
	if newTotal > 0 && rows[0].Up+rows[0].Down >= newTotal {
		return false
	}
	return newExpiry <= 0 || newExpiry > time.Now().UnixMilli()
}

func (s *ClientService) renewalReenableFor(email string, expiry, total *int64) bool {
	var traffic xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", email).First(&traffic).Error; err != nil {
		return false
	}
	newTotal, newExpiry := traffic.Total, traffic.ExpiryTime
	if expiry != nil {
		newExpiry = *expiry
	}
	if total != nil {
		newTotal = *total
	}
	return s.renewalReenables(email, newTotal, newExpiry)
}
