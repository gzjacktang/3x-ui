package xray

import (
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

/* The freshness window is shorter than the five-second traffic interval. */
const onlineStatsMaxAge = 4 * time.Second

type onlineStatsSnapshot struct {
	mu         sync.Mutex
	users      []OnlineUser
	validUntil time.Time
}

/* GetOnlineUsers returns a shared read-only snapshot of the core's online stats. */
func (p *Process) GetOnlineUsers() ([]OnlineUser, bool, error) {
	snapshot := &p.onlineStats
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if !p.IsRunning() || p.OnlineAPISupport() == OnlineAPIUnsupported {
		return nil, false, nil
	}
	now := time.Now()
	if now.Before(snapshot.validUntil) {
		return snapshot.users, true, nil
	}
	snapshot.users = nil
	snapshot.validUntil = time.Time{}

	var api XrayAPI
	if err := api.Init(p.GetAPIPort()); err != nil {
		return nil, false, err
	}
	defer api.Close()
	users, err := api.GetOnlineUsers()
	if IsUnimplementedErr(err) {
		p.SetOnlineAPISupport(OnlineAPIUnsupported)
		logger.Info("Xray core does not support the online-stats API; using traffic-delta onlines")
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !p.IsRunning() {
		return nil, false, nil
	}
	if p.OnlineAPISupport() == OnlineAPIUnknown {
		p.SetOnlineAPISupport(OnlineAPISupported)
		logger.Info("Xray core supports the online-stats API; using connection-based onlines and access-log-free IP limit")
	}
	snapshot.users = users
	snapshot.validUntil = now.Add(onlineStatsMaxAge)
	return users, true, nil
}
