package job

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestMergeClientIps_EvictsStaleOldEntries(t *testing.T) {
	// #4077: after a ban expires, a single IP that reconnects used to get
	// banned again immediately because a long-disconnected IP stayed in the
	// DB with an ancient timestamp and kept "protecting" itself against
	// eviction. Guard against that regression here.
	old := []IPWithTimestamp{
		{IP: "1.1.1.1", Timestamp: 100},  // stale — client disconnected long ago
		{IP: "2.2.2.2", Timestamp: 1900}, // fresh — still connecting
	}
	new := []IPWithTimestamp{
		{IP: "2.2.2.2", Timestamp: 2000}, // same IP, newer log line
	}

	got := mergeClientIps(old, new, 1000, false)

	want := map[string]int64{"2.2.2.2": 2000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale 1.1.1.1 should have been dropped\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_KeepsFreshOldEntriesUnchanged(t *testing.T) {
	// Backwards-compat: entries that aren't stale are still carried forward,
	// so enforcement survives access-log rotation.
	old := []IPWithTimestamp{
		{IP: "1.1.1.1", Timestamp: 1500},
	}
	got := mergeClientIps(old, nil, 1000, false)

	want := map[string]int64{"1.1.1.1": 1500}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh old IP should have been retained\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_PrefersLaterTimestampForSameIp(t *testing.T) {
	old := []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: 1500}}
	new := []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: 1700}}

	got := mergeClientIps(old, new, 1000, false)

	if got["1.1.1.1"] != 1700 {
		t.Fatalf("expected latest timestamp 1700, got %d", got["1.1.1.1"])
	}
}

func TestMergeClientIps_DropsStaleNewEntries(t *testing.T) {
	// A log line with a clock-skewed old timestamp must not resurrect a
	// stale IP past the cutoff.
	new := []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: 500}}
	got := mergeClientIps(nil, new, 1000, false)

	if len(got) != 0 {
		t.Fatalf("stale new IP should have been dropped, got %v", got)
	}
}

func TestMergeClientIps_NoStaleCutoffStillWorks(t *testing.T) {
	// Defensive: a zero cutoff (e.g. during very first run on a fresh
	// install) must not over-evict.
	old := []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: 100}}
	new := []IPWithTimestamp{{IP: "2.2.2.2", Timestamp: 200}}

	got := mergeClientIps(old, new, 0, false)

	want := map[string]int64{"1.1.1.1": 100, "2.2.2.2": 200}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zero cutoff should keep everything\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_LiveObservationsBypassStaleCutoff(t *testing.T) {
	// online-API mode: lastSeen is set when the connection was dispatched, so
	// a connection held open for hours has an "old" timestamp while being live
	// by definition. It must survive the stale cutoff.
	new := []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: 500}} // opened long ago, still connected
	got := mergeClientIps(nil, new, 1000, true)

	want := map[string]int64{"1.1.1.1": 500}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("live observation must bypass the stale cutoff\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_LiveModeStillEvictsStaleOldEntries(t *testing.T) {
	// the bypass applies only to this scan's observations — persisted entries
	// from past scans still age out as before.
	old := []IPWithTimestamp{{IP: "2.2.2.2", Timestamp: 100}}
	new := []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: 2000}}
	got := mergeClientIps(old, new, 1000, true)

	want := map[string]int64{"1.1.1.1": 2000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale db entry must still be evicted in live mode\ngot:  %v\nwant: %v", got, want)
	}
}

func TestSelectIpsToBan(t *testing.T) {
	live := []IPWithTimestamp{ // sorted oldest-first, as partitionLiveIps returns
		{IP: "A", Timestamp: 100},
		{IP: "B", Timestamp: 200},
		{IP: "C", Timestamp: 300},
	}

	// over the limit: oldest connections are banned, newest keep the slots
	kept, banned := selectIpsToBan(live, 1)
	if got := collectIps(kept); !reflect.DeepEqual(got, []string{"C"}) {
		t.Fatalf("newest ip must keep the slot, got %v", got)
	}
	if got := collectIps(banned); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("older ips must be banned oldest-first, got %v", got)
	}

	// at the limit: nothing banned
	kept, banned = selectIpsToBan(live, 3)
	if len(banned) != 0 || len(kept) != 3 {
		t.Fatalf("at-limit set must not ban, kept=%v banned=%v", kept, banned)
	}

	// under the limit: nothing banned
	kept, banned = selectIpsToBan(live[:1], 3)
	if len(banned) != 0 || len(kept) != 1 {
		t.Fatalf("under-limit set must not ban, kept=%v banned=%v", kept, banned)
	}

	// defensive: non-positive limit never reaches enforcement, but must not panic
	if _, banned := selectIpsToBan(live, 0); banned != nil {
		t.Fatalf("zero limit must not ban, got %v", banned)
	}
}

func collectIps(entries []IPWithTimestamp) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.IP)
	}
	return out
}

func TestPartitionLiveIps_SingleLiveNotStarvedByStillFreshHistoricals(t *testing.T) {
	// #4091: db holds A, B, C from minutes ago (still in the 30min
	// window) but they're not connecting anymore. only D is. old code
	// merged all four, sorted ascending, kept [A,B,C] and banned D
	// every tick. pin the new rule: only live ips count toward the limit.
	ipMap := map[string]int64{
		"A": 1000,
		"B": 1100,
		"C": 1200,
		"D": 2000,
	}
	observed := map[string]bool{"D": true}

	live, historical := partitionLiveIps(ipMap, observed)

	if got := collectIps(live); !reflect.DeepEqual(got, []string{"D"}) {
		t.Fatalf("live set should only contain the ip observed this scan\ngot:  %v\nwant: [D]", got)
	}
	if got := collectIps(historical); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("historical set should contain db-only ips in ascending order\ngot:  %v\nwant: [A B C]", got)
	}
}

func TestPartitionLiveIps_ConcurrentLiveIpsSortedAscending(t *testing.T) {
	// when several ips are really live, partition returns them all in the
	// live set sorted ascending by timestamp. updateInboundClientIps then
	// keeps the newest and bans the oldest (last-IP-wins, #4699).
	ipMap := map[string]int64{
		"A": 5000,
		"B": 5500,
	}
	observed := map[string]bool{"A": true, "B": true}

	live, historical := partitionLiveIps(ipMap, observed)

	if got := collectIps(live); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("both live ips should be in the live set, ascending\ngot:  %v\nwant: [A B]", got)
	}
	if len(historical) != 0 {
		t.Fatalf("no historical ips expected, got %v", historical)
	}
}

func TestGetInboundByEmailFallbackIgnoresProtocolScalarFields(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	inbound := &model.Inbound{
		UserId:   1,
		Tag:      "vless-limit-fallback",
		Enable:   true,
		Port:     43002,
		Protocol: model.VLESS,
		Settings: `{
			"clients": [{"email": "alice@example.test", "id": "11111111-1111-1111-1111-111111111111", "limitIp": 2}],
			"decryption": "none",
			"encryption": "none",
			"fallbacks": []
		}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	got, err := (&CheckClientIpJob{}).getInboundByEmail("alice@example.test")
	if err != nil {
		t.Fatalf("getInboundByEmail: %v", err)
	}
	if got.Id != inbound.Id {
		t.Fatalf("inbound id = %d, want %d", got.Id, inbound.Id)
	}
}

func TestPartitionLiveIps_EmptyScanLeavesDbIntact(t *testing.T) {
	// quiet tick: nothing observed => nothing live. everything merged
	// is historical. keeps the panel from wiping recent-but-idle ips.
	ipMap := map[string]int64{
		"A": 1000,
		"B": 1100,
	}
	observed := map[string]bool{}

	live, historical := partitionLiveIps(ipMap, observed)

	if len(live) != 0 {
		t.Fatalf("no live ips expected, got %v", live)
	}
	if got := collectIps(historical); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("all merged entries should flow to historical\ngot:  %v\nwant: [A B]", got)
	}
}

func TestPartitionLiveIps_RecentSyncedIpIsLive(t *testing.T) {
	// Synced IPs from other nodes within 2 minutes should be counted as live
	// even if they weren't observed in the local scan.
	now := time.Now().Unix()
	ipMap := map[string]int64{
		"A": now - 30,  // synced 30s ago -> live
		"B": now - 150, // synced 2m30s ago -> historical
	}
	observed := map[string]bool{}

	live, historical := partitionLiveIps(ipMap, observed)

	if got := collectIps(live); !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("recent IP should be live\ngot:  %v\nwant: [A]", got)
	}
	if got := collectIps(historical); !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("older IP should be historical\ngot:  %v\nwant: [B]", got)
	}
}

func TestCheckFail2BanInstalled_DisabledEnvSkipsClientProbe(t *testing.T) {
	t.Setenv("XUI_ENABLE_FAIL2BAN", "false")
	marker := fakeFail2BanClient(t)

	if (&CheckClientIpJob{}).checkFail2BanInstalled() {
		t.Fatal("fail2ban should be unavailable when XUI_ENABLE_FAIL2BAN=false")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("fail2ban-client should not have been executed, stat error: %v", err)
	}
}

func TestCheckFail2BanInstalled_EmptyEnvSkipsClientProbe(t *testing.T) {
	t.Setenv("XUI_ENABLE_FAIL2BAN", "")
	marker := fakeFail2BanClient(t)

	if (&CheckClientIpJob{}).checkFail2BanInstalled() {
		t.Fatal("fail2ban should be unavailable when XUI_ENABLE_FAIL2BAN is empty")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("fail2ban-client should not have been executed, stat error: %v", err)
	}
}

func TestIsFail2BanEnabled_DefaultsToEnabledWhenUnset(t *testing.T) {
	value, ok := os.LookupEnv("XUI_ENABLE_FAIL2BAN")
	os.Unsetenv("XUI_ENABLE_FAIL2BAN")
	t.Cleanup(func() {
		if ok {
			os.Setenv("XUI_ENABLE_FAIL2BAN", value)
		} else {
			os.Unsetenv("XUI_ENABLE_FAIL2BAN")
		}
	})

	if !isFail2BanEnabled() {
		t.Fatal("fail2ban should default to enabled when XUI_ENABLE_FAIL2BAN is unset")
	}
}

func TestCheckFail2BanInstalled_EnabledEnvProbesClient(t *testing.T) {
	t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
	marker := fakeFail2BanClient(t)

	if !(&CheckClientIpJob{}).checkFail2BanInstalled() {
		t.Fatal("fail2ban should be available when the client probe succeeds")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fail2ban-client should have been executed: %v", err)
	}
}

func fakeFail2BanClient(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	marker := filepath.Join(dir, "probe-called")
	fakeClient := filepath.Join(dir, "fail2ban-client")
	script := "#!/bin/sh\n: > \"$FAIL2BAN_PROBE_MARKER\"\nexit 0\n"
	if runtime.GOOS == "windows" {
		fakeClient += ".bat"
		script = "@echo off\ntype nul > \"%FAIL2BAN_PROBE_MARKER%\"\nexit /b 0\n"
	}
	if err := os.WriteFile(fakeClient, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake fail2ban-client: %v", err)
	}

	t.Setenv("FAIL2BAN_PROBE_MARKER", marker)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

type stubOnlineUsersProvider struct {
	mu          sync.Mutex
	running     bool
	cached      []xray.OnlineUser
	fresh       []xray.OnlineUser
	sampledProc *xray.Process
	currentProc *xray.Process
	cachedHits  int
	freshHits   int
	removeCalls []*xray.Process
	addCalls    []*xray.Process
	onSample    func()
	onBeforeDo  func()
	onAfterDo   func(ran bool)
}

func (s *stubOnlineUsersProvider) setCurrentProcess(core *xray.Process) {
	s.mu.Lock()
	s.currentProc = core
	s.mu.Unlock()
}

func (s *stubOnlineUsersProvider) IsXrayRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *stubOnlineUsersProvider) GetOnlineUsersSample(fresh bool) (service.OnlineUsersSample, bool, error) {
	s.mu.Lock()
	proc := s.sampledProc
	if proc == nil {
		proc = s.currentProc
	}
	var users []xray.OnlineUser
	if fresh {
		s.freshHits++
		users = s.fresh
	} else {
		s.cachedHits++
		users = s.cached
	}
	onSample := s.onSample
	s.mu.Unlock()
	if onSample != nil {
		onSample()
	}
	return service.OnlineUsersSample{Users: users, Process: proc}, true, nil
}

func (s *stubOnlineUsersProvider) IsCurrentProcess(core *xray.Process) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return core != nil && core == s.currentProc
}

func (s *stubOnlineUsersProvider) DoForProcess(core *xray.Process, fn func() error) (bool, error) {
	if s.onBeforeDo != nil {
		s.onBeforeDo()
	}
	if core == nil || fn == nil {
		return false, nil
	}
	s.mu.Lock()
	if core != s.currentProc {
		s.mu.Unlock()
		if s.onAfterDo != nil {
			s.onAfterDo(false)
		}
		return false, nil
	}
	err := fn()
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	if s.onAfterDo != nil {
		s.onAfterDo(true)
	}
	return true, nil
}

func (s *stubOnlineUsersProvider) RemoveUserForProcess(core *xray.Process, _, _ string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if core == nil || core != s.currentProc {
		return false, nil
	}
	s.removeCalls = append(s.removeCalls, core)
	return true, nil
}

func (s *stubOnlineUsersProvider) AddUserForProcess(core *xray.Process, _, _ string, _ map[string]any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if core == nil || core != s.currentProc {
		return false, nil
	}
	s.addCalls = append(s.addCalls, core)
	return true, nil
}

func TestCheckClientIpJobRunUsesFreshSamplingForEnforcementAndCacheForCollection(t *testing.T) {
	t.Run("enforcing limit uses fresh sampling instead of stale cache", func(t *testing.T) {
		setupIntegrationDB(t)
		t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
		fakeFail2BanClient(t)
		seedLinkedInboundWithClient(t, "vless-fresh-enforce", "alice@example.test", 1)

		now := time.Now().Unix()
		seedClientIps(t, "alice@example.test", []IPWithTimestamp{{IP: "2.2.2.2", Timestamp: now - 10}})
		proc := xray.NewProcess(&xray.Config{})
		provider := &stubOnlineUsersProvider{
			running:     true,
			sampledProc: proc,
			currentProc: proc,
			cached: []xray.OnlineUser{{
				Email: "alice@example.test",
				IPs: []xray.OnlineIP{
					{IP: "1.1.1.1", LastSeen: now - 3},
					{IP: "2.2.2.2", LastSeen: now - 1},
				},
			}},
			fresh: []xray.OnlineUser{{
				Email: "alice@example.test",
				IPs: []xray.OnlineIP{
					{IP: "2.2.2.2", LastSeen: now},
				},
			}},
		}

		ipJob := &CheckClientIpJob{onlineSource: provider, disconnectWait: func() {}}
		ipJob.Run()

		if provider.freshHits != 1 || provider.cachedHits != 0 {
			t.Fatalf("enforcement hits fresh=%d cached=%d, want fresh=1 cached=0", provider.freshHits, provider.cachedHits)
		}
		if _, err := os.Stat(xray.GetIPLimitLogPath()); !os.IsNotExist(err) {
			t.Fatalf("stale cached IP triggered a ban log: stat err = %v", err)
		}
	})

	t.Run("collection without limit reuses cached snapshot", func(t *testing.T) {
		setupIntegrationDB(t)
		t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
		seedLinkedInboundWithClient(t, "vless-collect-only", "bob@example.test", 0)

		now := time.Now().Unix()
		proc := xray.NewProcess(&xray.Config{})
		provider := &stubOnlineUsersProvider{
			running:     true,
			sampledProc: proc,
			currentProc: proc,
			cached: []xray.OnlineUser{{
				Email: "bob@example.test",
				IPs:   []xray.OnlineIP{{IP: "3.3.3.3", LastSeen: now}},
			}},
		}

		ipJob := &CheckClientIpJob{onlineSource: provider, disconnectWait: func() {}}
		ipJob.Run()

		if provider.cachedHits != 1 || provider.freshHits != 0 {
			t.Fatalf("collection hits cached=%d fresh=%d, want cached=1 fresh=0", provider.cachedHits, provider.freshHits)
		}
	})
}

func TestCheckClientIpJobAbortsEnforcementAcrossXrayRestart(t *testing.T) {
	seedOverLimit := func(t *testing.T, tag, email string) int64 {
		t.Helper()
		setupIntegrationDB(t)
		t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
		fakeFail2BanClient(t)
		seedLinkedInboundWithClient(t, tag, email, 1)
		now := time.Now().Unix()
		seedClientIps(t, email, []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: now - 10}})
		return now
	}

	t.Run("restart after sampling skips ban log and disconnect", func(t *testing.T) {
		now := seedOverLimit(t, "vless-restart-preban", "alice@example.test")
		gen1 := xray.NewProcess(&xray.Config{})
		gen2 := xray.NewProcess(&xray.Config{})
		provider := &stubOnlineUsersProvider{
			running:     true,
			sampledProc: gen1,
			currentProc: gen1,
			fresh: []xray.OnlineUser{{
				Email: "alice@example.test",
				IPs: []xray.OnlineIP{
					{IP: "1.1.1.1", LastSeen: now - 5},
					{IP: "2.2.2.2", LastSeen: now},
				},
			}},
		}
		provider.onSample = func() {
			provider.setCurrentProcess(gen2)
		}

		ipJob := &CheckClientIpJob{onlineSource: provider, disconnectWait: func() {}}
		ipJob.Run()

		if _, err := os.Stat(xray.GetIPLimitLogPath()); !os.IsNotExist(err) {
			t.Fatalf("ban log created after Xray restart: stat err = %v", err)
		}
		if len(provider.removeCalls) != 0 || len(provider.addCalls) != 0 {
			t.Fatalf("unexpected user mutations after restart: remove=%d add=%d", len(provider.removeCalls), len(provider.addCalls))
		}
		if len(ipJob.bannedSeen) != 0 {
			t.Fatalf("aborted scan mutated bannedSeen: %+v", ipJob.bannedSeen)
		}
	})

	t.Run("restart after tx commit before fail2ban log skips log and disconnect", func(t *testing.T) {
		now := seedOverLimit(t, "vless-restart-prelog", "erin@example.test")
		gen1 := xray.NewProcess(&xray.Config{})
		gen2 := xray.NewProcess(&xray.Config{})
		provider := &stubOnlineUsersProvider{
			running:     true,
			sampledProc: gen1,
			currentProc: gen1,
			fresh: []xray.OnlineUser{{
				Email: "erin@example.test",
				IPs: []xray.OnlineIP{
					{IP: "1.1.1.1", LastSeen: now - 5},
					{IP: "2.2.2.2", LastSeen: now},
				},
			}},
		}
		provider.onBeforeDo = func() {
			provider.setCurrentProcess(gen2)
		}

		ipJob := &CheckClientIpJob{onlineSource: provider, disconnectWait: func() {}}
		ipJob.Run()

		if _, err := os.Stat(xray.GetIPLimitLogPath()); !os.IsNotExist(err) {
			t.Fatalf("stale process wrote fail2ban log after restart: stat err = %v", err)
		}
		if len(provider.removeCalls) != 0 || len(provider.addCalls) != 0 {
			t.Fatalf("unexpected user mutations after restart: remove=%d add=%d", len(provider.removeCalls), len(provider.addCalls))
		}
		if len(ipJob.bannedSeen) != 0 {
			t.Fatalf("aborted scan mutated bannedSeen: %+v", ipJob.bannedSeen)
		}
	})

	t.Run("restart after ban log skips disconnect on new process", func(t *testing.T) {
		now := seedOverLimit(t, "vless-restart-predisconnect", "bob@example.test")
		gen1 := xray.NewProcess(&xray.Config{})
		gen2 := xray.NewProcess(&xray.Config{})
		provider := &stubOnlineUsersProvider{
			running:     true,
			sampledProc: gen1,
			currentProc: gen1,
			fresh: []xray.OnlineUser{{
				Email: "bob@example.test",
				IPs: []xray.OnlineIP{
					{IP: "1.1.1.1", LastSeen: now - 5},
					{IP: "2.2.2.2", LastSeen: now},
				},
			}},
		}
		provider.onAfterDo = func(ran bool) {
			if ran {
				provider.setCurrentProcess(gen2)
			}
		}

		ipJob := &CheckClientIpJob{onlineSource: provider, disconnectWait: func() {}}
		ipJob.Run()

		logBytes, err := os.ReadFile(xray.GetIPLimitLogPath())
		if err != nil || !strings.Contains(string(logBytes), "Email = bob@example.test") {
			t.Fatalf("expected ban log before pre-disconnect restart, got %q (err=%v)", string(logBytes), err)
		}
		if len(provider.removeCalls) != 0 || len(provider.addCalls) != 0 {
			t.Fatalf("new process received disconnect after restart: remove=%d add=%d", len(provider.removeCalls), len(provider.addCalls))
		}
	})

	t.Run("restart during disconnect wait skips restore on new process", func(t *testing.T) {
		now := seedOverLimit(t, "vless-restart-midwait", "carol@example.test")
		gen1 := xray.NewProcess(&xray.Config{})
		gen2 := xray.NewProcess(&xray.Config{})
		provider := &stubOnlineUsersProvider{
			running:     true,
			sampledProc: gen1,
			currentProc: gen1,
			fresh: []xray.OnlineUser{{
				Email: "carol@example.test",
				IPs: []xray.OnlineIP{
					{IP: "1.1.1.1", LastSeen: now - 5},
					{IP: "2.2.2.2", LastSeen: now},
				},
			}},
		}

		ipJob := &CheckClientIpJob{
			onlineSource: provider,
			disconnectWait: func() {
				provider.setCurrentProcess(gen2)
			},
		}
		ipJob.Run()

		if len(provider.removeCalls) != 1 || provider.removeCalls[0] != gen1 {
			t.Fatalf("removeCalls = %v, want single remove on gen1", provider.removeCalls)
		}
		if len(provider.addCalls) != 0 {
			t.Fatalf("addCalls = %v, want no restore on replacement process", provider.addCalls)
		}
	})
}

func statementTable(tx *gorm.DB) string {
	if tx == nil || tx.Statement == nil {
		return ""
	}
	if tx.Statement.Table != "" {
		return tx.Statement.Table
	}
	if tx.Statement.Schema != nil {
		return tx.Statement.Schema.Table
	}
	return ""
}

func TestCheckClientIpJobDoesNotWaitForRestartLockInsideTransaction(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
	fakeFail2BanClient(t)
	seedLinkedInboundWithClient(t, "vless-restart-lock-order", "dave@example.test", 1)

	now := time.Now().Unix()
	seedClientIps(t, "dave@example.test", []IPWithTimestamp{{IP: "1.1.1.1", Timestamp: now - 10}})

	gen1 := xray.NewProcess(&xray.Config{})
	gen2 := xray.NewProcess(&xray.Config{})
	provider := &stubOnlineUsersProvider{
		running:     true,
		sampledProc: gen1,
		currentProc: gen1,
		fresh: []xray.OnlineUser{{
			Email: "dave@example.test",
			IPs: []xray.OnlineIP{
				{IP: "1.1.1.1", LastSeen: now - 5},
				{IP: "2.2.2.2", LastSeen: now},
			},
		}},
	}

	enteredTx := make(chan struct{})
	restartHoldingLock := make(chan struct{})
	stopRestart := make(chan struct{})
	var enteredOnce sync.Once
	var enteredTxFired atomic.Bool

	if err := database.GetDB().Callback().Update().Before("gorm:update").Register("test:simulate_xray_restart_during_ip_tx", func(txDB *gorm.DB) {
		if statementTable(txDB) != "inbound_client_ips" {
			return
		}
		enteredOnce.Do(func() {
			enteredTxFired.Store(true)
			close(enteredTx)
			select {
			case <-restartHoldingLock:
			case <-time.After(5 * time.Second):
			}
		})
	}); err != nil {
		t.Fatalf("register update callback: %v", err)
	}

	restartDone := make(chan struct{})
	var restartWriteErr error
	go func() {
		defer close(restartDone)
		select {
		case <-enteredTx:
		case <-stopRestart:
			return
		}
		provider.mu.Lock()
		close(restartHoldingLock)
		restartWriteErr = database.GetDB().Create(&model.Setting{
			Key:   "restartConcurrentWrite",
			Value: "1",
		}).Error
		provider.currentProc = gen2
		provider.mu.Unlock()
	}()

	ipJob := &CheckClientIpJob{onlineSource: provider, disconnectWait: func() {}}
	start := time.Now()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		ipJob.Run()
	}()

	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("CheckClientIpJob.Run stalled while concurrent Xray restart held restart lock")
	}
	if !enteredTxFired.Load() {
		close(stopRestart)
		<-restartDone
		t.Fatal("expected inbound_client_ips update callback inside transaction to fire")
	}
	select {
	case <-restartDone:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Xray restart goroutine stalled on database write")
	}

	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("scan and restart waited %v, indicating lock-order contention", elapsed)
	}
	if restartWriteErr != nil {
		t.Fatalf("concurrent restart DB write failed: %v", restartWriteErr)
	}

	var saved model.Setting
	if err := database.GetDB().Where("key = ?", "restartConcurrentWrite").First(&saved).Error; err != nil {
		t.Fatalf("load restartConcurrentWrite setting: %v", err)
	}
	if saved.Value != "1" {
		t.Fatalf("restartConcurrentWrite value = %q, want %q", saved.Value, "1")
	}
	if _, err := os.Stat(xray.GetIPLimitLogPath()); !os.IsNotExist(err) {
		t.Fatalf("stale process wrote fail2ban log after concurrent restart: stat err = %v", err)
	}
	if len(provider.removeCalls) != 0 || len(provider.addCalls) != 0 {
		t.Fatalf("unexpected user mutations after concurrent restart: remove=%d add=%d", len(provider.removeCalls), len(provider.addCalls))
	}
	if len(ipJob.bannedSeen) != 0 {
		t.Fatalf("aborted scan mutated bannedSeen: %+v", ipJob.bannedSeen)
	}
}
