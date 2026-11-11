package firewall

import (
	"strings"
)

type ForeignFirewall struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Detail string `json:"detail,omitempty"`
}

func (s *Service) detectForeign(ufwActive bool) []ForeignFirewall {
	out := make([]ForeignFirewall, 0, 3)
	if fw := s.detectFirewalld(); fw != nil {
		out = append(out, *fw)
	}
	if fw := s.detectNftables(ufwActive); fw != nil {
		out = append(out, *fw)
	}
	if fw := s.detectIptables(ufwActive); fw != nil {
		out = append(out, *fw)
	}
	return out
}

func takeoverRecommended(ufwActive bool, foreign []ForeignFirewall) bool {
	if ufwActive {
		return false
	}
	for _, fw := range foreign {
		if fw.Active {
			return true
		}
	}
	return false
}

func (s *Service) detectFirewalld() *ForeignFirewall {
	if _, err := s.lookPath()("firewall-cmd"); err != nil {
		if _, err := s.lookPath()("firewalld"); err != nil {
			return nil
		}
	}
	fw := &ForeignFirewall{Name: "firewalld"}
	out, err := s.run("systemctl", "is-active", "firewalld")
	state := strings.TrimSpace(out)
	if err == nil && state == "active" {
		fw.Active = true
		fw.Detail = "firewalld service is active"
		return fw
	}
	if state != "" && state != "inactive" && state != "unknown" {
		fw.Detail = "firewalld state: " + state
	}
	return fw
}

func (s *Service) detectNftables(ufwActive bool) *ForeignFirewall {
	if _, err := s.lookPath()("nft"); err != nil {
		return nil
	}
	fw := &ForeignFirewall{Name: "nftables"}
	out, err := s.run("nft", "list", "ruleset")
	if err != nil {
		fw.Detail = strings.TrimSpace(out)
		if fw.Detail == "" {
			fw.Detail = err.Error()
		}
		return fw
	}
	if ufwActive {
		fw.Detail = "nftables present; managed by UFW"
		return fw
	}
	if nftLooksLikeHostFirewall(out) {
		fw.Active = true
		fw.Detail = "nftables filter rules appear to restrict inbound traffic"
	}
	return fw
}

func (s *Service) detectIptables(ufwActive bool) *ForeignFirewall {
	if _, err := s.lookPath()("iptables"); err != nil {
		return nil
	}
	fw := &ForeignFirewall{Name: "iptables"}
	out, err := s.run("iptables", "-S", "INPUT")
	if err != nil {
		fw.Detail = strings.TrimSpace(out)
		if fw.Detail == "" {
			fw.Detail = err.Error()
		}
		return fw
	}
	if ufwActive {
		fw.Detail = "iptables present; managed by UFW"
		return fw
	}
	if iptablesLooksLikeHostFirewall(out) {
		fw.Active = true
		fw.Detail = "iptables INPUT policy or rules appear to restrict inbound traffic"
	}
	return fw
}

func nftLooksLikeHostFirewall(ruleset string) bool {
	lower := strings.ToLower(ruleset)
	if !strings.Contains(lower, "hook input") && !strings.Contains(lower, "chain input") {
		return false
	}
	if strings.Contains(lower, "policy drop") || strings.Contains(lower, "policy reject") {
		return true
	}
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "hook input") || strings.Contains(line, "chain input") {
			continue
		}
		if strings.HasSuffix(line, " drop") || strings.HasSuffix(line, " reject") ||
			strings.Contains(line, " drop ") || strings.Contains(line, " reject ") {
			return true
		}
	}
	return false
}

func iptablesLooksLikeHostFirewall(rules string) bool {
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "-P INPUT DROP") || strings.HasPrefix(upper, "-P INPUT REJECT") {
			return true
		}
		if strings.Contains(upper, " -J DROP") || strings.Contains(upper, " -J REJECT") {
			if strings.Contains(upper, "ESTABLISHED") || strings.Contains(upper, "RELATED") {
				continue
			}
			return true
		}
	}
	return false
}
