package controller

import (
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/firewall"

	"github.com/gin-gonic/gin"
)

func (a *ServerController) firewallService() *firewall.Service {
	return firewall.NewService(a.settingService.GetPort)
}

func (a *ServerController) getFirewallStatus(c *gin.Context) {
	st, err := a.firewallService().Status()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.firewall.statusFailed"), err)
		return
	}
	jsonObj(c, st, nil)
}

func (a *ServerController) installFirewall(c *gin.Context) {
	err := a.firewallService().Install()
	jsonMsg(c, I18nWeb(c, "pages.settings.firewall.installSuccess"), err)
}

func (a *ServerController) enableFirewall(c *gin.Context) {
	err := a.firewallService().Enable()
	jsonMsg(c, I18nWeb(c, "pages.settings.firewall.enableSuccess"), err)
}

func (a *ServerController) disableFirewall(c *gin.Context) {
	err := a.firewallService().Disable()
	jsonMsg(c, I18nWeb(c, "pages.settings.firewall.disableSuccess"), err)
}

type firewallAllowRequest struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Proto string `json:"proto"`
}

func (a *ServerController) allowFirewallPort(c *gin.Context) {
	var req firewallAllowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.firewall.invalidRequest"), err)
		return
	}
	err := a.firewallService().Allow(firewall.PortSpec{
		Start: req.Start,
		End:   req.End,
		Proto: req.Proto,
	})
	jsonMsg(c, I18nWeb(c, "pages.settings.firewall.allowSuccess"), err)
}

type firewallDeleteRequest struct {
	Number int    `json:"number"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Proto  string `json:"proto"`
}

func (a *ServerController) deleteFirewallRule(c *gin.Context) {
	var req firewallDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.firewall.invalidRequest"), err)
		return
	}
	svc := a.firewallService()
	var err error
	if req.Number > 0 {
		err = svc.DeleteByNumber(req.Number)
	} else {
		err = svc.DeleteAllow(firewall.PortSpec{
			Start: req.Start,
			End:   req.End,
			Proto: req.Proto,
		})
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.firewall.deleteSuccess"), err)
}

type firewallTakeoverRequest struct {
	Confirm bool `json:"confirm"`
}

func (a *ServerController) takeoverFirewall(c *gin.Context) {
	var req firewallTakeoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.firewall.invalidRequest"), err)
		return
	}
	result, err := a.firewallService().Takeover(req.Confirm)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.firewall.takeoverFailed"), err)
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.settings.firewall.takeoverSuccess"), result, nil)
}
