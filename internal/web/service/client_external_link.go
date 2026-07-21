package service

import (
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/link"

	"gorm.io/gorm"
)

// ExternalLinkInput is one row from the client form's Links tab.
type ExternalLinkInput struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Remark string `json:"remark"`
}

func (s *ClientService) GetExternalLinksForRecord(id int) ([]model.ClientExternalLink, error) {
	var rows []model.ClientExternalLink
	if err := database.GetDB().
		Where("client_id = ?", id).
		Order("sort_index ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func normalizeExternalLinks(inputs []ExternalLinkInput) ([]model.ClientExternalLink, error) {
	out := make([]model.ClientExternalLink, 0, len(inputs))
	for _, in := range inputs {
		value := strings.TrimSpace(in.Value)
		if value == "" {
			continue
		}
		kind := strings.TrimSpace(in.Kind)
		switch kind {
		case model.ExternalLinkKindLink, "":
			kind = model.ExternalLinkKindLink
			if _, err := link.ParseLink(value); err != nil {
				return nil, common.NewError("unsupported or invalid share link: " + value)
			}
		default:
			return nil, common.NewError("unknown external link kind: " + kind)
		}
		out = append(out, model.ClientExternalLink{
			Kind:      kind,
			Value:     value,
			Remark:    strings.TrimSpace(in.Remark),
			SortIndex: len(out),
		})
	}
	return out, nil
}

// SetExternalLinksForRecord replaces a client's entire external-link set.
func (s *ClientService) SetExternalLinksForRecord(id int, inputs []ExternalLinkInput) error {
	rows, err := normalizeExternalLinks(inputs)
	if err != nil {
		return err
	}
	db := database.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("client_id = ?", id).Delete(&model.ClientExternalLink{}).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].ClientId = id
			if err := tx.Create(&rows[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *ClientService) SetExternalLinksByEmail(email string, inputs []ExternalLinkInput) error {
	if strings.TrimSpace(email) == "" {
		return common.NewError("client email is required")
	}
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return err
	}
	return s.SetExternalLinksForRecord(rec.Id, inputs)
}
