package service

import (
	"slices"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/gorm"
)

func (s *ClientService) ResetTrafficByEmail(inboundSvc *InboundService, email string) (bool, error) {
	if email == "" {
		return false, common.NewError("client email is required")
	}
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return false, err
	}
	inboundIds, err := s.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return false, err
	}

	// Decide before any reset erases the reason for the disabled state.
	restore, err := quotaDisabledEmails(database.GetDB(), []string{email})
	if err != nil {
		return false, err
	}
	needRestart := false

	if len(inboundIds) == 0 {
		if rErr := inboundSvc.ResetClientTrafficByEmail(email); rErr != nil {
			return false, rErr
		}
	} else {
		for _, ibId := range inboundIds {
			nr, rErr := inboundSvc.ResetClientTraffic(ibId, email)
			if rErr != nil {
				return needRestart, rErr
			}
			if nr {
				needRestart = true
			}
		}
	}
	// Enabling before zeroing races the next quota tick and turns the client
	// straight back off. An operator's manual disable is never lifted here.
	if !rec.Enable && len(restore) > 0 {
		updated := rec.ToClient()
		updated.Enable = true
		nr, err := s.Update(inboundSvc, rec.Id, *updated)
		if err != nil {
			return needRestart, err
		}
		needRestart = needRestart || nr
	}
	return needRestart, nil
}

func (s *ClientService) BulkResetTraffic(inboundSvc *InboundService, emails []string) (int, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	seen := map[string]struct{}{}
	cleanEmails := make([]string, 0, len(emails))
	for _, e := range emails {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		cleanEmails = append(cleanEmails, e)
	}
	if len(cleanEmails) == 0 {
		return 0, nil
	}

	var affected int64
	var restore []string
	err := submitTrafficWrite(func() error {
		db := database.GetDB()
		return db.Transaction(func(tx *gorm.DB) error {
			var err error
			if restore, err = quotaDisabledEmails(tx, cleanEmails); err != nil {
				return err
			}
			keepOff, err := handDisabledEmails(tx, cleanEmails)
			if err != nil {
				return err
			}
			if affected, err = zeroClientTraffics(tx, cleanEmails, keepOff); err != nil {
				return err
			}
			if err := clearGlobalTraffic(tx, cleanEmails...); err != nil {
				return err
			}
			for _, batch := range chunkStrings(cleanEmails, sqlInChunk) {
				if err := tx.Where("email IN ?", batch).Delete(&model.NodeClientTraffic{}).Error; err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		return 0, err
	}
	inboundSvc.resetAllMtprotoQuotas()
	if err := s.restoreResetClients(inboundSvc, restore); err != nil {
		return int(affected), err
	}
	return int(affected), nil
}

func (s *ClientService) ResetAllClientTraffics(inboundSvc *InboundService, id int) error {
	var restore []string
	err := submitTrafficWrite(func() error {
		var err error
		restore, err = s.resetAllClientTrafficsLocked(id)
		return err
	})
	if err == nil {
		inboundSvc.resetAllMtprotoQuotas()
		return s.restoreResetClients(inboundSvc, restore)
	}
	return err
}

func (s *ClientService) resetAllClientTrafficsLocked(id int) ([]string, error) {
	db := database.GetDB()
	now := time.Now().Unix() * 1000
	var restore []string

	if err := db.Transaction(func(tx *gorm.DB) error {
		// client_traffics.inbound_id is stale: it reflects the inbound the row was
		// first inserted under and is never refreshed. Use the client_inbounds join
		// as the authoritative source for which emails belong to a given inbound.
		var resetEmails []string
		if id == -1 {
			if err := tx.Model(xray.ClientTraffic{}).Pluck("email", &resetEmails).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Table("client_inbounds ci").
				Select("c.email").
				Joins("JOIN clients c ON c.id = ci.client_id").
				Where("ci.inbound_id = ?", id).
				Pluck("c.email", &resetEmails).Error; err != nil {
				return err
			}
		}
		if len(resetEmails) == 0 {
			return nil
		}

		var err error
		if restore, err = quotaDisabledEmails(tx, resetEmails); err != nil {
			return err
		}
		keepOff, err := handDisabledEmails(tx, resetEmails)
		if err != nil {
			return err
		}
		if _, err := zeroClientTraffics(tx, resetEmails, keepOff); err != nil {
			return err
		}

		if err := clearGlobalTraffic(tx, resetEmails...); err != nil {
			return err
		}

		for _, batch := range chunkStrings(resetEmails, sqlInChunk) {
			if err := tx.Where("email IN ?", batch).Delete(&model.NodeClientTraffic{}).Error; err != nil {
				return err
			}
		}

		inboundWhereText := "id "
		if id == -1 {
			inboundWhereText += " > ?"
		} else {
			inboundWhereText += " = ?"
		}

		result := tx.Model(model.Inbound{}).
			Where(inboundWhereText, id).
			Update("last_traffic_reset_time", now)

		return result.Error
	}); err != nil {
		return nil, err
	}
	return restore, nil
}

func (s *ClientService) ResetAllTraffics() (bool, error) {
	var affected int64
	var restore []string
	err := submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var err error
			if restore, err = quotaDisabledEmails(tx, nil); err != nil {
				return err
			}
			keepOff, err := handDisabledEmails(tx, nil)
			if err != nil {
				return err
			}
			if affected, err = zeroClientTraffics(tx, nil, keepOff); err != nil {
				return err
			}
			if err := tx.Where("1 = 1").Delete(&model.ClientGlobalTraffic{}).Error; err != nil {
				return err
			}
			return tx.Where("1 = 1").Delete(&model.NodeClientTraffic{}).Error
		})
	})
	if err != nil {
		return false, err
	}
	inboundSvc := &InboundService{}
	inboundSvc.resetAllMtprotoQuotas()
	if err := s.restoreResetClients(inboundSvc, restore); err != nil {
		return affected > 0, err
	}
	return affected > 0, nil
}

// Apply runtime changes only after the zeroing transaction has committed, and
// once per inbound rather than once per client in a bulk reset.
func (s *ClientService) restoreResetClients(inboundSvc *InboundService, emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	_, needRestart, err := s.bulkSetEnable(inboundSvc, emails, true, false)
	if needRestart {
		(&XrayService{}).SetToNeedRestart()
	}
	if err != nil {
		logger.Warning("Failed to restore quota-disabled clients after traffic reset:", err)
	}
	return err
}

// quotaDisabledEmails must run before resetting counters. A disabled client
// below its quota was switched off by the operator, not the quota loop.
func quotaDisabledEmails(tx *gorm.DB, emails []string) ([]string, error) {
	query := func() *gorm.DB {
		return tx.Model(&xray.ClientTraffic{}).
			Where("email IN (?)", tx.Model(&model.ClientRecord{}).Select("email").Where("enable = ? AND manual_disabled = ?", false, false))
	}
	var rows []xray.ClientTraffic
	if emails == nil {
		if err := query().Find(&rows).Error; err != nil {
			return nil, err
		}
	} else {
		for _, batch := range chunkStrings(emails, sqlInChunk) {
			var page []xray.ClientTraffic
			if err := query().Where("email IN ?", batch).Find(&page).Error; err != nil {
				return nil, err
			}
			rows = append(rows, page...)
		}
	}
	overlayGlobalTrafficValues(tx, rows)
	var restore []string
	for _, row := range rows {
		if row.Total > 0 && row.Up+row.Down >= row.Total {
			restore = append(restore, row.Email)
		}
	}
	return restore, nil
}

func handDisabledEmails(tx *gorm.DB, emails []string) ([]string, error) {
	var disabled []string
	query := func() *gorm.DB { return tx.Model(&model.ClientRecord{}).Where("enable = ?", false) }
	if emails == nil {
		if err := query().Pluck("email", &disabled).Error; err != nil {
			return nil, err
		}
	} else {
		for _, batch := range chunkStrings(emails, sqlInChunk) {
			var page []string
			if err := query().Where("email IN ?", batch).Pluck("email", &page).Error; err != nil {
				return nil, err
			}
			disabled = append(disabled, page...)
		}
	}
	quota, err := quotaDisabledEmails(tx, disabled)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(disabled, func(email string) bool { return slices.Contains(quota, email) }), nil
}

func zeroClientTraffics(tx *gorm.DB, emails, keepOff []string) (int64, error) {
	var affected int64
	reset := map[string]any{"enable": true, "up": 0, "down": 0}
	if emails == nil {
		res := tx.Model(&xray.ClientTraffic{}).Where("1 = 1").Updates(reset)
		if res.Error != nil {
			return 0, res.Error
		}
		affected = res.RowsAffected
	} else {
		for _, batch := range chunkStrings(emails, sqlInChunk) {
			res := tx.Model(&xray.ClientTraffic{}).Where("email IN ?", batch).Updates(reset)
			if res.Error != nil {
				return 0, res.Error
			}
			affected += res.RowsAffected
		}
	}
	for _, batch := range chunkStrings(keepOff, sqlInChunk) {
		if err := tx.Model(&xray.ClientTraffic{}).Where("email IN ?", batch).Update("enable", false).Error; err != nil {
			return 0, err
		}
	}
	return affected, nil
}
