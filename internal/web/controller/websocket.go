package controller

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"

	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"
)

var upgrader = ws.Upgrader{
	ReadBufferSize:    32768,
	WriteBufferSize:   32768,
	EnableCompression: true,
	CheckOrigin:       checkSameOrigin,
}

// checkSameOrigin allows requests with no Origin header (same-origin or non-browser
// clients) and otherwise checks the Origin's host, scheme and exposed port.
func checkSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}
	if u.Scheme != "https" && (u.Scheme != "http" || r.TLS != nil) {
		return false
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		// IPv6 literals without a port arrive as "[::1]"; net.SplitHostPort
		// fails in that case while url.Hostname() returns the address without
		// brackets. Strip them so same-origin checks pass for bare IPv6 hosts.
		host = r.Host
		if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
			host = host[1 : len(host)-1]
		}
	}
	if !strings.EqualFold(u.Hostname(), host) {
		return false
	}
	return port == "" || port == originPort(u)
}

func originPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// WebSocketController handles the HTTP→WebSocket upgrade for real-time updates.
// All per-connection lifecycle (pumps, hub registration) lives in
// panel.WebSocketService — this controller is HTTP-layer only.
type WebSocketController struct {
	BaseController
	service *panel.WebSocketService
}

// NewWebSocketController creates a controller wired to the given service.
func NewWebSocketController(svc *panel.WebSocketService) *WebSocketController {
	return &WebSocketController{service: svc}
}

// HandleWebSocket authenticates the request, upgrades the HTTP connection, and
// hands ownership of the connection off to the service.
func (w *WebSocketController) HandleWebSocket(c *gin.Context) {
	if !session.IsLogin(c) {
		logger.Warningf("Unauthorized WebSocket connection attempt from %s", getRemoteIp(c))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Error("Failed to upgrade WebSocket connection:", err)
		return
	}

	w.service.HandleConnection(conn, getRemoteIp(c))
}
