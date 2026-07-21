package entity

import (
	"crypto/tls"
	"math"
	"net"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

type Msg struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     any    `json:"obj"`
}

type AllSetting struct {
	WebListen         string `json:"webListen" form:"webListen"`
	WebDomain         string `json:"webDomain" form:"webDomain"`
	WebPort           int    `json:"webPort" form:"webPort" validate:"gte=1,lte=65535"`
	WebCertFile       string `json:"webCertFile" form:"webCertFile"`
	WebKeyFile        string `json:"webKeyFile" form:"webKeyFile"`
	WebBasePath       string `json:"webBasePath" form:"webBasePath"`
	SessionMaxAge     int    `json:"sessionMaxAge" form:"sessionMaxAge" validate:"gte=1,lte=525600"`
	TrustedProxyCIDRs string `json:"trustedProxyCIDRs" form:"trustedProxyCIDRs"`
	PanelOutbound     string `json:"panelOutbound" form:"panelOutbound"`

	PageSize       int    `json:"pageSize" form:"pageSize" validate:"gte=0,lte=1000"`
	ExpireDiff     int    `json:"expireDiff" form:"expireDiff" validate:"gte=0"`
	TrafficDiff    int    `json:"trafficDiff" form:"trafficDiff" validate:"gte=0,lte=100"`
	RemarkTemplate string `json:"remarkTemplate" form:"remarkTemplate"`
	Datepicker     string `json:"datepicker" form:"datepicker"`

	TimeLocation    string `json:"timeLocation" form:"timeLocation"`
	TwoFactorEnable bool   `json:"twoFactorEnable" form:"twoFactorEnable"`
	TwoFactorToken  string `json:"twoFactorToken" form:"twoFactorToken"`

	ExternalTrafficInformEnable bool   `json:"externalTrafficInformEnable" form:"externalTrafficInformEnable"`
	ExternalTrafficInformURI    string `json:"externalTrafficInformURI" form:"externalTrafficInformURI"`
	RestartXrayOnClientDisable  bool   `json:"restartXrayOnClientDisable" form:"restartXrayOnClientDisable"`

	LdapEnable             bool   `json:"ldapEnable" form:"ldapEnable"`
	LdapHost               string `json:"ldapHost" form:"ldapHost"`
	LdapPort               int    `json:"ldapPort" form:"ldapPort" validate:"gte=0,lte=65535"`
	LdapUseTLS             bool   `json:"ldapUseTLS" form:"ldapUseTLS"`
	LdapInsecureSkipVerify bool   `json:"ldapInsecureSkipVerify" form:"ldapInsecureSkipVerify"`
	LdapBindDN             string `json:"ldapBindDN" form:"ldapBindDN"`
	LdapPassword           string `json:"ldapPassword" form:"ldapPassword"`
	LdapBaseDN             string `json:"ldapBaseDN" form:"ldapBaseDN"`
	LdapUserFilter         string `json:"ldapUserFilter" form:"ldapUserFilter"`
	LdapUserAttr           string `json:"ldapUserAttr" form:"ldapUserAttr"`
	LdapVlessField         string `json:"ldapVlessField" form:"ldapVlessField"`
	LdapSyncCron           string `json:"ldapSyncCron" form:"ldapSyncCron"`
	LdapFlagField          string `json:"ldapFlagField" form:"ldapFlagField"`
	LdapTruthyValues       string `json:"ldapTruthyValues" form:"ldapTruthyValues"`
	LdapInvertFlag         bool   `json:"ldapInvertFlag" form:"ldapInvertFlag"`
	LdapInboundTags        string `json:"ldapInboundTags" form:"ldapInboundTags"`
	LdapAutoCreate         bool   `json:"ldapAutoCreate" form:"ldapAutoCreate"`
	LdapAutoDelete         bool   `json:"ldapAutoDelete" form:"ldapAutoDelete"`
	LdapDefaultTotalGB     int    `json:"ldapDefaultTotalGB" form:"ldapDefaultTotalGB" validate:"gte=0"`
	LdapDefaultExpiryDays  int    `json:"ldapDefaultExpiryDays" form:"ldapDefaultExpiryDays" validate:"gte=0"`
	LdapDefaultLimitIP     int    `json:"ldapDefaultLimitIP" form:"ldapDefaultLimitIP" validate:"gte=0"`

	WarpUpdateInterval int `json:"warpUpdateInterval" form:"warpUpdateInterval" validate:"gte=0"`
}

type AllSettingView struct {
	AllSetting

	HasTwoFactorToken bool `json:"hasTwoFactorToken"`
	HasLdapPassword   bool `json:"hasLdapPassword"`
	HasApiToken       bool `json:"hasApiToken"`
	HasWarpSecret     bool `json:"hasWarpSecret"`
	HasNordSecret     bool `json:"hasNordSecret"`
}

func pathHasForbiddenChar(s string) bool {
	for _, r := range s {
		if r == '\\' || r == ' ' || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func (s *AllSetting) CheckValid() error {
	if s.WebListen != "" {
		ip := net.ParseIP(s.WebListen)
		if ip == nil {
			return common.NewError("web listen is not valid ip:", s.WebListen)
		}
	}

	if s.WebPort <= 0 || s.WebPort > math.MaxUint16 {
		return common.NewError("web port is not a valid port:", s.WebPort)
	}

	if s.WebCertFile != "" || s.WebKeyFile != "" {
		_, err := tls.LoadX509KeyPair(s.WebCertFile, s.WebKeyFile)
		if err != nil {
			return common.NewErrorf("cert file <%v> or key file <%v> invalid: %v", s.WebCertFile, s.WebKeyFile, err)
		}
	}

	for _, p := range []struct {
		name  string
		value string
	}{
		{"web base path", s.WebBasePath},
	} {
		if pathHasForbiddenChar(p.value) {
			return common.NewError("URI path contains an invalid character:", p.name)
		}
	}

	if !strings.HasPrefix(s.WebBasePath, "/") {
		s.WebBasePath = "/" + s.WebBasePath
	}
	if !strings.HasSuffix(s.WebBasePath, "/") {
		s.WebBasePath += "/"
	}
	for cidr := range strings.SplitSeq(s.TrustedProxyCIDRs, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if ip := net.ParseIP(cidr); ip != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return common.NewError("trusted proxy CIDR is not valid:", cidr)
		}
	}

	_, err := time.LoadLocation(s.TimeLocation)
	if err != nil {
		return common.NewError("time location not exist:", s.TimeLocation)
	}

	return nil
}
