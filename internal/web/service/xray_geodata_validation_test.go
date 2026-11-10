package service

import (
	"strconv"
	"strings"
	"testing"
)

func TestCheckXrayConfig_RefusesGeodataTheCoreRefuses(t *testing.T) {
	const outbounds = `"outbounds":[{"protocol":"freedom","tag":"direct"}]`
	geodata := func(cron string, files ...string) string {
		assets := make([]string, len(files))
		for i, f := range files {
			assets[i] = `{"url":"https://example.com/` + f + `","file":"` + f + `"}`
		}
		return `{"geodata":{"cron":"` + cron + `","assets":[` + strings.Join(assets, ",") + `]},` + outbounds + `}`
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "geo" + strconv.Itoa(i) + ".dat"
	}
	tests := []struct{ name, template, want string }{
		{"file that is not .dat", geodata("0 4 * * *", "custom.mmdb"), "file extension must be .dat"},
		{"more than 100 assets", geodata("0 4 * * *", tooMany...), "at most 100 assets"},
		{"cron the core cannot parse", geodata("every day", "custom.dat"), "invalid geodata cron"},
		{"interval under a minute", geodata("@every 30s", "custom.dat"), "at least 1 minute"},
		{"upper-case extension on a daily cron", geodata("0 4 * * *", "Custom.DAT"), ""},
		{"no cron leaves the assets unscheduled", geodata("", "custom.mmdb"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (&XraySettingService{}).CheckXrayConfig(tc.template)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckXrayConfig = %v, want the geodata the core accepts kept", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckXrayConfig = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
