package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	proxymanCommand "github.com/xtls/xray-core/app/proxyman/command"
	statsService "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type blockingOnlineStatsServer struct {
	statsService.UnimplementedStatsServiceServer
	entered chan struct{}
	release chan struct{}
}

func (s *blockingOnlineStatsServer) GetUsersStats(context.Context, *statsService.GetUsersStatsRequest) (*statsService.GetUsersStatsResponse, error) {
	close(s.entered)
	<-s.release
	return &statsService.GetUsersStatsResponse{
		Users: []*statsService.UserStat{{Email: "alice"}},
	}, nil
}

func TestXrayServiceGetOnlineUsersDiscardsStaleProcessGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake core is a shell script")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	statsSrv := &blockingOnlineStatsServer{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	statsService.RegisterStatsServiceServer(grpcServer, statsSrv)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	setupConflictDB(t)
	binFolder := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", binFolder)
	t.Setenv("XUI_LOG_FOLDER", t.TempDir())
	binary := filepath.Join(binFolder, xray.GetBinaryName())
	if err := os.WriteFile(binary, []byte(fakeXrayCore("26.10.31", true)), 0o755); err != nil {
		t.Fatal(err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	cfg := &xray.Config{
		InboundConfigs: []xray.InboundConfig{{Tag: "api", Port: port}},
	}
	core := xray.NewProcess(cfg)
	if err := core.Start(); err != nil {
		t.Fatalf("start initial fake core: %v", err)
	}
	replacement := xray.NewProcess(cfg)
	if err := replacement.Start(); err != nil {
		_ = core.Stop()
		t.Fatalf("start replacement fake core: %v", err)
	}
	previous := XrayProcess()
	installTestXrayProcess(core)
	t.Cleanup(func() {
		_ = core.Stop()
		_ = replacement.Stop()
		installTestXrayProcess(previous)
	})

	type outcome struct {
		users     []xray.OnlineUser
		supported bool
		err       error
	}
	done := make(chan outcome, 1)
	go func() {
		users, supported, callErr := (&XrayService{}).GetOnlineUsersFresh()
		done <- outcome{users: users, supported: supported, err: callErr}
	}()

	<-statsSrv.entered
	installTestXrayProcess(replacement)
	close(statsSrv.release)

	got := <-done
	if got.err != nil || got.supported || len(got.users) != 0 {
		t.Fatalf("stale generation returned users=%+v supported=%v err=%v; want discarded sample", got.users, got.supported, got.err)
	}
}

type recordingHandlerServer struct {
	proxymanCommand.UnimplementedHandlerServiceServer
	alterCalls atomic.Int32
}

func (s *recordingHandlerServer) AlterInbound(context.Context, *proxymanCommand.AlterInboundRequest) (*proxymanCommand.AlterInboundResponse, error) {
	s.alterCalls.Add(1)
	return &proxymanCommand.AlterInboundResponse{}, nil
}

type staticOnlineStatsServer struct {
	statsService.UnimplementedStatsServiceServer
}

func (s *staticOnlineStatsServer) GetUsersStats(context.Context, *statsService.GetUsersStatsRequest) (*statsService.GetUsersStatsResponse, error) {
	return &statsService.GetUsersStatsResponse{
		Users: []*statsService.UserStat{{Email: "alice"}},
	}, nil
}

func TestXrayServiceUserMutationsRejectStaleProcessGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake core is a shell script")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	handlerSrv := &recordingHandlerServer{}
	proxymanCommand.RegisterHandlerServiceServer(grpcServer, handlerSrv)
	statsService.RegisterStatsServiceServer(grpcServer, &staticOnlineStatsServer{})
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	setupConflictDB(t)
	binFolder := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", binFolder)
	t.Setenv("XUI_LOG_FOLDER", t.TempDir())
	binary := filepath.Join(binFolder, xray.GetBinaryName())
	if err := os.WriteFile(binary, []byte(fakeXrayCore("26.10.31", true)), 0o755); err != nil {
		t.Fatal(err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	cfg := &xray.Config{
		InboundConfigs: []xray.InboundConfig{{Tag: "api", Port: port}},
	}
	initial := xray.NewProcess(cfg)
	if err := initial.Start(); err != nil {
		t.Fatalf("start initial fake core: %v", err)
	}
	replacement := xray.NewProcess(cfg)
	if err := replacement.Start(); err != nil {
		_ = initial.Stop()
		t.Fatalf("start replacement fake core: %v", err)
	}
	previous := XrayProcess()
	installTestXrayProcess(initial)
	t.Cleanup(func() {
		_ = initial.Stop()
		_ = replacement.Stop()
		installTestXrayProcess(previous)
	})

	svc := &XrayService{}
	sample, ok, err := svc.GetOnlineUsersSample(true)
	if err != nil || !ok || sample.Process != initial || len(sample.Users) != 1 || sample.Users[0].Email != "alice" {
		t.Fatalf("GetOnlineUsersSample = (%+v, %v, %v), want initial process with alice", sample, ok, err)
	}
	if !svc.IsCurrentProcess(initial) {
		t.Fatal("IsCurrentProcess(initial) = false, want true")
	}

	clientCfg := map[string]any{
		"email": "alice",
		"id":    "11111111-1111-1111-1111-111111111111",
	}
	if removed, err := svc.RemoveUserForProcess(sample.Process, "vless-in", "alice"); err != nil || !removed {
		t.Fatalf("RemoveUserForProcess(initial) = (%v, %v), want (true, nil)", removed, err)
	}
	if added, err := svc.AddUserForProcess(sample.Process, "vless", "vless-in", clientCfg); err != nil || !added {
		t.Fatalf("AddUserForProcess(initial) = (%v, %v), want (true, nil)", added, err)
	}
	if got := handlerSrv.alterCalls.Load(); got != 2 {
		t.Fatalf("alterCalls before restart = %d, want 2", got)
	}

	installTestXrayProcess(replacement)
	if svc.IsCurrentProcess(sample.Process) {
		t.Fatal("IsCurrentProcess(initial) = true after replacement, want false")
	}
	if removed, err := svc.RemoveUserForProcess(sample.Process, "vless-in", "alice"); err != nil || removed {
		t.Fatalf("RemoveUserForProcess(stale) = (%v, %v), want (false, nil)", removed, err)
	}
	if added, err := svc.AddUserForProcess(sample.Process, "vless", "vless-in", clientCfg); err != nil || added {
		t.Fatalf("AddUserForProcess(stale) = (%v, %v), want (false, nil)", added, err)
	}
	if got := handlerSrv.alterCalls.Load(); got != 2 {
		t.Fatalf("stale process mutated replacement core: alterCalls = %d, want 2", got)
	}
}
