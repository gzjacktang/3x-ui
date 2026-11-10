package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/xtls/xray-core/common/platform/filesystem"
	"github.com/xtls/xray-core/infra/conf"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

func localizeNoiseScripts(stream *conf.StreamConfig) {
	if stream == nil || stream.FinalMask == nil {
		return
	}
	for i := range stream.FinalMask.Udp {
		mask := &stream.FinalMask.Udp[i]
		if !strings.EqualFold(mask.Type, "noise") || mask.Settings == nil {
			continue
		}
		var settings map[string]any
		if json.Unmarshal(*mask.Settings, &settings) != nil {
			continue
		}
		script, _ := settings["script"].(string)
		resolved := childLuaScriptPath(script)
		if resolved == script {
			continue
		}
		settings["script"] = resolved
		if raw, err := json.Marshal(settings); err == nil {
			localized := json.RawMessage(raw)
			mask.Settings = &localized
		}
	}
}

// childLuaScriptPath resolves a relative Lua script the way the xray child does: it shares the
// panel's working directory, then looks in its own bin folder instead of the panel's.
func childLuaScriptPath(script string) string {
	if script == "" || filepath.IsAbs(script) {
		return script
	}
	if _, err := filesystem.ResolveLuaFile(script); err == nil {
		return script
	}
	binDir, err := filepath.Abs(config.GetBinFolderPath())
	if err != nil {
		return script
	}
	inBin := filepath.Join(binDir, script)
	if _, err := os.Stat(inBin); err != nil {
		return script
	}
	return inBin
}
