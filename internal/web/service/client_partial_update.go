package service

import (
	"encoding/json"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// UpdateByEmailJSON preserves omitted fields. Decoding onto the stored value
// still honors explicit false, zero, empty strings and null object fields.
func (s *ClientService) UpdateByEmailJSON(inboundSvc *InboundService, email string, body []byte, inboundFilter ...int) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return false, err
	}
	if fields == nil {
		return false, common.NewError("client update must be a JSON object")
	}
	record, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return false, err
	}
	updated := *record.ToClient()
	if err := json.Unmarshal(body, &updated); err != nil {
		return false, err
	}
	keepInboundFlows := true
	allowRenewal := true
	for field := range fields {
		if strings.EqualFold(field, "flow") {
			keepInboundFlows = false
		}
		if strings.EqualFold(field, "enable") && !updated.Enable {
			allowRenewal = false
		}
	}
	return s.update(inboundSvc, record.Id, updated, keepInboundFlows, allowRenewal, inboundFilter)
}

func inboundFlowOverrides(recordID int) (map[int]string, error) {
	var links []model.ClientInbound
	if err := database.GetDB().Select("inbound_id", "flow_override").
		Where("client_id = ?", recordID).Find(&links).Error; err != nil {
		return nil, err
	}
	flows := make(map[int]string, len(links))
	for _, link := range links {
		flows[link.InboundId] = link.FlowOverride
	}
	return flows, nil
}
