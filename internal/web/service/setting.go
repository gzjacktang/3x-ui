package service

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xlzd/gotp"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netproxy"
	"github.com/mhsanaei/3x-ui/v3/internal/util/random"
	"github.com/mhsanaei/3x-ui/v3/internal/util/reflect_util"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

//go:embed config.json
var xrayTemplateConfig string

const (
	DefaultRemarkTemplate = "{{INBOUND}}-{{EMAIL}}|📊{{TRAFFIC_LEFT}}|⏳{{DAYS_LEFT}}D"
	maxRegexLength        = 2048
)

var defaultValueMap = map[string]string{
	"xrayTemplateConfig": xrayTemplateConfig,
	"webListen":          "",
	"webDomain":          "",
	"webPort":            "2053",
	"webCertFile":        "",
	"webKeyFile":         "",
	"secret":             random.Seq(32),
	"panelGuid":          uuid.NewString(),
	"apiToken":           "",
	// Node mTLS material (opt-in). All default empty: the CA + master client
	// cert are minted lazily on first use, and the node-side trust CA is pasted
	// in by the operator. Kept out of entity.AllSetting so private keys never
	// reach the settings UI/export.
	"nodeMtlsCaCertPem":           "",
	"nodeMtlsCaKeyPem":            "",
	"nodeMtlsClientCertPem":       "",
	"nodeMtlsClientKeyPem":        "",
	"nodeMtlsClientCAPem":         "",
	"webBasePath":                 normalizeBasePath(getEnv("XUI_INIT_WEB_BASE_PATH", "/")),
	"sessionMaxAge":               "360",
	"trustedProxyCIDRs":           "127.0.0.1/32,::1/128",
	"pageSize":                    "25",
	"expireDiff":                  "0",
	"trafficDiff":                 "0",
	"remarkTemplate":              DefaultRemarkTemplate,
	"timeLocation":                "Local",
	"twoFactorEnable":             "false",
	"twoFactorToken":              "",
	"datepicker":                  "gregorian",
	"warp":                        "",
	"warpUpdateInterval":          "0",
	"nord":                        "",
	"externalTrafficInformEnable": "false",
	"externalTrafficInformURI":    "",
	"restartXrayOnClientDisable":  "true",
	"xrayOutboundTestUrl":         "https://www.google.com/generate_204",
	"panelOutbound":               "",
	"devChannelEnable":            "false",

	// LDAP defaults
	"ldapEnable":             "false",
	"ldapHost":               "",
	"ldapPort":               "389",
	"ldapUseTLS":             "false",
	"ldapInsecureSkipVerify": "false",
	"ldapBindDN":             "",
	"ldapPassword":           "",
	"ldapBaseDN":             "",
	"ldapUserFilter":         "(objectClass=person)",
	"ldapUserAttr":           "mail",
	"ldapVlessField":         "vless_enabled",
	"ldapSyncCron":           "@every 1m",
	"ldapFlagField":          "",
	"ldapTruthyValues":       "true,1,yes,on",
	"ldapInvertFlag":         "false",
	"ldapInboundTags":        "",
	"ldapAutoCreate":         "false",
	"ldapAutoDelete":         "false",
	"ldapDefaultTotalGB":     "0",
	"ldapDefaultExpiryDays":  "0",
	"ldapDefaultLimitIP":     "0",
}

// SettingService provides business logic for application settings management.
// It handles configuration storage, retrieval, and validation for all system settings.
type SettingService struct{}

func (s *SettingService) GetDefaultJSONConfig() (any, error) {
	var jsonData any
	err := json.Unmarshal([]byte(xrayTemplateConfig), &jsonData)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

func (s *SettingService) GetAllSetting() (*entity.AllSetting, error) {
	db := database.GetDB()
	settings := make([]*model.Setting, 0)
	err := db.Model(model.Setting{}).Not("key = ?", "xrayTemplateConfig").Find(&settings).Error
	if err != nil {
		return nil, err
	}
	allSetting := &entity.AllSetting{}
	t := reflect.TypeFor[entity.AllSetting]()
	v := reflect.ValueOf(allSetting).Elem()
	fields := reflect_util.GetFields(t)

	setSetting := func(key, value string) (err error) {
		defer func() {
			panicErr := recover()
			if panicErr != nil {
				err = errors.New(fmt.Sprint(panicErr))
			}
		}()

		var found bool
		var field reflect.StructField
		for _, f := range fields {
			if f.Tag.Get("json") == key {
				field = f
				found = true
				break
			}
		}

		if !found {
			// Some settings are automatically generated, no need to return to the front end to modify the user
			return nil
		}

		fieldV := v.FieldByName(field.Name)
		switch t := fieldV.Interface().(type) {
		case int:
			n, err := strconv.ParseInt(effectiveSettingValue(key, value), 10, 64)
			if err != nil {
				return err
			}
			fieldV.SetInt(n)
		case string:
			fieldV.SetString(value)
		case bool:
			fieldV.SetBool(effectiveSettingValue(key, value) == "true")
		default:
			return common.NewErrorf("unknown field %v type %v", key, t)
		}
		return
	}

	keyMap := map[string]bool{}
	for _, setting := range settings {
		err := setSetting(setting.Key, setting.Value)
		if err != nil {
			return nil, err
		}
		keyMap[setting.Key] = true
	}

	for key, value := range defaultValueMap {
		if keyMap[key] {
			continue
		}
		err := setSetting(key, value)
		if err != nil {
			return nil, err
		}
	}

	return allSetting, nil
}

func (s *SettingService) GetAllSettingView() (*entity.AllSettingView, error) {
	allSetting, err := s.GetAllSetting()
	if err != nil {
		return nil, err
	}
	view := &entity.AllSettingView{AllSetting: *allSetting}
	view.HasTwoFactorToken = secretConfigured(allSetting.TwoFactorToken)
	view.HasLdapPassword = secretConfigured(allSetting.LdapPassword)
	view.HasWarpSecret = secretConfigured(mustString(s.GetWarp()))
	view.HasNordSecret = secretConfigured(mustString(s.GetNord()))
	var apiTokenCount int64
	if err := database.GetDB().Model(model.ApiToken{}).Where("enabled = ?", true).Count(&apiTokenCount).Error; err == nil {
		view.HasApiToken = apiTokenCount > 0
	}
	view.TwoFactorToken = ""
	view.LdapPassword = ""
	return view, nil
}

func secretConfigured(value string) bool {
	return strings.TrimSpace(value) != ""
}

func mustString(value string, _ error) string {
	return value
}

func getEnv(key, fallback string) string {
	val, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return fallback
	}
	return val
}

func (s *SettingService) ResetSettings() error {
	db := database.GetDB()
	err := db.Where("1 = 1").Delete(model.Setting{}).Error
	if err != nil {
		return err
	}
	return db.Model(model.User{}).
		Where("1 = 1").Error
}

func (s *SettingService) getSetting(key string) (*model.Setting, error) {
	db := database.GetDB()
	setting := &model.Setting{}
	err := db.Model(model.Setting{}).Where("key = ?", key).First(setting).Error
	if err != nil {
		return nil, err
	}
	return setting, nil
}

func (s *SettingService) saveSetting(key string, value string) error {
	setting, err := s.getSetting(key)
	db := database.GetDB()
	if database.IsNotFound(err) {
		return db.Create(&model.Setting{
			Key:   key,
			Value: value,
		}).Error
	} else if err != nil {
		return err
	}
	setting.Key = key
	setting.Value = value
	return db.Save(setting).Error
}

func (s *SettingService) getString(key string) (string, error) {
	setting, err := s.getSetting(key)
	if database.IsNotFound(err) {
		value, ok := defaultValueMap[key]
		if !ok {
			return "", common.NewErrorf("key <%v> not in defaultValueMap", key)
		}
		return value, nil
	} else if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *SettingService) setString(key string, value string) error {
	return s.saveSetting(key, value)
}

func effectiveSettingValue(key, stored string) string {
	if stored == "" {
		if def, ok := defaultValueMap[key]; ok {
			return def
		}
	}
	return stored
}

func (s *SettingService) getBool(key string) (bool, error) {
	str, err := s.getString(key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(effectiveSettingValue(key, str))
}

func (s *SettingService) setBool(key string, value bool) error {
	return s.setString(key, strconv.FormatBool(value))
}

func (s *SettingService) getInt(key string) (int, error) {
	str, err := s.getString(key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(effectiveSettingValue(key, str))
}

func (s *SettingService) setInt(key string, value int) error {
	return s.setString(key, strconv.Itoa(value))
}

func (s *SettingService) GetWarpLastUpdate() (int64, error) {
	val, err := s.getString("warpLastUpdate")
	if err != nil || val == "" {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

func (s *SettingService) SetWarpLastUpdate(val int64) error {
	return s.saveSetting("warpLastUpdate", strconv.FormatInt(val, 10))
}

func (s *SettingService) SetWarpUpdateInterval(val int) error {
	return s.setInt("warpUpdateInterval", val)
}

func (s *SettingService) GetXrayConfigTemplate() (string, error) {
	return s.getString("xrayTemplateConfig")
}

func (s *SettingService) GetXrayOutboundTestUrl() (string, error) {
	return s.getString("xrayOutboundTestUrl")
}

func (s *SettingService) SetXrayOutboundTestUrl(url string) error {
	clean, err := SanitizeHTTPURL(url)
	if err != nil {
		return err
	}
	return s.setString("xrayOutboundTestUrl", clean)
}

func (s *SettingService) GetListen() (string, error) {
	return s.getString("webListen")
}

func (s *SettingService) SetListen(ip string) error {
	return s.setString("webListen", ip)
}

func (s *SettingService) GetWebDomain() (string, error) {
	return s.getString("webDomain")
}

func (s *SettingService) GetPanelOutbound() (string, error) {
	return s.getString("panelOutbound")
}

func (s *SettingService) SetPanelOutbound(tag string) error {
	return s.setString("panelOutbound", tag)
}

// PanelEgressProxyURL resolves the loopback SOCKS bridge that the generated
// config exposes when a panel outbound is configured (see injectPanelEgress).
// It returns "" — meaning a direct connection — when the feature is off or
// the bridge is not present in the running core yet.
func (s *SettingService) PanelEgressProxyURL() string {
	tag, err := s.GetPanelOutbound()
	if err != nil || tag == "" {
		return ""
	}
	proc := XrayProcess()
	if proc == nil || !proc.IsRunning() {
		logger.Warning("panel outbound [", tag, "] is set but Xray is not running, using a direct connection")
		return ""
	}
	cfg := proc.GetConfig()
	if cfg == nil {
		return ""
	}
	for i := range cfg.InboundConfigs {
		if cfg.InboundConfigs[i].Tag == PanelEgressInboundTag {
			return fmt.Sprintf("socks5://127.0.0.1:%d", cfg.InboundConfigs[i].Port)
		}
	}
	logger.Warning("panel outbound [", tag, "] is set but the egress bridge is not in the running config, using a direct connection")
	return ""
}

// NewProxiedHTTPClient returns an HTTP client that routes the panel's own
// outbound requests through the configured panel outbound (via the loopback
// SOCKS bridge in the running Xray). When the feature is off or the bridge
// is unavailable it falls back to a direct client.
func (s *SettingService) NewProxiedHTTPClient(timeout time.Duration) *http.Client {
	proxyUrl := s.PanelEgressProxyURL()
	client, err := netproxy.NewHTTPClient(proxyUrl, timeout)
	if err != nil {
		logger.Warningf("Invalid panel egress proxy %q, using direct connection: %v", proxyUrl, err)
		return &http.Client{Timeout: timeout}
	}
	return client
}

func (s *SettingService) GetTwoFactorEnable() (bool, error) {
	return s.getBool("twoFactorEnable")
}

func (s *SettingService) SetTwoFactorEnable(value bool) error {
	return s.setBool("twoFactorEnable", value)
}

func (s *SettingService) GetTwoFactorToken() (string, error) {
	return s.getString("twoFactorToken")
}

func (s *SettingService) SetTwoFactorToken(value string) error {
	return s.setString("twoFactorToken", value)
}

func (s *SettingService) VerifyTwoFactorCode(code string) error {
	enabled, err := s.GetTwoFactorEnable()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	token, err := s.GetTwoFactorToken()
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" || !gotp.NewDefaultTOTP(token).Verify(strings.TrimSpace(code), time.Now().Unix()) {
		return common.NewError("invalid two factor code")
	}
	return nil
}

func (s *SettingService) GetPort() (int, error) {
	return s.getInt("webPort")
}

func (s *SettingService) SetPort(port int) error {
	return s.setInt("webPort", port)
}

func (s *SettingService) SetCertFile(webCertFile string) error {
	return s.setString("webCertFile", webCertFile)
}

func (s *SettingService) GetCertFile() (string, error) {
	return s.getString("webCertFile")
}

func (s *SettingService) SetKeyFile(webKeyFile string) error {
	return s.setString("webKeyFile", webKeyFile)
}

func (s *SettingService) GetKeyFile() (string, error) {
	return s.getString("webKeyFile")
}

func (s *SettingService) GetExpireDiff() (int, error) {
	return s.getInt("expireDiff")
}

func (s *SettingService) GetTrafficDiff() (int, error) {
	return s.getInt("trafficDiff")
}

func (s *SettingService) GetSessionMaxAge() (int, error) {
	return s.getInt("sessionMaxAge")
}

func (s *SettingService) GetTrustedProxyCIDRs() (string, error) {
	return s.getString("trustedProxyCIDRs")
}

func (s *SettingService) GetRemarkTemplate() (string, error) {
	return s.getString("remarkTemplate")
}

func (s *SettingService) GetSecret() ([]byte, error) {
	secret, err := s.getString("secret")
	if secret == defaultValueMap["secret"] {
		err := s.saveSetting("secret", secret)
		if err != nil {
			logger.Warning("save secret failed:", err)
		}
	}
	return []byte(secret), err
}

// GetPanelGuid returns this panel's stable self-identifier, persisting a
// freshly generated UUID on first read. It is the globally stable node
// identity used to attribute online clients and inbounds to the physical
// node that hosts them across a chain of nodes (#4983), where per-panel
// autoincrement node ids are meaningless one hop away.
func (s *SettingService) GetPanelGuid() (string, error) {
	guid, err := s.getString("panelGuid")
	if err != nil {
		return "", err
	}
	if guid == defaultValueMap["panelGuid"] {
		if saveErr := s.saveSetting("panelGuid", guid); saveErr != nil {
			logger.Warning("save panelGuid failed:", saveErr)
		}
	}
	return guid, nil
}

func (s *SettingService) SetBasePath(basePath string) error {
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}
	return s.setString("webBasePath", basePath)
}

func (s *SettingService) GetBasePath() (string, error) {
	basePath, err := s.getString("webBasePath")
	if err != nil {
		return "", err
	}
	return normalizeBasePath(basePath), nil
}

func (s *SettingService) GetTimeLocation() (*time.Location, error) {
	l, err := s.getString("timeLocation")
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(l)
	if err != nil {
		defaultLocation := defaultValueMap["timeLocation"]
		logger.Errorf("location <%v> not exist, using default location: %v", l, defaultLocation)
		location, err = time.LoadLocation(defaultLocation)
		if err != nil {
			logger.Errorf("failed to load default location, using UTC: %v", err)
			return time.UTC, nil
		}
		return location, nil
	}
	return location, nil
}

func (s *SettingService) GetPageSize() (int, error) {
	return s.getInt("pageSize")
}

func (s *SettingService) GetDatepicker() (string, error) {
	return s.getString("datepicker")
}

func (s *SettingService) GetWarp() (string, error) {
	return s.getString("warp")
}

func (s *SettingService) SetWarp(data string) error {
	return s.setString("warp", data)
}

func (s *SettingService) GetNord() (string, error) {
	return s.getString("nord")
}

func (s *SettingService) SetNord(data string) error {
	return s.setString("nord", data)
}

func (s *SettingService) GetExternalTrafficInformEnable() (bool, error) {
	return s.getBool("externalTrafficInformEnable")
}

func (s *SettingService) SetExternalTrafficInformEnable(value bool) error {
	return s.setBool("externalTrafficInformEnable", value)
}

func (s *SettingService) GetExternalTrafficInformURI() (string, error) {
	return s.getString("externalTrafficInformURI")
}

func (s *SettingService) SetExternalTrafficInformURI(InformURI string) error {
	return s.setString("externalTrafficInformURI", InformURI)
}

func (s *SettingService) GetRestartXrayOnClientDisable() (bool, error) {
	return s.getBool("restartXrayOnClientDisable")
}

func (s *SettingService) SetRestartXrayOnClientDisable(value bool) error {
	return s.setBool("restartXrayOnClientDisable", value)
}

// GetDevChannelEnable reports whether the panel self-update tracks the rolling
// per-commit dev release instead of the latest stable tag.
func (s *SettingService) GetDevChannelEnable() (bool, error) {
	return s.getBool("devChannelEnable")
}

func (s *SettingService) SetDevChannelEnable(value bool) error {
	return s.setBool("devChannelEnable", value)
}

// GetIpLimitEnable reports whether the IP-limit feature is available. Always
// true since the panel enforces limits via the core's online-stats API; on an
// older core the job falls back to access-log parsing and warns there when the
// log is missing, so the UI no longer hides the field behind that condition.
func (s *SettingService) GetIpLimitEnable() (bool, error) {
	return true, nil
}

// GetAccessLogEnable reports whether an Xray access log is configured. Used by
// the UI for features that genuinely read the log file (the xray log viewer) —
// distinct from IP limiting, which works without it.
func (s *SettingService) GetAccessLogEnable() (bool, error) {
	accessLogPath, err := xray.GetAccessLogPath()
	if err != nil {
		return false, err
	}
	return (accessLogPath != "none" && accessLogPath != ""), nil
}

// GetLdapEnable returns whether LDAP is enabled.
func (s *SettingService) GetLdapEnable() (bool, error) {
	return s.getBool("ldapEnable")
}

func (s *SettingService) GetLdapHost() (string, error) {
	return s.getString("ldapHost")
}

func (s *SettingService) GetLdapPort() (int, error) {
	return s.getInt("ldapPort")
}

func (s *SettingService) GetLdapUseTLS() (bool, error) {
	return s.getBool("ldapUseTLS")
}

func (s *SettingService) GetLdapInsecureSkipVerify() (bool, error) {
	return s.getBool("ldapInsecureSkipVerify")
}

func (s *SettingService) GetLdapBindDN() (string, error) {
	return s.getString("ldapBindDN")
}

func (s *SettingService) GetLdapPassword() (string, error) {
	return s.getString("ldapPassword")
}

func (s *SettingService) GetLdapBaseDN() (string, error) {
	return s.getString("ldapBaseDN")
}

func (s *SettingService) GetLdapUserFilter() (string, error) {
	return s.getString("ldapUserFilter")
}

func (s *SettingService) GetLdapUserAttr() (string, error) {
	return s.getString("ldapUserAttr")
}

func (s *SettingService) GetLdapVlessField() (string, error) {
	return s.getString("ldapVlessField")
}

func (s *SettingService) GetLdapSyncCron() (string, error) {
	return s.getString("ldapSyncCron")
}

func (s *SettingService) GetLdapFlagField() (string, error) {
	return s.getString("ldapFlagField")
}

func (s *SettingService) GetLdapTruthyValues() (string, error) {
	return s.getString("ldapTruthyValues")
}

func (s *SettingService) GetLdapInvertFlag() (bool, error) {
	return s.getBool("ldapInvertFlag")
}

func (s *SettingService) GetLdapInboundTags() (string, error) {
	return s.getString("ldapInboundTags")
}

func (s *SettingService) GetLdapAutoCreate() (bool, error) {
	return s.getBool("ldapAutoCreate")
}

func (s *SettingService) GetLdapAutoDelete() (bool, error) {
	return s.getBool("ldapAutoDelete")
}

func (s *SettingService) GetLdapDefaultTotalGB() (int, error) {
	return s.getInt("ldapDefaultTotalGB")
}

func (s *SettingService) GetLdapDefaultExpiryDays() (int, error) {
	return s.getInt("ldapDefaultExpiryDays")
}

func (s *SettingService) GetLdapDefaultLimitIP() (int, error) {
	return s.getInt("ldapDefaultLimitIP")
}

// SecretClears marks redacted secrets the user explicitly emptied. Without a
// flag, a blank submitted secret means "unchanged" (the field is always served
// blank to the browser) and the stored value is preserved.
type SecretClears struct {
	LdapPassword bool
}

func (s *SettingService) UpdateAllSetting(allSetting *entity.AllSetting, clears SecretClears) error {
	if err := s.preserveRedactedSecrets(allSetting, clears); err != nil {
		return err
	}
	if err := validateSettingsURLs(allSetting); err != nil {
		return err
	}
	if err := allSetting.CheckValid(); err != nil {
		return err
	}

	v := reflect.ValueOf(allSetting).Elem()
	t := reflect.TypeFor[entity.AllSetting]()
	fields := reflect_util.GetFields(t)

	db := database.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		var existing []*model.Setting
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		byKey := make(map[string]*model.Setting, len(existing))
		for _, st := range existing {
			byKey[st.Key] = st
		}
		for _, field := range fields {
			key := field.Tag.Get("json")
			fieldV := v.FieldByName(field.Name)
			value := fmt.Sprint(fieldV.Interface())
			if st, ok := byKey[key]; ok {
				if st.Value == value {
					continue
				}
				st.Value = value
				if err := tx.Save(st).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func ValidateRegex(pattern string) error {
	if len(pattern) > maxRegexLength {
		return common.NewErrorf("Regular expression must not exceed %d characters", maxRegexLength)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return common.NewError("Regular expression is invalid:", err)
	}
	return nil
}

func (s *SettingService) preserveRedactedSecrets(allSetting *entity.AllSetting, clears SecretClears) error {
	if !clears.LdapPassword && strings.TrimSpace(allSetting.LdapPassword) == "" {
		value, err := s.GetLdapPassword()
		if err != nil {
			return err
		}
		allSetting.LdapPassword = value
	}
	if allSetting.TwoFactorEnable && strings.TrimSpace(allSetting.TwoFactorToken) == "" {
		value, err := s.GetTwoFactorToken()
		if err != nil {
			return err
		}
		allSetting.TwoFactorToken = value
	}
	return nil
}

func validateSettingsURLs(allSetting *entity.AllSetting) error {
	if allSetting.ExternalTrafficInformURI != "" {
		u, err := SanitizeHTTPURL(allSetting.ExternalTrafficInformURI)
		if err != nil {
			return common.NewError("external traffic inform URI is invalid:", err)
		}
		allSetting.ExternalTrafficInformURI = u
	}
	return nil
}

func (s *SettingService) UpdateSecret(key string, value string) error {
	switch key {
	case "ldapPassword", "twoFactorToken":
		return s.saveSetting(key, strings.TrimSpace(value))
	default:
		return common.NewError("secret key is not replaceable:", key)
	}
}

func (s *SettingService) GetDefaultXrayConfig() (any, error) {
	var jsonData any
	err := json.Unmarshal([]byte(xrayTemplateConfig), &jsonData)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

func extractHostname(host string) string {
	h, _, err := net.SplitHostPort(host)
	// Err is not nil means host does not contain port
	if err != nil {
		h = host
	}

	ip := net.ParseIP(h)
	// If it's not an IP, return as is
	if ip == nil {
		return h
	}

	// If it's an IPv4, return as is
	if ip.To4() != nil {
		return h
	}

	// IPv6 needs bracketing
	return "[" + h + "]"
}

func (s *SettingService) GetDefaultSettings(_ string) (any, error) {
	type settingFunc func() (any, error)
	settings := map[string]settingFunc{
		"expireDiff":       func() (any, error) { return s.GetExpireDiff() },
		"trafficDiff":      func() (any, error) { return s.GetTrafficDiff() },
		"pageSize":         func() (any, error) { return s.GetPageSize() },
		"defaultCert":      func() (any, error) { return s.GetCertFile() },
		"defaultKey":       func() (any, error) { return s.GetKeyFile() },
		"datepicker":       func() (any, error) { return s.GetDatepicker() },
		"ipLimitEnable":    func() (any, error) { return s.GetIpLimitEnable() },
		"accessLogEnable":  func() (any, error) { return s.GetAccessLogEnable() },
		"webDomain":        func() (any, error) { return s.GetWebDomain() },
		"devChannelEnable": func() (any, error) { return s.GetDevChannelEnable() },
		"isDevBuild":       func() (any, error) { return config.IsDevBuild(), nil },
	}

	result := make(map[string]any)

	for key, fn := range settings {
		value, err := fn()
		if err != nil {
			return "", err
		}
		result[key] = value
	}

	return result, nil
}
