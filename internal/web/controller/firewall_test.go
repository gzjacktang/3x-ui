package controller

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFirewallRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	g := engine.Group("/panel/api/server")
	NewServerController(g)

	want := map[string]string{
		"/panel/api/server/firewall/status":  http.MethodGet,
		"/panel/api/server/firewall/install": http.MethodPost,
		"/panel/api/server/firewall/enable":  http.MethodPost,
		"/panel/api/server/firewall/disable": http.MethodPost,
		"/panel/api/server/firewall/allow":    http.MethodPost,
		"/panel/api/server/firewall/delete":   http.MethodPost,
		"/panel/api/server/firewall/takeover": http.MethodPost,
	}
	found := map[string]string{}
	for _, r := range engine.Routes() {
		if _, ok := want[r.Path]; ok {
			found[r.Path] = r.Method
		}
	}
	for path, method := range want {
		if found[path] != method {
			t.Fatalf("route %s %s not registered (got %q)", method, path, found[path])
		}
	}
}
