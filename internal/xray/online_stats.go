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

/* GetOnlineUsers returns a shared snapshot of the core's online stats for display callers. */
func (p *Process) GetOnlineUsers() ([]OnlineUser, bool, error) {
	return p.getOnlineUsers(true)
}

/* GetOnlineUsersFresh queries the core directly and refreshes the shared snapshot. */
func (p *Process) GetOnlineUsersFresh() ([]OnlineUser, bool, error) {
	return p.getOnlineUsers(false)
}

func (p *Process) getOnlineUsers(allowCache bool) ([]OnlineUser, bool, error) {
	snapshot := &p.onlineStats
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if !p.IsRunning() || p.OnlineAPISupport() == OnlineAPIUnsupported {
		return nil, false, nil
	}
	now := time.Now()
	if allowCache && now.Before(snapshot.validUntil) {
		return cloneOnlineUsers(snapshot.users), true, nil
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
	return cloneOnlineUsers(users), true, nil
}

func cloneOnlineUsers(users []OnlineUser) []OnlineUser {
	if len(users) == 0 {
		return nil
	}
	cloned := make([]OnlineUser, len(users))
	for i, u := range users {
		var ips []OnlineIP
		if len(u.IPs) > 0 {
			ips = make([]OnlineIP, len(u.IPs))
			copy(ips, u.IPs)
		}
		cloned[i] = OnlineUser{
			Email: u.Email,
			IPs:   ips,
		}
	}
	return cloned
}
