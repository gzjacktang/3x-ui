package service

import (
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func setClientManualStop(email string, disabled bool) error {
	return runSerializedTx(func(tx *gorm.DB) error {
		return tx.Model(&model.ClientRecord{}).Where("email = ?", email).
			UpdateColumn("manual_disabled", disabled).Error
	})
}
