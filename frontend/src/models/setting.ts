import { ObjectUtil } from '@/utils';

export class AllSetting {
  webListen = '';
  webDomain = '';
  webPort = 2053;
  webCertFile = '';
  webKeyFile = '';
  webBasePath = '/';
  sessionMaxAge = 360;
  trustedProxyCIDRs = '127.0.0.1/32,::1/128';
  panelOutbound = '';
  pageSize = 25;
  expireDiff = 0;
  trafficDiff = 0;
  datepicker: 'gregorian' | 'jalalian' = 'gregorian';
  twoFactorEnable = false;
  twoFactorToken = '';
  xrayTemplateConfig = '';
  externalTrafficInformEnable = false;
  externalTrafficInformURI = '';
  restartXrayOnClientDisable = true;

  timeLocation = 'Local';

  ldapEnable = false;
  ldapHost = '';
  ldapPort = 389;
  ldapUseTLS = false;
  ldapInsecureSkipVerify = false;
  ldapBindDN = '';
  ldapPassword = '';
  ldapBaseDN = '';
  ldapUserFilter = '(objectClass=person)';
  ldapUserAttr = 'mail';
  ldapVlessField = 'vless_enabled';
  ldapSyncCron = '@every 1m';
  ldapFlagField = '';
  ldapTruthyValues = 'true,1,yes,on';
  ldapInvertFlag = false;
  ldapInboundTags = '';
  ldapAutoCreate = false;
  ldapAutoDelete = false;
  ldapDefaultTotalGB = 0;
  ldapDefaultExpiryDays = 0;
  ldapDefaultLimitIP = 0;
  hasTwoFactorToken = false;
  hasLdapPassword = false;
  hasApiToken = false;
  hasWarpSecret = false;
  hasNordSecret = false;
  clearLdapPassword = false;

  constructor(data?: unknown) {
    if (data != null) {
      ObjectUtil.cloneProps(this, data);
    }
  }

  equals(other: AllSetting): boolean {
    return ObjectUtil.equals(this, other);
  }
}
