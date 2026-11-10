package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizePortSpec(t *testing.T) {
	t.Parallel()

	ok, err := normalizePortSpec(PortSpec{Start: 443, Proto: ProtoTCP})
	if err != nil {
		t.Fatal(err)
	}
	if ok.End != 443 || ok.Proto != ProtoTCP {
		t.Fatalf("unexpected normalized spec: %+v", ok)
	}

	if _, err := normalizePortSpec(PortSpec{Start: 0, Proto: ProtoTCP}); !errors.Is(err, ErrInvalidPort) {
		t.Fatalf("want ErrInvalidPort, got %v", err)
	}
	if _, err := normalizePortSpec(PortSpec{Start: 80, End: 70, Proto: ProtoTCP}); !errors.Is(err, ErrInvalidPort) {
		t.Fatalf("want ErrInvalidPort for reversed range, got %v", err)
	}
	if _, err := normalizePortSpec(PortSpec{Start: 80, Proto: "sctp"}); !errors.Is(err, ErrInvalidProto) {
		t.Fatalf("want ErrInvalidProto, got %v", err)
	}
}

func TestParseNumberedRulesMarksProtected(t *testing.T) {
	t.Parallel()

	out := strings.Join([]string{
		"Status: active",
		"",
		"     To                         Action      From",
		"     --                         ------      ----",
		"[ 1] OpenSSH                    ALLOW IN    Anywhere",
		"[ 2] 2053/tcp                   ALLOW IN    Anywhere",
		"[ 3] 443/tcp                    ALLOW IN    Anywhere",
		"[ 4] 400:500/tcp                ALLOW IN    Anywhere",
		"[ 5] 8443/udp                   ALLOW IN    Anywhere",
	}, "\n")

	protected := []ProtectedPort{
		{Port: 22, Proto: ProtoTCP, Label: "22/tcp (SSH)"},
		{Port: 2053, Proto: ProtoTCP, Label: "2053/tcp (panel)"},
	}
	rules := parseNumberedRules(out, protected)
	if len(rules) != 5 {
		t.Fatalf("got %d rules", len(rules))
	}
	if !rules[0].Protected || !rules[1].Protected {
		t.Fatalf("SSH/panel rules should be protected: %+v", rules[:2])
	}
	if rules[2].Protected {
		t.Fatalf("443 should not be protected: %+v", rules[2])
	}
	if rules[3].Protected {
		t.Fatalf("400-500 does not cover panel port and must not be protected: %+v", rules[3])
	}
	if rules[4].Protected {
		t.Fatalf("udp 8443 should not be protected: %+v", rules[4])
	}
}

func TestParseNumberedRulesRangeCoversPanel(t *testing.T) {
	t.Parallel()
	out := "[ 1] 2000:3000/tcp              ALLOW IN    Anywhere\n"
	protected := []ProtectedPort{{Port: 2053, Proto: ProtoTCP}}
	rules := parseNumberedRules(out, protected)
	if len(rules) != 1 || !rules[0].Protected {
		t.Fatalf("range covering panel must be protected: %+v", rules)
	}
}

func TestEnableAllowsProtectedThenEnables(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sshPath, err := WriteSSHConfigForTest(dir, 2222)
	if err != nil {
		t.Fatal(err)
	}

	var calls [][]string
	svc := &Service{
		SSHConfig: sshPath,
		PanelPortFn: func() (int, error) { return 2053, nil },
		LookPath: func(file string) (string, error) {
			if file == "ufw" {
				return "/usr/sbin/ufw", nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			calls = append(calls, append([]string{name}, args...))
			joined := strings.Join(args, " ")
			if joined == "status numbered" {
				return "Status: inactive\n", nil
			}
			return "ok\n", nil
		},
	}

	if err := svc.Enable(); err != nil {
		t.Fatal(err)
	}

	wantAllow := []string{
		"ufw allow 2222/tcp",
		"ufw allow 2053/tcp",
		"ufw --force enable",
	}
	got := make([]string, 0, len(calls))
	for _, c := range calls {
		got = append(got, strings.Join(c, " "))
	}
	for _, w := range wantAllow {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing call %q in %v", w, got)
		}
	}
}

func TestDeleteByNumberRefusesProtected(t *testing.T) {
	t.Parallel()

	svc := &Service{
		PanelPortFn: func() (int, error) { return 2053, nil },
		SSHConfig:   filepath.Join(t.TempDir(), "missing"),
		LookPath: func(file string) (string, error) {
			if file == "ufw" {
				return "/usr/sbin/ufw", nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			if len(args) >= 2 && args[0] == "status" {
				return "Status: active\n[ 1] 2053/tcp                   ALLOW IN    Anywhere\n[ 2] 443/tcp                    ALLOW IN    Anywhere\n", nil
			}
			t.Fatalf("unexpected command: %s %v", name, args)
			return "", nil
		},
	}

	if err := svc.DeleteByNumber(1); !errors.Is(err, ErrProtectedPort) {
		t.Fatalf("want ErrProtectedPort, got %v", err)
	}
}

func TestDeleteAllowRefusesProtectedPort(t *testing.T) {
	t.Parallel()

	svc := &Service{
		PanelPortFn: func() (int, error) { return 2053, nil },
		SSHConfig:   filepath.Join(t.TempDir(), "missing"),
		LookPath: func(file string) (string, error) {
			if file == "ufw" {
				return "/usr/sbin/ufw", nil
			}
			return "", errors.New("not found")
		},
		Run: func(context.Context, string, ...string) (string, error) {
			t.Fatal("ufw should not be invoked for protected delete")
			return "", nil
		},
	}

	err := svc.DeleteAllow(PortSpec{Start: 2053, Proto: ProtoTCP})
	if !errors.Is(err, ErrProtectedPort) {
		t.Fatalf("want ErrProtectedPort, got %v", err)
	}
}

func TestDeleteByNumberRemovesUnprotected(t *testing.T) {
	t.Parallel()

	var deleted bool
	svc := &Service{
		PanelPortFn: func() (int, error) { return 2053, nil },
		SSHConfig:   filepath.Join(t.TempDir(), "missing"),
		LookPath: func(file string) (string, error) {
			if file == "ufw" {
				return "/usr/sbin/ufw", nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, _ string, args ...string) (string, error) {
			if len(args) >= 2 && args[0] == "status" {
				return "Status: active\n[ 2] 443/tcp                    ALLOW IN    Anywhere\n", nil
			}
			if len(args) >= 3 && args[0] == "--force" && args[1] == "delete" && args[2] == "2" {
				deleted = true
				return "Rule deleted\n", nil
			}
			return "", errors.New("unexpected")
		},
	}

	if err := svc.DeleteByNumber(2); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected delete call")
	}
}

func TestAllowInvalidRejectedWithoutExec(t *testing.T) {
	t.Parallel()
	svc := &Service{
		LookPath: func(string) (string, error) { return "/usr/sbin/ufw", nil },
		Run: func(context.Context, string, ...string) (string, error) {
			t.Fatal("should not run")
			return "", nil
		},
	}
	if err := svc.Allow(PortSpec{Start: 70000, Proto: ProtoTCP}); !errors.Is(err, ErrInvalidPort) {
		t.Fatalf("want ErrInvalidPort, got %v", err)
	}
}

func TestStatusWhenUFWMissing(t *testing.T) {
	t.Parallel()
	svc := &Service{
		PanelPortFn: func() (int, error) { return 2053, nil },
		SSHConfig:   filepath.Join(t.TempDir(), "missing"),
		LookPath:    func(string) (string, error) { return "", os.ErrNotExist },
	}
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Installed || st.Available {
		t.Fatalf("expected unavailable status: %+v", st)
	}
	if st.Message == "" {
		t.Fatal("expected message")
	}
}

func TestSSHPortFromConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, err := WriteSSHConfigForTest(dir, 2200)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{SSHConfig: path}
	if got := svc.sshPort(); got != 2200 {
		t.Fatalf("got %d", got)
	}
}
