package firewall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrTakeoverNotConfirmed = fmt.Errorf("takeover requires confirm=true")
	ErrTakeoverNotNeeded    = fmt.Errorf("no foreign host firewall detected for takeover")
)

type TakeoverResult struct {
	BackupDir string `json:"backupDir"`
}

func (s *Service) backupRoot() string {
	if s.BackupDir != "" {
		return s.BackupDir
	}
	return "/etc/x-ui/firewall-backups"
}

func (s *Service) Takeover(confirm bool) (TakeoverResult, error) {
	if !confirm {
		return TakeoverResult{}, ErrTakeoverNotConfirmed
	}

	st, err := s.Status()
	if err != nil {
		return TakeoverResult{}, err
	}
	if !st.TakeoverRecommended && !hasActiveForeign(st.Foreign) {
		return TakeoverResult{}, ErrTakeoverNotNeeded
	}

	dir := filepath.Join(s.backupRoot(), time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return TakeoverResult{}, fmt.Errorf("create backup dir: %w", err)
	}
	if err := s.backupForeignRules(dir); err != nil {
		return TakeoverResult{}, err
	}

	if err := s.Install(); err != nil {
		return TakeoverResult{}, err
	}

	if err := s.disableForeignFirewalls(); err != nil {
		return TakeoverResult{}, err
	}

	if err := s.ensureProtectedAllows(); err != nil {
		return TakeoverResult{}, err
	}
	out, err := s.run("ufw", "--force", "enable")
	if err != nil {
		return TakeoverResult{}, fmt.Errorf("ufw enable failed after takeover: %w (%s)", err, strings.TrimSpace(out))
	}

	return TakeoverResult{BackupDir: dir}, nil
}

func hasActiveForeign(foreign []ForeignFirewall) bool {
	for _, fw := range foreign {
		if fw.Active {
			return true
		}
	}
	return false
}

func (s *Service) backupForeignRules(dir string) error {
	write := func(name, body string) error {
		path := filepath.Join(dir, name)
		return os.WriteFile(path, []byte(body), 0o600)
	}

	if _, err := s.lookPath()("nft"); err == nil {
		out, _ := s.run("nft", "list", "ruleset")
		if err := write("nft.rules", out); err != nil {
			return err
		}
	}
	if _, err := s.lookPath()("iptables-save"); err == nil {
		out, _ := s.run("iptables-save")
		if err := write("iptables.v4", out); err != nil {
			return err
		}
	}
	if _, err := s.lookPath()("ip6tables-save"); err == nil {
		out, _ := s.run("ip6tables-save")
		if err := write("iptables.v6", out); err != nil {
			return err
		}
	}
	if _, err := s.lookPath()("firewall-cmd"); err == nil {
		out, _ := s.run("firewall-cmd", "--list-all-zones")
		if err := write("firewalld.zones", out); err != nil {
			return err
		}
	}
	note := "Backup created by 3x-ui firewall takeover before disabling foreign host firewalls.\n"
	return write("README.txt", note)
}

func (s *Service) disableForeignFirewalls() error {
	if _, err := s.lookPath()("systemctl"); err == nil {
		_, _ = s.run("systemctl", "stop", "firewalld")
		_, _ = s.run("systemctl", "disable", "firewalld")
		_, _ = s.run("systemctl", "stop", "nftables")
		_, _ = s.run("systemctl", "disable", "nftables")
	}

	if _, err := s.lookPath()("nft"); err == nil {
		out, err := s.run("nft", "flush", "ruleset")
		if err != nil {
			return fmt.Errorf("nft flush ruleset failed: %w (%s)", err, strings.TrimSpace(out))
		}
	}

	if err := s.openIptables("iptables"); err != nil {
		return err
	}
	if err := s.openIptables("ip6tables"); err != nil {
		return err
	}
	return nil
}

func (s *Service) openIptables(bin string) error {
	if _, err := s.lookPath()(bin); err != nil {
		return nil
	}
	steps := [][]string{
		{bin, "-F"},
		{bin, "-X"},
		{bin, "-t", "nat", "-F"},
		{bin, "-t", "nat", "-X"},
		{bin, "-t", "mangle", "-F"},
		{bin, "-t", "mangle", "-X"},
		{bin, "-P", "INPUT", "ACCEPT"},
		{bin, "-P", "FORWARD", "ACCEPT"},
		{bin, "-P", "OUTPUT", "ACCEPT"},
	}
	for _, args := range steps {
		out, err := s.run(args[0], args[1:]...)
		if err != nil {
			return fmt.Errorf("%s failed: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(out))
		}
	}
	return nil
}
