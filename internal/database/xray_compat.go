package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/maskcompat"
)

func outboundSockopt(obj map[string]any, create bool) map[string]any {
	stream, _ := obj["streamSettings"].(map[string]any)
	if stream == nil {
		if !create {
			return nil
		}
		stream = map[string]any{}
		obj["streamSettings"] = stream
	}
	sockopt, _ := stream["sockopt"].(map[string]any)
	if sockopt == nil {
		if !create {
			return nil
		}
		sockopt = map[string]any{}
		stream["sockopt"] = sockopt
	}
	return sockopt
}

func migrateFreedomDomainStrategy() error {
	var setting model.Setting
	err := db.Model(model.Setting{}).Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&model.HistoryOfSeeders{SeederName: "FreedomDomainStrategyFix"}).Error
	}
	if err != nil {
		return err
	}

	updated, changed, rErr := rewriteFreedomDomainStrategy(setting.Value)
	if rErr != nil {
		log.Printf("FreedomDomainStrategyFix: skip (invalid xrayTemplateConfig json): %v", rErr)
		return db.Create(&model.HistoryOfSeeders{SeederName: "FreedomDomainStrategyFix"}).Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if changed {
			if err := tx.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").
				Update("value", updated).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "FreedomDomainStrategyFix"}).Error
	})
}

// rewriteFreedomDomainStrategy moves a freedom outbound's legacy strategy keys
// into sockopt.domainStrategy, the placement the core's deprecation warning names.
func rewriteFreedomDomainStrategy(raw string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return raw, false, err
	}
	outbounds, ok := cfg["outbounds"].([]any)
	if !ok {
		return raw, false, nil
	}
	changed := false
	for _, ob := range outbounds {
		obj, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if proto, _ := obj["protocol"].(string); !strings.EqualFold(proto, "freedom") {
			continue
		}
		settings, hasSettings := obj["settings"].(map[string]any)
		_, hasRoot := obj["targetStrategy"]
		_, hasSettingsTarget := settings["targetStrategy"]
		_, hasSettingsDomain := settings["domainStrategy"]
		if !hasRoot && !hasSettingsTarget && !hasSettingsDomain {
			continue
		}
		strategy := freedomMigratedStrategy(obj, settings)
		delete(obj, "targetStrategy")
		if hasSettings {
			delete(settings, "targetStrategy")
			delete(settings, "domainStrategy")
		}
		if strategy != "" {
			outboundSockopt(obj, true)["domainStrategy"] = strategy
		}
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false, err
	}
	return string(out), true, nil
}

// freedomMigratedStrategy clones the core's own resolution order for a freedom
// outbound (infra/conf/freedom.go), returning "" when none of them holds one.
func freedomMigratedStrategy(obj, settings map[string]any) string {
	if s, ok := freedomStrategyValue(obj["targetStrategy"]); ok && !strings.EqualFold(s, "asis") {
		return s
	}
	legacy := settings["targetStrategy"]
	if s, ok := legacy.(string); !ok || s == "" {
		legacy = settings["domainStrategy"]
	}
	if s, ok := freedomStrategyValue(legacy); ok && !strings.EqualFold(s, "asis") {
		return s
	}
	return ""
}

// freedomStrategyValue reports a strategy the core accepts -- anything else is a
// hard load error in freedom and sockopt alike, so it cannot be migrated.
func freedomStrategyValue(value any) (string, bool) {
	s, ok := value.(string)
	if !ok || s == "" {
		return "", false
	}
	if !freedomDomainStrategies[strings.ToLower(s)] {
		return "", false
	}
	return s, true
}

var freedomDomainStrategies = map[string]bool{
	"asis": true, "useip": true, "useipv4": true, "useipv6": true,
	"useipv4v6": true, "useipv6v4": true, "forceip": true, "forceipv4": true,
	"forceipv6": true, "forceipv4v6": true, "forceipv6v4": true,
}

func migrateWireguardDomainStrategy() error {
	var setting model.Setting
	err := db.Model(model.Setting{}).Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&model.HistoryOfSeeders{SeederName: "WireguardDomainStrategyFix"}).Error
	}
	if err != nil {
		return err
	}

	updated, changed, rErr := rewriteWireguardDomainStrategy(setting.Value)
	if rErr != nil {
		log.Printf("WireguardDomainStrategyFix: skip (invalid xrayTemplateConfig json): %v", rErr)
		return db.Create(&model.HistoryOfSeeders{SeederName: "WireguardDomainStrategyFix"}).Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if changed {
			if err := tx.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").
				Update("value", updated).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "WireguardDomainStrategyFix"}).Error
	})
}

// rewriteWireguardDomainStrategy splits the settings.domainStrategy xray-core 26.9.30 (#6771)
// ignores: sockopt.domainStrategy now picks the endpoint's family, targetStrategy the targets'.
func rewriteWireguardDomainStrategy(raw string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return raw, false, err
	}
	outbounds, ok := cfg["outbounds"].([]any)
	if !ok {
		return raw, false, nil
	}
	changed := false
	for _, ob := range outbounds {
		obj, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if proto, _ := obj["protocol"].(string); !strings.EqualFold(proto, "wireguard") {
			continue
		}
		settings, _ := obj["settings"].(map[string]any)
		legacy, hasLegacy := settings["domainStrategy"]
		remoteDNS, _ := settings["remoteDNS"].([]any)
		localDNS := len(remoteDNS) == 1 && remoteDNS[0] == "local"
		if !hasLegacy && !localDNS {
			continue
		}
		delete(settings, "domainStrategy")
		strategy, _ := legacy.(string)
		if !wireguardFamilyStrategies[strings.ToLower(strategy)] {
			strategy = ""
		}
		if strategy != "" {
			if sockopt := outboundSockopt(obj, true); !strategyIsSet(sockopt["domainStrategy"]) {
				sockopt["domainStrategy"] = strategy
			}
		}
		if localDNS {
			delete(settings, "remoteDNS")
			if strategy == "" {
				strategy = "ForceIP"
			}
		}
		if strategy != "" && !strategyIsSet(obj["targetStrategy"]) {
			obj["targetStrategy"] = strategy
		}
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false, err
	}
	return string(out), true, nil
}

// wireguardFamilyStrategies are the old wireguard values that pinned an address family;
// plain ForceIP pinned none, and any other value already failed the old core's load.
var wireguardFamilyStrategies = map[string]bool{
	"forceipv4": true, "forceipv6": true, "forceipv4v6": true, "forceipv6v4": true,
}

func strategyIsSet(value any) bool {
	s, _ := value.(string)
	return s != "" && !strings.EqualFold(s, "asis")
}

// migrateDNSOutboundLegacyKeys rewrites stored dns outbounds once, because the
// core logs nonIPQuery/blockTypes as deprecated on every config load.
func migrateDNSOutboundLegacyKeys() error {
	var setting model.Setting
	err := db.Model(model.Setting{}).Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&model.HistoryOfSeeders{SeederName: "DNSOutboundLegacyKeysFix"}).Error
	}
	if err != nil {
		return err
	}

	updated, changed, rErr := rewriteDNSOutboundLegacyKeys(setting.Value)
	if rErr != nil {
		log.Printf("DNSOutboundLegacyKeysFix: skip (invalid xrayTemplateConfig json): %v", rErr)
		return db.Create(&model.HistoryOfSeeders{SeederName: "DNSOutboundLegacyKeysFix"}).Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if changed {
			if err := tx.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").
				Update("value", updated).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "DNSOutboundLegacyKeysFix"}).Error
	})
}

// rewriteDNSOutboundLegacyKeys turns a dns outbound's legacy nonIPQuery and
// blockTypes into rules, in the order the core's legacy builder used.
func rewriteDNSOutboundLegacyKeys(raw string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return raw, false, err
	}
	outbounds, ok := cfg["outbounds"].([]any)
	if !ok {
		return raw, false, nil
	}
	changed := false
	for _, ob := range outbounds {
		obj, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if proto, _ := obj["protocol"].(string); !strings.EqualFold(proto, "dns") {
			continue
		}
		settings, _ := obj["settings"].(map[string]any)
		if settings == nil {
			continue
		}
		nonIPQuery, hasMode := settings["nonIPQuery"]
		blockTypes, hasTypes := settings["blockTypes"]
		// JSON null is absent to the core, which decides on nil pointers.
		hasMode = hasMode && nonIPQuery != nil
		hasTypes = hasTypes && blockTypes != nil
		if !hasMode && !hasTypes {
			continue
		}
		// The core refuses legacy keys next to real rules, so existing rules win.
		if rules, hasRules := settings["rules"]; !hasRules || rules == nil {
			settings["rules"] = legacyDNSOutboundRules(dnsNonIPQueryMode(nonIPQuery), legacyDNSBlockTypes(blockTypes))
		}
		delete(settings, "nonIPQuery")
		delete(settings, "blockTypes")
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false, err
	}
	return string(out), true, nil
}

// dnsNonIPQueryMode reports the mode the core resolved: everything but drop and
// skip meant reject, and any other value never loaded in the first place.
func dnsNonIPQueryMode(value any) string {
	mode, _ := value.(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "drop" || mode == "skip" {
		return mode
	}
	return "reject"
}

// legacyDNSBlockTypes accepts every shape the old card could save: a list, a
// bare number, or a comma-separated string, minus the qTypes the core rejects.
func legacyDNSBlockTypes(value any) []int {
	items, ok := value.([]any)
	if !ok && value != nil {
		items = []any{value}
	}
	var out []int
	for _, item := range items {
		for _, part := range strings.Split(fmt.Sprint(item), ",") {
			qType, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || qType < 0 || qType > 65535 {
				continue
			}
			out = append(out, qType)
		}
	}
	return out
}

// legacyDNSOutboundRules mirrors the core's own legacy dns policy: the blocked
// qTypes, then the hijack, then the mode's answer for everything else.
func legacyDNSOutboundRules(mode string, blockTypes []int) []any {
	rules := make([]any, 0, 3)
	if len(blockTypes) > 0 {
		rule := map[string]any{"action": "drop", "qType": dnsQTypeValue(blockTypes)}
		if mode == "reject" {
			rule["action"] = "return"
			rule["rCode"] = 5
		}
		rules = append(rules, rule)
	}
	rules = append(rules, map[string]any{"action": "hijack", "qType": "1,28"})
	fallback := map[string]any{"action": "direct"}
	switch mode {
	case "reject":
		fallback["action"] = "return"
		fallback["rCode"] = 5
	case "drop":
		fallback["action"] = "drop"
	}
	return append(rules, fallback)
}

// dnsQTypeValue keeps a lone qType a number the way the core marshals one, except
// 0: the core drops a numeric 0, and a rule with no qTypes matches every query.
func dnsQTypeValue(blockTypes []int) any {
	if len(blockTypes) == 1 && blockTypes[0] != 0 {
		return blockTypes[0]
	}
	parts := make([]string, 0, len(blockTypes))
	for _, qType := range blockTypes {
		parts = append(parts, strconv.Itoa(qType))
	}
	return strings.Join(parts, ",")
}

// migrateDNSOutboundQTypeZero repairs the numeric qType 0 that 3.8.0's legacy-keys
// seeder stored, which that seeder's own history row keeps it from revisiting.
func migrateDNSOutboundQTypeZero() error {
	var setting model.Setting
	err := db.Model(model.Setting{}).Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&model.HistoryOfSeeders{SeederName: "DNSOutboundQTypeZeroFix"}).Error
	}
	if err != nil {
		return err
	}

	updated, changed, rErr := RewriteDNSOutboundQTypeZero(setting.Value)
	if rErr != nil {
		log.Printf("DNSOutboundQTypeZeroFix: skip (invalid xrayTemplateConfig json): %v", rErr)
		return db.Create(&model.HistoryOfSeeders{SeederName: "DNSOutboundQTypeZeroFix"}).Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if changed {
			if err := tx.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").
				Update("value", updated).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "DNSOutboundQTypeZeroFix"}).Error
	})
}

// RewriteDNSOutboundQTypeZero spells a dns rule's numeric qType 0 as "0", the one
// form the core reads as query type 0 rather than as every query.
func RewriteDNSOutboundQTypeZero(raw string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return raw, false, err
	}
	outbounds, _ := cfg["outbounds"].([]any)
	changed := false
	for _, ob := range outbounds {
		obj, _ := ob.(map[string]any)
		if proto, _ := obj["protocol"].(string); !strings.EqualFold(proto, "dns") {
			continue
		}
		settings, _ := obj["settings"].(map[string]any)
		rules, _ := settings["rules"].([]any)
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			if qType, ok := rule["qType"].(float64); ok && qType == 0 {
				rule["qType"] = "0"
				changed = true
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false, err
	}
	return string(out), true, nil
}

func migrateXdnsFinalmaskShape() error {
	return db.Transaction(func(tx *gorm.DB) error {
		var inbounds []model.Inbound
		if err := tx.Select("id", "stream_settings").Find(&inbounds).Error; err != nil {
			return err
		}
		for _, inbound := range inbounds {
			if updated, changed := upgradeFinalmasksJSON(inbound.StreamSettings, streamFinalmask, false, maskcompat.UpgradeLegacyXdns); changed {
				if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).Update("stream_settings", updated).Error; err != nil {
					return err
				}
			}
		}
		var setting model.Setting
		err := tx.Where("key = ?", "xrayTemplateConfig").First(&setting).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if updated, changed := upgradeFinalmasksJSON(setting.Value, templateFinalmasks, true, maskcompat.UpgradeLegacyXdns); changed {
				if err := tx.Model(&model.Setting{}).Where("key = ?", setting.Key).Update("value", updated).Error; err != nil {
					return err
				}
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "XdnsFinalmaskNamesAddrsFix"}).Error
	})
}

// upgradeFinalmasksJSON runs upgrade on the finalmasks locate finds in one stored JSON
// document, leaving the document byte-for-byte alone when nothing in it changed.
func upgradeFinalmasksJSON(raw string, locate func(any) []any, indent bool, upgrade func(any) bool) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, false
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return raw, false
	}
	changed := false
	for _, mask := range locate(doc) {
		if upgrade(mask) {
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	var out []byte
	var err error
	if indent {
		out, err = json.MarshalIndent(doc, "", "  ")
	} else {
		out, err = json.Marshal(doc)
	}
	if err != nil {
		return raw, false
	}
	return string(out), true
}

func streamFinalmask(doc any) []any {
	stream, _ := doc.(map[string]any)
	return []any{stream["finalmask"]}
}

func outboundListFinalmasks(doc any) []any {
	list, _ := doc.([]any)
	finalmasks := make([]any, 0, len(list))
	for _, entry := range list {
		obj, _ := entry.(map[string]any)
		finalmasks = append(finalmasks, streamFinalmask(obj["streamSettings"])...)
	}
	return finalmasks
}

func templateFinalmasks(doc any) []any {
	cfg, _ := doc.(map[string]any)
	return append(outboundListFinalmasks(cfg["inbounds"]), outboundListFinalmasks(cfg["outbounds"])...)
}

// RewriteXrayTemplateCompat repairs retained legacy protocol fields after a backup
// restore or import as well as during the one-time database migration.
func RewriteXrayTemplateCompat(raw string) (string, bool, error) {
	changed := false
	for _, rewrite := range []func(string) (string, bool, error){
		rewriteFreedomDomainStrategy, rewriteDNSOutboundLegacyKeys, rewriteWireguardDomainStrategy, RewriteDNSOutboundQTypeZero,
	} {
		next, didChange, err := rewrite(raw)
		if err != nil {
			return raw, changed, err
		}
		raw, changed = next, changed || didChange
	}
	next, didChange := upgradeFinalmasksJSON(raw, templateFinalmasks, true, maskcompat.UpgradeLegacyXdns)
	return next, changed || didChange, nil
}

// RewriteStreamXdnsCompat leaves streams byte-for-byte unchanged unless an
// old XDNS mask needs upgrading for the pinned core.
func RewriteStreamXdnsCompat(raw string) (string, bool) {
	return upgradeFinalmasksJSON(raw, streamFinalmask, false, maskcompat.UpgradeLegacyXdns)
}
