package xray

import (
	"context"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	statsService "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testOnlineStatsServer struct {
	statsService.UnimplementedStatsServiceServer
	calls atomic.Int32
}

func (s *testOnlineStatsServer) GetUsersStats(context.Context, *statsService.GetUsersStatsRequest) (*statsService.GetUsersStatsResponse, error) {
	switch s.calls.Add(1) {
	case 1:
		return &statsService.GetUsersStatsResponse{Users: []*statsService.UserStat{{Email: "alice"}}}, nil
	case 2:
		return nil, status.Error(codes.Unavailable, "test connection lost")
	default:
		return &statsService.GetUsersStatsResponse{Users: []*statsService.UserStat{{Email: "bob"}}}, nil
	}
}

func TestProcessOnlineStatsShareOnlyFreshSuccessfulSamples(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	stats := new(testOnlineStatsServer)
	statsService.RegisterStatsServiceServer(server, stats)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	core := &Process{newProcess(&Config{})}
	core.cmd = &exec.Cmd{Process: &os.Process{}}
	core.done = make(chan struct{})
	core.apiPort = listener.Addr().(*net.TCPAddr).Port
	assertUser := func(want string) {
		t.Helper()
		users, supported, err := core.GetOnlineUsers()
		if err != nil || !supported || len(users) != 1 || users[0].Email != want {
			t.Fatalf("online users = %+v, supported=%v, err=%v; want %q", users, supported, err, want)
		}
	}
	assertUser("alice")
	assertUser("alice")
	if calls := stats.calls.Load(); calls != 1 {
		t.Fatalf("fresh snapshot made %d stats calls, want 1", calls)
	}

	core.onlineStats.validUntil = time.Now().Add(-time.Second)
	if users, supported, err := core.GetOnlineUsers(); len(users) != 0 || supported || status.Code(err) != codes.Unavailable {
		t.Fatalf("failed refresh returned users=%+v supported=%v err=%v; want unavailable without stale users", users, supported, err)
	}
	assertUser("bob")
	close(core.done)
	if users, supported, err := core.GetOnlineUsers(); len(users) != 0 || supported || err != nil {
		t.Fatalf("stopped core returned users=%+v supported=%v err=%v", users, supported, err)
	}
}
