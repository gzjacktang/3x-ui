package service

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// checkTemplateGeodata applies the checks xray-core 26.10.29 (#7267) runs on a scheduled geodata
// section only when the core starts, where a refusal stops every inbound.
func checkTemplateGeodata(raw []byte) error {
	var geodata struct {
		Cron   string `json:"cron"`
		Assets []struct {
			File string `json:"file"`
		} `json:"assets"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &geodata) != nil || geodata.Cron == "" {
		return nil
	}
	if len(geodata.Assets) > 100 {
		return common.NewError("xray template geodata: xray-core supports at most 100 assets.")
	}
	for _, asset := range geodata.Assets {
		if !strings.EqualFold(filepath.Ext(asset.File), ".dat") {
			return common.NewError(`xray template geodata: file extension must be .dat, not "` + asset.File + `".`)
		}
	}
	schedule, err := cron.ParseStandard(geodata.Cron)
	if err != nil {
		return common.NewError("xray template geodata: invalid geodata cron:", err)
	}
	if interval, ok := schedule.(cron.ConstantDelaySchedule); ok && interval.Delay < time.Minute {
		return common.NewError("xray template geodata: the cron interval must be at least 1 minute.")
	}
	return nil
}
