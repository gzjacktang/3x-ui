package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTakeoverRequiresConfirm(t *testing.T) {
	t.Parallel()
	svc := &Service{
		PanelPortFn: func() (int, error) { return 2053, nil },
		LookPath:    func(string) (string, error) { return "", os.ErrNotExist },
	}
	if _, err := svc.Takeover(false); !errors.Is(err, ErrTakeoverNotConfirmed) {
		t.Fatalf("got %v", err)
	}
}

func TestTakeoverDisablesForeignAndEnablesUFW(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sshPath, err := WriteSSHConfigForTest(dir, 22282)
	if err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(dir, "backups")
	bins := map[string]string{
		"ufw":            "/usr/sbin/ufw",
		"apt-get":        "/usr/bin/apt-get",
		"nft":            "/usr/sbin/nft",
		"iptables":       "/usr/sbin/iptables",
		"iptables-save":  "/usr/sbin/iptables-save",
		"ip6tables":      "/usr/sbin/ip6tables",
		"ip6tables-save": "/usr/sbin/ip6tables-save",
		"systemctl":      "/bin/systemctl",
	}
	var calls []string
	svc := &Service{
		SSHConfig:   sshPath,
		BackupDir:   backupRoot,
		PanelPortFn: func() (int, error) { return 20223, nil },
		LookPath: func(file string) (string, error) {
			if path, ok := bins[file]; ok {
				return path, nil
			}
			return "", os.ErrNotExist
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			joined := strings.Join(append([]string{name}, args...), " ")
			calls = append(calls, joined)
			switch {
			case joined == "ufw status numbered":
				return "Status: inactive\n", nil
			case joined == "nft list ruleset":
				return "table inet filter { chain input { type filter hook input priority filter; policy drop; } }\n", nil
			case joined == "iptables -S INPUT":
				return "-P INPUT DROP\n", nil
			case strings.HasPrefix(joined, "iptables-save"), strings.HasPrefix(joined, "ip6tables-save"):
				return "# saved\n", nil
			case strings.HasPrefix(joined, "systemctl"):
				return "inactive\n", nil
			case joined == "nft flush ruleset":
				return "", nil
			case strings.HasPrefix(joined, "iptables "), strings.HasPrefix(joined, "ip6tables "):
				return "", nil
			case joined == "ufw allow 22282/tcp", joined == "ufw allow 20223/tcp":
				return "ok\n", nil
			case joined == "ufw --force enable":
				return "Firewall is active\n", nil
			default:
				return "", errors.New("unexpected: " + joined)
			}
		},
	}

	result, err := svc.Takeover(true)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupDir == "" {
		t.Fatal("expected backup dir")
	}
	if _, err := os.Stat(filepath.Join(result.BackupDir, "nft.rules")); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"nft flush ruleset",
		"ufw allow 22282/tcp",
		"ufw allow 20223/tcp",
		"ufw --force enable",
	}
	for _, w := range want {
		found := false
		for _, c := range calls {
			if c == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing call %q in %v", w, calls)
		}
	}
}
