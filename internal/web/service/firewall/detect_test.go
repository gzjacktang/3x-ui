package firewall

import (
	"testing"
)

func TestNftLooksLikeHostFirewall(t *testing.T) {
	t.Parallel()
	if nftLooksLikeHostFirewall("") {
		t.Fatal("empty ruleset")
	}
	open := "table inet filter {\n chain input { type filter hook input priority filter; policy accept; }\n}\n"
	if nftLooksLikeHostFirewall(open) {
		t.Fatal("accept policy should not look restrictive")
	}
	drop := "table inet filter {\n chain input { type filter hook input priority filter; policy drop; }\n}\n"
	if !nftLooksLikeHostFirewall(drop) {
		t.Fatal("drop policy should look restrictive")
	}
}

func TestIptablesLooksLikeHostFirewall(t *testing.T) {
	t.Parallel()
	if iptablesLooksLikeHostFirewall("-P INPUT ACCEPT\n") {
		t.Fatal("accept policy")
	}
	if !iptablesLooksLikeHostFirewall("-P INPUT DROP\n") {
		t.Fatal("drop policy")
	}
	established := "-A INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT\n-A INPUT -j DROP\n"
	if !iptablesLooksLikeHostFirewall(established) {
		t.Fatal("final drop should count")
	}
}

func TestTakeoverRecommended(t *testing.T) {
	t.Parallel()
	foreign := []ForeignFirewall{{Name: "nftables", Active: true}}
	if !takeoverRecommended(false, foreign) {
		t.Fatal("expected recommended")
	}
	if takeoverRecommended(true, foreign) {
		t.Fatal("ufw active should suppress recommendation")
	}
}
