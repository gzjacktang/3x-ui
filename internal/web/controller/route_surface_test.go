package controller

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLiteAPIRouteSurface(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewAPIController(engine.Group(""))

	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Path] = struct{}{}
	}

	retained := []string{
		"/panel/api/inbounds/list",
		"/panel/api/clients/list/paged",
		"/panel/api/xray/",
		"/panel/api/xray/routeTest",
		"/panel/api/setting/all",
	}
	for _, path := range retained {
		if _, ok := routes[path]; !ok {
			t.Errorf("retained route %q is not registered", path)
		}
	}

	removed := []string{
		"/panel/api/nodes",
		"/panel/api/hosts",
		"/panel/api/clients/groups",
		"/panel/api/clients/subLinks",
		"/panel/api/server/status",
		"/panel/api/server/history",
		"/panel/api/server/xrayMetrics",
		"/panel/api/server/descendants",
		"/panel/api/setting/testSmtp",
		"/panel/api/setting/testTgBot",
		"/panel/api/xray/outbound-subs",
		"/panel/api/openapi.json",
	}
	for route := range routes {
		for _, prefix := range removed {
			if route == prefix || len(route) > len(prefix) && route[:len(prefix)] == prefix && route[len(prefix)] == '/' {
				t.Errorf("removed route %q is still registered", route)
			}
		}
	}
}
